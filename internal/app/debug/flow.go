package debugapp

import (
	"context"
	"errors"
	"time"

	contentapp "reader/internal/app/content"
	diagnosticapp "reader/internal/app/diagnostic"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
	"reader/internal/domain/task"
)

var ErrDebugNoSearchResult = errors.New("empty search result")

type SearchService interface {
	Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error)
}

type BookService interface {
	Save(ctx context.Context, b *book.Book) (*book.Book, error)
	RefreshInfo(ctx context.Context, id string) (book.Book, error)
	RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error)
}

type ContentService interface {
	GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error)
}

type DiscoveryService interface {
	Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error)
}

type KeywordFlow struct {
	search    SearchService
	discovery DiscoveryService
	books     BookService
	content   ContentService
	events    *Publisher
	now       func() time.Time
}

func NewKeywordFlow(search SearchService, books BookService, content ContentService, events *Publisher) *KeywordFlow {
	return &KeywordFlow{search: search, books: books, content: content, events: events, now: time.Now}
}

func (f *KeywordFlow) WithDiscovery(discovery DiscoveryService) *KeywordFlow {
	f.discovery = discovery
	return f
}

func (f *KeywordFlow) RunKeyword(ctx context.Context, session *task.DebugSession, keyword string) error {
	start := f.now()
	if err := f.publish(ctx, session, task.DebugEventStarted, "start", task.DebugLevelInfo, "debug started", start); err != nil {
		return err
	}
	if err := f.publish(ctx, session, task.DebugEventStageStarted, "search", task.DebugLevelInfo, "search started", start); err != nil {
		return err
	}
	searchResult, err := f.search.Search(ctx, searchapp.MultiSearchInput{Keyword: keyword, Page: 1, SourceIDs: []string{session.SourceID}, EnabledOnly: false, Concurrency: 1})
	if err != nil {
		_ = f.publishFailure(ctx, session, "search", err, diagnosticapp.FailureWebsiteFailed, start)
		return err
	}
	if len(searchResult.Items) == 0 {
		_ = f.publishFailure(ctx, session, "search", ErrDebugNoSearchResult, diagnosticapp.FailureSearchFailed, start)
		return ErrDebugNoSearchResult
	}
	if err := f.publish(ctx, session, task.DebugEventStageCompleted, "search", task.DebugLevelInfo, "search completed", start); err != nil {
		return err
	}

	b := searchResult.Items[0].ToBook()
	saved, err := f.books.Save(ctx, &b)
	if err != nil {
		_ = f.publishFailure(ctx, session, "info", err, diagnosticapp.FailureWebsiteFailed, start)
		return err
	}
	bookID := book.IDFromURL(saved.BookURL)
	if err := f.stage(ctx, session, "info", start, func() error {
		_, err := f.books.RefreshInfo(ctx, bookID)
		return err
	}); err != nil {
		return err
	}

	var chapters []*book.BookChapter
	if err := f.stage(ctx, session, "toc", start, func() error {
		var err error
		chapters, _, err = f.books.RefreshToc(ctx, bookID)
		return err
	}); err != nil {
		return err
	}
	contentIndex := firstContentChapterIndex(chapters)
	if err := f.stage(ctx, session, "content", start, func() error {
		_, _, err := f.content.GetChapterContent(ctx, bookID, contentIndex)
		return err
	}); err != nil {
		return err
	}
	return f.publish(ctx, session, task.DebugEventCompleted, "complete", task.DebugLevelInfo, "debug completed", start)
}

func (f *KeywordFlow) RunURL(ctx context.Context, session *task.DebugSession) error {
	start := f.now()
	if err := f.publish(ctx, session, task.DebugEventStarted, "start", task.DebugLevelInfo, "debug started", start); err != nil {
		return err
	}
	switch session.Mode {
	case task.DebugModeInfoURL:
		b := book.Book{BookURL: session.URL, Origin: session.SourceID, Name: "debug", Author: "debug"}
		return f.runFromBook(ctx, session, b, start, true, true, true)
	case task.DebugModeTocURL:
		b := book.Book{BookURL: session.URL, TocURL: session.URL, Origin: session.SourceID, Name: "debug", Author: "debug"}
		return f.runFromBook(ctx, session, b, start, false, true, true)
	case task.DebugModeContentURL:
		b := book.Book{BookURL: session.URL, Origin: session.SourceID, Name: "debug", Author: "debug"}
		saved, err := f.books.Save(ctx, &b)
		if err != nil {
			_ = f.publishFailure(ctx, session, "content", err, diagnosticapp.FailureWebsiteFailed, start)
			return err
		}
		if err := f.stage(ctx, session, "content", start, func() error {
			_, _, err := f.content.GetChapterContent(ctx, book.IDFromURL(saved.BookURL), 0)
			return err
		}); err != nil {
			return err
		}
		return f.publish(ctx, session, task.DebugEventCompleted, "complete", task.DebugLevelInfo, "debug completed", start)
	case task.DebugModeDiscoveryURL:
		if f.discovery == nil {
			return errors.New("discovery service is not configured")
		}
		if err := f.publish(ctx, session, task.DebugEventStageStarted, "discovery", task.DebugLevelInfo, "discovery started", start); err != nil {
			return err
		}
		result, err := f.discovery.Discover(ctx, discoveryapp.Input{SourceIDs: []string{session.SourceID}, DiscoveryURL: session.URL, Page: 1})
		if err != nil {
			_ = f.publishFailure(ctx, session, "discovery", err, diagnosticapp.FailureWebsiteFailed, start)
			return err
		}
		if len(result.Items) == 0 {
			err := errors.New("empty discovery result")
			_ = f.publishFailure(ctx, session, "discovery", err, diagnosticapp.FailureDiscoveryFailed, start)
			return err
		}
		if err := f.publish(ctx, session, task.DebugEventStageCompleted, "discovery", task.DebugLevelInfo, "discovery completed", start); err != nil {
			return err
		}
		return f.runFromBook(ctx, session, result.Items[0].ToBook(), start, true, true, true)
	default:
		return f.RunKeyword(ctx, session, session.Keyword)
	}
}

