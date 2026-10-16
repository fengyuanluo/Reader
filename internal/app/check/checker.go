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
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	// Legado CheckSourceService 用 withTimeout 包住整本书源的校验过程，Reader 保持同一语义。
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	item := &task.CheckTaskItem{
		SourceID:       src.SourceID,
		BookSourceURL:  src.BookSourceURL,
		BookSourceName: src.BookSourceName,
		Status:         task.CheckItemPassed,
	}
	if opts.CheckSearch {
		if strings.TrimSpace(src.SearchURL) == "" {
			// Legado：没有搜索链接规则时不执行搜索，直接标记“搜索链接规则为空”。
			item.Message = "searchUrl rule is empty"
			failed, _ := c.fail(item, start, diagnosticapp.FailureDetails{Code: FailureSearchRuleEmpty, Message: item.Message})
			return failed, nil
		}
		result, err := c.search.Search(ctx, searchapp.MultiSearchInput{Keyword: opts.Keyword, Page: 1, SourceIDs: []string{src.SourceID}, EnabledOnly: false, Concurrency: 1})
		if err != nil {
			item.Message = err.Error()
			failed, _ := c.fail(item, start, diagnosticapp.ClassifyFailure(err, FailureSearchFailed))
			return failed, err
		}
		if len(result.Items) == 0 {
			item.Message = "empty search result"
			failed, _ := c.fail(item, start, diagnosticapp.ClassifyFailure(errors.New(item.Message), FailureSearchFailed))
			return failed, nil
		}
		if err := c.checkBook(ctx, result.Items[0].ToBook(), opts, start, item, task.CheckPathSearch); err != nil {
			return item, err
		}
	}
	if opts.CheckDiscovery && strings.TrimSpace(src.ExploreURL) != "" {
		categories, err := c.discovery.Categories(ctx, src.SourceID)
		if err != nil {
			item.Message = err.Error()
			failed, _ := c.fail(item, start, diagnosticapp.ClassifyFailure(err, FailureDiscoveryFailed))
			return failed, err
		}
		if len(categories) == 0 {
			// Legado：exploreUrl 非空却解析不出任何可用分类时标记“发现规则为空”。
			item.Message = "exploreUrl rule is empty"
			failed, _ := c.fail(item, start, diagnosticapp.FailureDetails{Code: FailureDiscoveryRuleEmpty, Message: item.Message})
			return failed, nil
		}
		result, err := c.discovery.Discover(ctx, discoveryapp.Input{SourceIDs: []string{src.SourceID}, Page: 1})
		if err != nil {
			item.Message = err.Error()
			failed, _ := c.fail(item, start, diagnosticapp.ClassifyFailure(err, FailureDiscoveryFailed))
			return failed, err
		}
		if len(result.Items) == 0 {
			item.Message = "empty discovery result"
			failed, _ := c.fail(item, start, diagnosticapp.ClassifyFailure(errors.New(item.Message), FailureDiscoveryFailed))
			return failed, nil
		}
		if !opts.CheckSearch {
			if err := c.checkBook(ctx, result.Items[0].ToBook(), opts, start, item, task.CheckPathDiscovery); err != nil {
				return item, err
			}
		}
	}
	item.RespondTime = time.Since(start).Milliseconds()
	return item, nil
}

// checkKeyword 按 Legado BookSource.getCheckKeyword 语义选取校验关键字：
// 书源自身的 RuleSearch.CheckKeyWord 优先，为空时才回退到调用方给出的默认关键字。
func checkKeyword(src *source.BookSource, fallback string) string {
	if src != nil && src.RuleSearch.Value != nil {
		if keyword := strings.TrimSpace(src.RuleSearch.Value.CheckKeyWord); keyword != "" {
			return keyword
		}
	}
	if keyword := strings.TrimSpace(fallback); keyword != "" {
		return keyword
	}
	return DefaultCheckKeyword
}

func (c *Checker) checkBook(ctx context.Context, b book.Book, opts Options, start time.Time, item *task.CheckTaskItem, path task.CheckItemPath) error {
	item.Path = path
	saved, err := c.books.Save(ctx, &b)
	if err != nil {
		item.Message = err.Error()
		_, err = c.fail(item, start, diagnosticapp.ClassifyFailure(err, ""))
		return err
	}
	bookID := book.IDFromURL(saved.BookURL)
	if opts.CheckInfo {
		if _, err := c.books.RefreshInfo(ctx, bookID); err != nil {
			item.Message = err.Error()
			_, err = c.fail(item, start, diagnosticapp.ClassifyFailure(err, "info_failed"))
			return err
		}
	}
	if opts.CheckToc {
		chapters, _, err := c.books.RefreshToc(ctx, bookID)
		if err != nil {
			item.Message = err.Error()
			_, err = c.fail(item, start, diagnosticapp.ClassifyFailure(err, FailureTocFailed))
			return err
		}
		if len(chapters) == 0 {
			item.Message = "empty toc"
			_, _ = c.fail(item, start, diagnosticapp.ClassifyFailure(errors.New(item.Message), FailureTocFailed))
			return nil
		}
		if opts.CheckContent {
			index := firstContentIndex(chapters)
			if _, _, err := c.content.GetChapterContent(ctx, bookID, index); err != nil {
				item.Message = err.Error()
				_, err = c.fail(item, start, diagnosticapp.ClassifyFailure(err, FailureContentFailed))
				return err
			}
		}
	}
	return nil
}

func (c *Checker) fail(item *task.CheckTaskItem, start time.Time, details diagnosticapp.FailureDetails) (*task.CheckTaskItem, error) {
	applyFailure(item, time.Since(start).Milliseconds(), details)
	return item, nil
}

func firstContentIndex(chapters []*book.BookChapter) int {
	for _, chapter := range chapters {
		if chapter != nil && !chapter.IsVolume {
			return chapter.Index
		}
	}
	return 0
}

func applyFailure(item *task.CheckTaskItem, responseMS int64, details diagnosticapp.FailureDetails) {
	item.Status = task.CheckItemFailed
	item.FailureCode = details.Code
	item.FailureClass = details.Class
	if item.Message == "" {
		item.Message = details.Message
	}
	if item.Message == "" {
		item.Message = details.Code
	}
	item.RespondTime = responseMS
}
