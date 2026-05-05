package checkapp

import (
	"context"
	"errors"
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
)

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
	if opts.Keyword == "" {
		opts.Keyword = "我的"
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	item := &task.CheckTaskItem{
		SourceID:       src.SourceID,
		BookSourceURL:  src.BookSourceURL,
		BookSourceName: src.BookSourceName,
		Status:         task.CheckItemPassed,
	}
	if opts.CheckSearch {
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
		if err := c.checkBook(ctx, result.Items[0].ToBook(), opts, start, item); err != nil {
			return item, err
		}
	}
	if opts.CheckDiscovery && src.ExploreURL != "" {
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
			if err := c.checkBook(ctx, result.Items[0].ToBook(), opts, start, item); err != nil {
				return item, err
			}
		}
	}
	item.RespondTime = time.Since(start).Milliseconds()
	return item, nil
}

func (c *Checker) checkBook(ctx context.Context, b book.Book, opts Options, start time.Time, item *task.CheckTaskItem) error {
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
