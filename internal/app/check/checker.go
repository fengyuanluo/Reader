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
	DefaultTimeout         = 180 * time.Second
)

const fileSourceType = 3

type SearchService interface {
	Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error)
}

type DiscoveryService interface {
	Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error)
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
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	item := &task.CheckTaskItem{
		SourceID:       src.SourceID,
		BookSourceURL:  src.BookSourceURL,
		BookSourceName: src.BookSourceName,
		Status:         task.CheckItemPassed,
		TimeoutMS:      opts.Timeout.Milliseconds(),
	}
	var firstErr error
	attempted := false
	if opts.CheckSearch {
		item.KeywordUsed = opts.Keyword
		if strings.TrimSpace(src.SearchURL) == "" && src.BookSourceType == fileSourceType {
			item.Stages = append(item.Stages, skippedStage(task.CheckFlowSearch, task.CheckStageSearch, "not applicable: searchUrl is empty"))
		} else {
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
	if opts.CheckDiscovery && src.ExploreURL != "" {
		attempted = true
		stageStart := time.Now()
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
	if !attempted {
		item.Status = task.CheckItemSkipped
		item.Message = "no applicable check flow"
	}
	applyStageFailures(item)
	item.RespondTime = time.Since(start).Milliseconds()
	return item, firstErr
}

func (c *Checker) checkBook(ctx context.Context, b book.Book, sourceType int, flow task.CheckStageFlow, opts Options, item *task.CheckTaskItem) error {
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
	return "我的"
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
