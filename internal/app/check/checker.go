package checkapp

import (
	"context"
	"errors"
	"strings"
	"time"

	contentapp "reader/internal/app/content"
	diagnosticapp "reader/internal/app/diagnostic"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

const (
	FailureTimeout         = diagnosticapp.FailureTimeout
	FailureJSFailed        = diagnosticapp.FailureJSFailed
	FailureWebsiteFailed   = diagnosticapp.FailureWebsiteFailed
	FailureSearchFailed    = diagnosticapp.FailureSearchFailed
	FailureDiscoveryFailed = diagnosticapp.FailureDiscoveryFailed
	FailureInfoFailed      = diagnosticapp.FailureInfoFailed
	FailureTocFailed       = diagnosticapp.FailureTocFailed
	FailureContentFailed   = diagnosticapp.FailureContentFailed
	// FailureSearchRuleEmpty / FailureDiscoveryRuleEmpty 表示书源缺少搜索/发现规则，对齐 Legado 分组语义。
	FailureSearchRuleEmpty    = diagnosticapp.FailureSearchRuleEmpty
	FailureDiscoveryRuleEmpty = diagnosticapp.FailureDiscoveryRuleEmpty
)

const (
	// DefaultCheckKeyword 是书源未配置 checkKeyWord 且调用方未指定关键字时的兜底关键字，
	// 对齐 Legado CheckSource.keyword 默认值与 spec CheckOptions.keyword 默认值。
	DefaultCheckKeyword = "我的"
	// DefaultTimeout 是单本书源校验的整体超时，对齐 Legado CheckSource.timeout（180000ms）
	// 与 spec CheckOptions.timeoutMs 默认值；整轮校验（搜索/详情/目录/正文）都受它约束。
	DefaultTimeout = 180 * time.Second
)

const fileSourceType = 3

type SearchService interface {
	Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error)
}

type DiscoveryService interface {
	Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error)
	// Categories 返回书源发现的分类列表；用于按 Legado 语义区分“发现规则为空”和“发现失效”。
	Categories(ctx context.Context, sourceID string) ([]discoveryapp.Category, error)
}

type BookService interface {
	Save(ctx context.Context, b *book.Book) (*book.Book, error)
	RefreshInfo(ctx context.Context, id string) (book.Book, error)
	RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error)
}

type ContentService interface {
	GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error)
}

type Options struct {
	Keyword        string
	CheckSearch    bool
	CheckDiscovery bool
	CheckInfo      bool
	CheckToc       bool
	CheckContent   bool
	Timeout        time.Duration
}

type Checker struct {
	search    SearchService
	discovery DiscoveryService
	books     BookService
	content   ContentService
}

func NewChecker(search SearchService, discovery DiscoveryService, books BookService, content ContentService) *Checker {
	return &Checker{search: search, discovery: discovery, books: books, content: content}
}