func (f *KeywordFlow) runFromBook(ctx context.Context, session *task.DebugSession, b book.Book, start time.Time, doInfo bool, doToc bool, doContent bool) error {
	saved, err := f.books.Save(ctx, &b)
	if err != nil {
		_ = f.publishFailure(ctx, session, "info", err, diagnosticapp.FailureWebsiteFailed, start)
		return err
	}
	bookID := book.IDFromURL(saved.BookURL)
	if doInfo {
		if err := f.stage(ctx, session, "info", start, func() error {
			_, err := f.books.RefreshInfo(ctx, bookID)
			return err
		}); err != nil {
			return err
		}
	}
	if doToc {
		if err := f.stage(ctx, session, "toc", start, func() error {
			_, _, err := f.books.RefreshToc(ctx, bookID)
			return err
		}); err != nil {
			return err
		}
	}
	if doContent {
		if err := f.stage(ctx, session, "content", start, func() error {
			_, _, err := f.content.GetChapterContent(ctx, bookID, 0)
			return err
		}); err != nil {
			return err
		}
	}
	return f.publish(ctx, session, task.DebugEventCompleted, "complete", task.DebugLevelInfo, "debug completed", start)
}

func (f *KeywordFlow) stage(ctx context.Context, session *task.DebugSession, stage string, start time.Time, run func() error) error {
	if err := f.publish(ctx, session, task.DebugEventStageStarted, stage, task.DebugLevelInfo, stage+" started", start); err != nil {
		return err
	}
	if err := run(); err != nil {
		_ = f.publishFailure(ctx, session, stage, err, fallbackFailureCodeForStage(stage), start)
		return err
	}
	return f.publish(ctx, session, task.DebugEventStageCompleted, stage, task.DebugLevelInfo, stage+" completed", start)
}

func (f *KeywordFlow) publishFailure(ctx context.Context, session *task.DebugSession, stage string, err error, fallbackCode string, start time.Time) error {
	details := diagnosticapp.ClassifyFailure(err, fallbackCode)
	event := &task.DebugEvent{
		SessionID: session.SessionID,
		SourceID:  session.SourceID,
		Type:      task.DebugEventStageFailed,
		Stage:     stage,
		Level:     task.DebugLevelError,
		Message:   details.Message,
		ElapsedMS: f.now().Sub(start).Milliseconds(),
		TraceID:   session.TraceID,
		CreatedAt: f.now().UTC(),
	}
	if details.Code != "" || details.Class != "" {
		event.Data = map[string]any{}
		if details.Code != "" {
			event.Data["failureCode"] = details.Code
		}
		if details.Class != "" {
			event.Data["failureClass"] = details.Class
		}
	}
	return f.events.Publish(ctx, event)
}

func (f *KeywordFlow) publish(ctx context.Context, session *task.DebugSession, eventType task.DebugEventType, stage string, level task.DebugEventLevel, message string, start time.Time) error {
	return f.events.Publish(ctx, &task.DebugEvent{
		SessionID: session.SessionID,
		SourceID:  session.SourceID,
		Type:      eventType,
		Stage:     stage,
		Level:     level,
		Message:   message,
		ElapsedMS: f.now().Sub(start).Milliseconds(),
		TraceID:   session.TraceID,
		CreatedAt: f.now().UTC(),
	})
}

func fallbackFailureCodeForStage(stage string) string {
	switch stage {
	case "search":
		return diagnosticapp.FailureSearchFailed
	case "discovery":
		return diagnosticapp.FailureDiscoveryFailed
	case "toc":
		return diagnosticapp.FailureTocFailed
	case "content":
		return diagnosticapp.FailureContentFailed
	default:
		return diagnosticapp.FailureWebsiteFailed
	}
}

func firstContentChapterIndex(chapters []*book.BookChapter) int {
	for _, chapter := range chapters {
		if chapter != nil && !chapter.IsVolume {
			return chapter.Index
		}
	}
	return 0
}
