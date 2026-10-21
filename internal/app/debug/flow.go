package debugapp

import (
	"context"
	"errors"
	"time"

	bookapp "reader/internal/app/book"
	contentapp "reader/internal/app/content"
	diagnosticapp "reader/internal/app/diagnostic"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

var ErrDebugNoSearchResult = errors.New("empty search result")

type SearchService interface {
	Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error)
}

// BookService 只抓取并解析详情/目录，不写书架与章节表（调试专用）。
type BookService interface {
	FetchInfo(ctx context.Context, src *source.BookSource, b book.Book) (book.Book, error)
	FetchToc(ctx context.Context, src *source.BookSource, b book.Book) ([]*book.BookChapter, error)
}

type ContentService interface {
	// GetTransientContent 用内存中的书/章节对象抓正文，不读也不写正文缓存与章节表。
	GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error)
	// GetContentFromChapter 直接用内存中的书/章节对象抓正文，不查章节表：
	// 对应 Legado Debug.kt 的 contentDebug（直接构造 BookChapter 且 needSave=false）。
	GetContentFromChapter(ctx context.Context, sourceID string, b book.Book, chapter book.BookChapter) (contentapp.Result, error)
}

type DiscoveryService interface {
	Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error)
}

// SourceFinder 按书源 ID 取内存中的书源，供调试链路免落库抓取使用。
type SourceFinder interface {
	FindByID(ctx context.Context, id string) (*source.BookSource, error)
}

type KeywordFlow struct {
	search    SearchService
	discovery DiscoveryService
	sources   SourceFinder
	books     BookService
	content   ContentService
	events    *Publisher
	now       func() time.Time
}

func NewKeywordFlow(search SearchService, sources SourceFinder, books BookService, content ContentService, events *Publisher) *KeywordFlow {
	return &KeywordFlow{search: search, sources: sources, books: books, content: content, events: events, now: time.Now}
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

	// 关键字调试与 runFromBook(info/toc/content 全开) 等价：只抓取解析，不写书架。
	return f.runFromBook(ctx, session, searchResult.Items[0].ToBook(), start, true, true, true, "info")
}

func (f *KeywordFlow) RunURL(ctx context.Context, session *task.DebugSession) error {
	start := f.now()
	if err := f.publish(ctx, session, task.DebugEventStarted, "start", task.DebugLevelInfo, "debug started", start); err != nil {
		return err
	}
	switch session.Mode {
	case task.DebugModeInfoURL:
		b := book.Book{BookURL: session.URL, Origin: session.SourceID, Name: "debug", Author: "debug"}
		return f.runFromBook(ctx, session, b, start, true, true, true, "info")
	case task.DebugModeTocURL:
		b := book.Book{BookURL: session.URL, TocURL: session.URL, Origin: session.SourceID, Name: "debug", Author: "debug"}
		return f.runFromBook(ctx, session, b, start, false, true, true, "toc")
	case task.DebugModeContentURL:
		// Legado Debug.startDebug 的 "--" 分支：直接构造 BookChapter 抓正文，不落库、
		// 不查章节表，因此对还没抓取过目录的新书同样可用。
		b := book.Book{BookURL: session.URL, Origin: session.SourceID, Name: "debug", Author: "debug"}
		chapter := book.BookChapter{BookURL: session.URL, Title: "调试", URL: session.URL}
		if err := f.stage(ctx, session, "content", start, func() error {
			_, err := f.content.GetContentFromChapter(ctx, session.SourceID, b, chapter)
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
		return f.runFromBook(ctx, session, result.Items[0].ToBook(), start, true, true, true, "info")
	default:
		return f.RunKeyword(ctx, session, session.Keyword)
	}
}

func (f *KeywordFlow) runFromBook(ctx context.Context, session *task.DebugSession, b book.Book, start time.Time, doInfo bool, doToc bool, doContent bool, firstStage string) error {
	// 调试全程不写书架：按 ID 取书源后只抓取解析，不落库（对应 Legado needSave=false）。
	src, err := f.sources.FindByID(ctx, session.SourceID)
	if err != nil {
		// FindByID 失败发生在首个阶段之前，按首阶段归属发布，避免 toc/content
		// 模式下失败事件被错误挂在 info 阶段。
		_ = f.publishFailure(ctx, session, firstStage, err, fallbackFailureCodeForStage(firstStage), start)
		return err
	}
	current := b
	if doInfo {
		if err := f.stage(ctx, session, "info", start, func() error {
			var err error
			current, err = f.books.FetchInfo(ctx, src, b)
			return err
		}); err != nil {
			return err
		}
	}
	var chapters []*book.BookChapter
	if doToc {
		if err := f.stage(ctx, session, "toc", start, func() error {
			var err error
			chapters, err = f.books.FetchToc(ctx, src, current)
			return err
		}); err != nil {
			return err
		}
	}
	if doContent {
		// Legado Debug.tocDebug 会先过滤掉卷章节，正文阶段只抓第一个正文章节。
		chapter, nextChapterURL, ok := bookapp.FirstContentChapter(chapters)
		if !ok {
			return f.stage(ctx, session, "content", start, func() error {
				return errors.New("empty toc")
			})
		}
		if err := f.stage(ctx, session, "content", start, func() error {
			_, err := f.content.GetTransientContent(ctx, src, current, chapter, nextChapterURL)
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