func (c *Checker) CheckSource(ctx context.Context, src *source.BookSource, opts Options) (*task.CheckTaskItem, error) {
	start := time.Now()
	opts.Keyword = checkKeyword(src, opts.Keyword)
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	// Legado CheckSourceService 用 withTimeout 包住整本书源的校验过程，Reader 保持同一语义。
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	item := &task.CheckTaskItem{
		SourceID:       src.SourceID,
		BookSourceURL:  src.BookSourceURL,
		BookSourceName: src.BookSourceName,
		Status:         task.CheckItemPassed,
		TimeoutMS:      opts.Timeout.Milliseconds(),
	}
	if opts.CheckContent && unsupportedContentBrowserFlow(src) {
		// contentRule.sourceRegex 在 Legado 里只在 webView 中生效（WebBook.kt:335-337 把 sourceRegex/webJs
		// 交给 getStrResponseAwait，由 WebViewClient.onLoadResource 嗅探页面资源请求），Reader 不提供 webView。
		// 校验阶段就给出明确失败，避免用户到抓正文时才发现（对齐 content.GetContent 的 ErrUnsupportedContentBrowserFlow）。
		item.Path = contentCheckPath(opts)
		item.Stages = append(item.Stages, task.CheckStageResult{
			Flow:         contentCheckFlow(opts),
			Stage:        task.CheckStageContent,
			Status:       task.CheckItemFailed,
			FailureCode:  FailureContentFailed,
			FailureClass: diagnosticapp.FailureClassSourceUnsupportedRule,
			Message:      contentapp.ErrUnsupportedContentBrowserFlow.Error(),
		})
		applyStageFailures(item)
		item.RespondTime = time.Since(start).Milliseconds()
		return item, nil
	}
	var firstErr error
	attempted := false
	if opts.CheckSearch {
		item.KeywordUsed = opts.Keyword
		switch {
		case strings.TrimSpace(src.SearchURL) == "" && src.BookSourceType == fileSourceType:
			// 文件源没有搜索链接规则属于正常形态：它不是搜索失败，跳过搜索阶段。
			item.Stages = append(item.Stages, skippedStage(task.CheckFlowSearch, task.CheckStageSearch, "not applicable: searchUrl is empty"))
		case strings.TrimSpace(src.SearchURL) == "":
			// Legado：没有搜索链接规则时不执行搜索，直接标记“搜索链接规则为空”。
			attempted = true
			item.Stages = append(item.Stages, ruleEmptyStage(task.CheckFlowSearch, task.CheckStageSearch, FailureSearchRuleEmpty, "searchUrl rule is empty"))
			appendSkippedBookStages(item, task.CheckFlowSearch, opts, src.BookSourceType, "searchUrl rule is empty")
		default:
			attempted = true
			stageStart := time.Now()
			result, err := c.search.Search(ctx, searchapp.MultiSearchInput{Keyword: opts.Keyword, Page: 1, SourceIDs: []string{src.SourceID}, EnabledOnly: false, Concurrency: 1})
			if err == nil && len(result.Warnings) > 0 {
				err = errors.New(result.Warnings[0].Message)
			}
			if err != nil {
				item.Stages = append(item.Stages, failedStage(task.CheckFlowSearch, task.CheckStageSearch, stageStart, err, FailureSearchFailed))
				appendSkippedBookStages(item, task.CheckFlowSearch, opts, src.BookSourceType, "search failed")
				firstErr = err
			} else if len(result.Items) == 0 {
				emptyErr := errors.New("empty search result")
				item.Stages = append(item.Stages, failedStage(task.CheckFlowSearch, task.CheckStageSearch, stageStart, emptyErr, FailureSearchFailed))
				appendSkippedBookStages(item, task.CheckFlowSearch, opts, src.BookSourceType, "search returned no books")
			} else {
				item.Stages = append(item.Stages, passedStage(task.CheckFlowSearch, task.CheckStageSearch, stageStart))
				_ = c.checkBook(ctx, result.Items[0].ToBook(), src.BookSourceType, task.CheckFlowSearch, opts, item)
			}
		}
	}
	if opts.CheckDiscovery && strings.TrimSpace(src.ExploreURL) != "" {
		attempted = true
		stageStart := time.Now()
		categories, err := c.discovery.Categories(ctx, src.SourceID)
		switch {
		case err != nil:
			item.Stages = append(item.Stages, failedStage(task.CheckFlowDiscovery, task.CheckStageDiscovery, stageStart, err, FailureDiscoveryFailed))
			appendSkippedBookStages(item, task.CheckFlowDiscovery, opts, src.BookSourceType, "categories failed")
			if firstErr == nil {
				firstErr = err
			}
		case len(categories) == 0:
			// Legado：exploreUrl 非空却解析不出任何可用分类时标记“发现规则为空”。
			item.Stages = append(item.Stages, ruleEmptyStage(task.CheckFlowDiscovery, task.CheckStageDiscovery, FailureDiscoveryRuleEmpty, "exploreUrl rule is empty"))
			appendSkippedBookStages(item, task.CheckFlowDiscovery, opts, src.BookSourceType, "exploreUrl rule is empty")
		default:
			result, err := c.discovery.Discover(ctx, discoveryapp.Input{SourceIDs: []string{src.SourceID}, Page: 1})
			if err == nil && len(result.Warnings) > 0 {
				err = errors.New(result.Warnings[0].Message)
			}
			if err != nil {
				item.Stages = append(item.Stages, failedStage(task.CheckFlowDiscovery, task.CheckStageDiscovery, stageStart, err, FailureDiscoveryFailed))
				appendSkippedBookStages(item, task.CheckFlowDiscovery, opts, src.BookSourceType, "discovery failed")
				if firstErr == nil {
					firstErr = err
				}
			} else if len(result.Items) == 0 {
				emptyErr := errors.New("empty discovery result")
				item.Stages = append(item.Stages, failedStage(task.CheckFlowDiscovery, task.CheckStageDiscovery, stageStart, emptyErr, FailureDiscoveryFailed))
				appendSkippedBookStages(item, task.CheckFlowDiscovery, opts, src.BookSourceType, "discovery returned no books")
			} else {
				item.Stages = append(item.Stages, passedStage(task.CheckFlowDiscovery, task.CheckStageDiscovery, stageStart))
				_ = c.checkBook(ctx, result.Items[0].ToBook(), src.BookSourceType, task.CheckFlowDiscovery, opts, item)
			}
		}
	}
	if !attempted {
		item.Status = task.CheckItemSkipped
		item.Message = "no applicable check flow"
	}
	applyStageFailures(item)
	item.RespondTime = time.Since(start).Milliseconds()
	return item, firstErr
}

func (c *Checker) checkBook(ctx context.Context, b book.Book, sourceType int, flow task.CheckStageFlow, opts Options, item *task.CheckTaskItem) error {
	// 条目仍带链路标记：诊断分组按 Legado 语义区分“搜索目录失效”与“发现目录失效”。
	item.Path = checkItemPath(flow)
	saved, err := c.books.Save(ctx, &b)
	if err != nil {
		item.Stages = append(item.Stages, failedStage(flow, task.CheckStageInfo, time.Now(), err, FailureInfoFailed))
		appendSkippedAfterInfo(item, flow, opts, sourceType, "book save failed")
		return err
	}
	bookID := book.IDFromURL(saved.BookURL)
	if opts.CheckInfo {
		stageStart := time.Now()
		info, err := c.books.RefreshInfo(ctx, bookID)
		if err != nil {
			item.Stages = append(item.Stages, failedStage(flow, task.CheckStageInfo, stageStart, err, FailureInfoFailed))
			appendSkippedAfterInfo(item, flow, opts, sourceType, "info failed")
			return err
		}
		if sourceType == fileSourceType && len(info.DownloadURLs) == 0 {
			err := errors.New("empty download urls")
			item.Stages = append(item.Stages, failedStage(flow, task.CheckStageInfo, stageStart, err, FailureInfoFailed))
			appendSkippedAfterInfo(item, flow, opts, sourceType, "download urls are empty")
			return nil
		}
		item.Stages = append(item.Stages, passedStage(flow, task.CheckStageInfo, stageStart))
	} else {
		item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageInfo, "disabled"))
	}
	if sourceType == fileSourceType {
		if opts.CheckToc {
			item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageToc, "not applicable to file source"))
		}
		if opts.CheckContent {
			item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageContent, "not applicable to file source"))
		}
		return nil
	}
	if opts.CheckToc {
		stageStart := time.Now()
		chapters, _, err := c.books.RefreshToc(ctx, bookID)
		if err != nil {
			item.Stages = append(item.Stages, failedStage(flow, task.CheckStageToc, stageStart, err, FailureTocFailed))
			if opts.CheckContent {
				item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageContent, "toc failed"))
			}
			return err
		}
		if len(chapters) == 0 {
			err := errors.New("empty toc")
			item.Stages = append(item.Stages, failedStage(flow, task.CheckStageToc, stageStart, err, FailureTocFailed))
			if opts.CheckContent {
				item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageContent, "toc returned no chapters"))
			}
			return nil
		}
		item.Stages = append(item.Stages, passedStage(flow, task.CheckStageToc, stageStart))
		if opts.CheckContent {
			stageStart := time.Now()
			index := firstContentIndex(chapters)
			if _, _, err := c.content.GetChapterContent(ctx, bookID, index); err != nil {
				item.Stages = append(item.Stages, failedStage(flow, task.CheckStageContent, stageStart, err, FailureContentFailed))
				return err
			}
			item.Stages = append(item.Stages, passedStage(flow, task.CheckStageContent, stageStart))
		}
	} else if opts.CheckContent {
		item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageContent, "toc check is disabled"))
	}
	return nil
}

// checkKeyword 按 Legado BookSource.getCheckKeyword 语义选取校验关键字：
// 书源自身的 RuleSearch.CheckKeyWord 优先，为空（或写成了 execute 规则）时才回退到调用方给出的默认关键字。
func checkKeyword(src *source.BookSource, fallback string) string {
	keyword := ""
	if src != nil && src.RuleSearch.Value != nil {
		keyword = strings.TrimSpace(src.RuleSearch.Value.CheckKeyWord)
	}
	if keyword != "" && !strings.Contains(keyword, "http") && !strings.Contains(keyword, "::") && !strings.Contains(keyword, "++") && !strings.Contains(keyword, "--") {
		return keyword
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return DefaultCheckKeyword
}

// unsupportedContentBrowserFlow 判断书源正文规则是否依赖 Reader 不支持的 webView 嗅探（sourceRegex）。
func unsupportedContentBrowserFlow(src *source.BookSource) bool {
	return src != nil && src.RuleContent.Value != nil && strings.TrimSpace(src.RuleContent.Value.SourceRegex) != ""
}

// contentCheckPath 决定正文类失败归入哪条链路：与 checkBook 用同一规则（搜索链路可用即归搜索链路）。
func contentCheckPath(opts Options) task.CheckItemPath {
	if opts.CheckSearch {
		return task.CheckPathSearch
	}
	return task.CheckPathDiscovery
}

// contentCheckFlow 是 contentCheckPath 的阶段化写法，供阶段结果记录链路。
func contentCheckFlow(opts Options) task.CheckStageFlow {
	if opts.CheckSearch {
		return task.CheckFlowSearch
	}
	return task.CheckFlowDiscovery
}

// checkItemPath 把阶段化链路映射回条目的链路标记，保证诊断分组仍带“搜索/发现”前缀。
func checkItemPath(flow task.CheckStageFlow) task.CheckItemPath {
	if flow == task.CheckFlowDiscovery {
		return task.CheckPathDiscovery
	}
	return task.CheckPathSearch
}

func passedStage(flow task.CheckStageFlow, stage task.CheckStageName, start time.Time) task.CheckStageResult {
	return task.CheckStageResult{Flow: flow, Stage: stage, Status: task.CheckItemPassed, ElapsedMS: time.Since(start).Milliseconds()}
}

func failedStage(flow task.CheckStageFlow, stage task.CheckStageName, start time.Time, err error, fallbackCode string) task.CheckStageResult {
	details := diagnosticapp.ClassifyFailure(err, fallbackCode)
	return task.CheckStageResult{
		Flow:         flow,
		Stage:        stage,
		Status:       task.CheckItemFailed,
		FailureCode:  details.Code,
		FailureClass: details.Class,
		Message:      details.Message,
		ElapsedMS:    time.Since(start).Milliseconds(),
	}
}

// ruleEmptyStage 记录“规则为空”这类确定性失败：书源根本没有配置规则，而不是规则执行失败，
// 因此不带 failureClass，也不走 ClassifyFailure 的兜底分类。
func ruleEmptyStage(flow task.CheckStageFlow, stage task.CheckStageName, code string, message string) task.CheckStageResult {
	return task.CheckStageResult{Flow: flow, Stage: stage, Status: task.CheckItemFailed, FailureCode: code, Message: message}
}

func skippedStage(flow task.CheckStageFlow, stage task.CheckStageName, message string) task.CheckStageResult {
	return task.CheckStageResult{Flow: flow, Stage: stage, Status: task.CheckItemSkipped, Message: message}
}

func appendSkippedBookStages(item *task.CheckTaskItem, flow task.CheckStageFlow, opts Options, sourceType int, reason string) {
	if opts.CheckInfo {
		item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageInfo, reason))
	}
	appendSkippedAfterInfo(item, flow, opts, sourceType, reason)
}

func appendSkippedAfterInfo(item *task.CheckTaskItem, flow task.CheckStageFlow, opts Options, sourceType int, reason string) {
	if opts.CheckToc {
		if sourceType == fileSourceType {
			reason = "not applicable to file source"
		}
		item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageToc, reason))
	}
	if opts.CheckContent {
		if sourceType == fileSourceType {
			reason = "not applicable to file source"
		}
		item.Stages = append(item.Stages, skippedStage(flow, task.CheckStageContent, reason))
	}
}

// applyStageFailures 把第一个失败阶段的终态语义提升到条目：条目失败码/分类/消息与阶段记录一致。
func applyStageFailures(item *task.CheckTaskItem) {
	for _, stage := range item.Stages {
		if stage.Status != task.CheckItemFailed {
			continue
		}
		item.Status = task.CheckItemFailed
		item.FailureCode = stage.FailureCode
		item.FailureClass = stage.FailureClass
		item.Message = stage.Message
		return
	}
}

func firstContentIndex(chapters []*book.BookChapter) int {
	for _, chapter := range chapters {
		if chapter != nil && !chapter.IsVolume {
			return chapter.Index
		}
	}
	return 0
}
