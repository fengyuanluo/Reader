package debugapp

import (
	"context"
	"errors"
	"testing"

	contentapp "reader/internal/app/content"
	diagnosticapp "reader/internal/app/diagnostic"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

func TestKeywordFlowPublishesSearchInfoTocContentStages(t *testing.T) {
	repo := &fakeDebugRepo{}
	publisher := NewPublisher(repo)
	flow := NewKeywordFlow(
		&fakeDebugSearch{result: searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}},
		&fakeDebugSources{},
		&fakeDebugBooks{chapters: []*book.BookChapter{{BookURL: "https://book.test/1", URL: "https://book.test/c/1", Title: "第一章", Index: 0}}},
		&fakeDebugContent{},
		publisher,
	)
	session := &task.DebugSession{SessionID: "debug-keyword", SourceID: "source-id", TraceID: "trace-debug"}

	if err := flow.RunKeyword(context.Background(), session, "斗破"); err != nil {
		t.Fatalf("RunKeyword() error = %v", err)
	}
	want := []task.DebugEventType{
		task.DebugEventStarted,
		task.DebugEventStageStarted,
		task.DebugEventStageCompleted,
		task.DebugEventStageStarted,
		task.DebugEventStageCompleted,
		task.DebugEventStageStarted,
		task.DebugEventStageCompleted,
		task.DebugEventStageStarted,
		task.DebugEventStageCompleted,
		task.DebugEventCompleted,
	}
	if len(repo.events) != len(want) {
		t.Fatalf("events len = %d events=%#v", len(repo.events), repo.events)
	}
	for i, eventType := range want {
		if repo.events[i].Type != eventType {
			t.Fatalf("event[%d] = %s, want %s", i, repo.events[i].Type, eventType)
		}
	}
	if repo.events[1].Stage != "search" || repo.events[3].Stage != "info" || repo.events[5].Stage != "toc" || repo.events[7].Stage != "content" {
		t.Fatalf("stages = %#v", repo.events)
	}
}

func TestKeywordFlowPublishesFailedSearch(t *testing.T) {
	repo := &fakeDebugRepo{}
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugSources{}, &fakeDebugBooks{}, &fakeDebugContent{}, NewPublisher(repo))
	err := flow.RunKeyword(context.Background(), &task.DebugSession{SessionID: "debug-empty", SourceID: "source-id"}, "missing")
	if !errors.Is(err, ErrDebugNoSearchResult) {
		t.Fatalf("err = %v", err)
	}
	if len(repo.events) != 3 || repo.events[2].Type != task.DebugEventStageFailed || repo.events[2].Stage != "search" {
		t.Fatalf("events = %#v", repo.events)
	}
	if repo.events[2].Data["failureCode"] != diagnosticapp.FailureSearchFailed {
		t.Fatalf("failure code = %#v", repo.events[2].Data)
	}
	if repo.events[2].Data["failureClass"] != diagnosticapp.FailureClassSourceSearchEmptyResult {
		t.Fatalf("failure class = %#v", repo.events[2].Data)
	}
}

func TestURLFlowStartsAtContentStage(t *testing.T) {
	repo := &fakeDebugRepo{}
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugSources{}, &fakeDebugBooks{}, &fakeDebugContent{}, NewPublisher(repo))
	err := flow.RunURL(context.Background(), &task.DebugSession{SessionID: "debug-content", SourceID: "source-id", Mode: task.DebugModeContentURL, URL: "https://book.test/c/1"})
	if err != nil {
		t.Fatalf("RunURL() error = %v", err)
	}
	for _, event := range repo.events {
		if event.Stage == "search" || event.Stage == "info" || event.Stage == "toc" {
			t.Fatalf("unexpected skipped stage event: %#v", event)
		}
	}
	if len(repo.events) != 4 || repo.events[1].Stage != "content" || repo.events[2].Type != task.DebugEventStageCompleted {
		t.Fatalf("events = %#v", repo.events)
	}
}

func TestURLFlowRunsDiscoveryThenBookStages(t *testing.T) {
	repo := &fakeDebugRepo{}
	flow := NewKeywordFlow(
		&fakeDebugSearch{},
		&fakeDebugSources{},
		&fakeDebugBooks{chapters: []*book.BookChapter{{BookURL: "https://book.test/2", URL: "https://book.test/c/1", Title: "第一章", Index: 0}}},
		&fakeDebugContent{},
		NewPublisher(repo),
	).WithDiscovery(&fakeDebugDiscovery{result: discoveryResultWithBook("https://book.test/2")})

	err := flow.RunURL(context.Background(), &task.DebugSession{SessionID: "debug-discovery", SourceID: "source-id", Mode: task.DebugModeDiscoveryURL, URL: "https://source.test/discovery"})
	if err != nil {
		t.Fatalf("RunURL() error = %v", err)
	}
	if len(repo.events) < 10 || repo.events[1].Stage != "discovery" || repo.events[3].Stage != "info" {
		t.Fatalf("events = %#v", repo.events)
	}
}

func TestURLFlowContentModeUsesInMemoryChapter(t *testing.T) {
	// 未抓取过目录的新书：content_url 模式不能依赖已落库章节，否则必然失败。
	repo := &fakeDebugRepo{}
	content := &fakeDebugContent{}
	books := &fakeDebugBooks{}
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugSources{}, books, content, NewPublisher(repo))
	err := flow.RunURL(context.Background(), &task.DebugSession{SessionID: "debug-content-memory", SourceID: "source-id", Mode: task.DebugModeContentURL, URL: "https://book.test/new-book/c/1"})
	if err != nil {
		t.Fatalf("RunURL() error = %v", err)
	}
	if len(content.fromChapter) != 1 {
		t.Fatalf("GetContentFromChapter calls = %d, want 1", len(content.fromChapter))
	}
	chapter := content.fromChapter[0]
	if chapter.URL != "https://book.test/new-book/c/1" || chapter.Title != "调试" {
		t.Fatalf("chapter = %#v", chapter)
	}
	if len(books.fetchedInfo) != 0 || len(books.fetchedToc) != 0 {
		t.Fatalf("content_url mode fetched info/toc: info=%d toc=%d, want 0", len(books.fetchedInfo), len(books.fetchedToc))
	}
}

func TestURLFlowContentStageSkipsVolumeChapters(t *testing.T) {
	repo := &fakeDebugRepo{}
	content := &fakeDebugContent{}
	books := &fakeDebugBooks{chapters: []*book.BookChapter{
		{BookURL: "https://book.test/1", URL: "第一卷", Title: "第一卷", Index: 0, IsVolume: true},
		{BookURL: "https://book.test/1", URL: "https://book.test/c/1", Title: "第一章", Index: 1},
		{BookURL: "https://book.test/1", URL: "https://book.test/c/2", Title: "第二章", Index: 2},
	}}
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugSources{}, books, content, NewPublisher(repo))
	err := flow.RunURL(context.Background(), &task.DebugSession{SessionID: "debug-toc", SourceID: "source-id", Mode: task.DebugModeTocURL, URL: "https://book.test/1"})
	if err != nil {
		t.Fatalf("RunURL() error = %v", err)
	}
	if len(content.transient) != 1 {
		t.Fatalf("GetTransientContent calls = %d, want 1", len(content.transient))
	}
	call := content.transient[0]
	if call.chapter.Index != 1 || call.chapter.URL != "https://book.test/c/1" {
		t.Fatalf("chapter = %#v, want first non-volume chapter", call.chapter)
	}
	if call.nextChapterURL != "https://book.test/c/2" {
		t.Fatalf("nextChapterURL = %q, want https://book.test/c/2", call.nextChapterURL)
	}
	if len(books.fetchedInfo) != 0 {
		t.Fatalf("toc_url mode fetched info %d times, want 0", len(books.fetchedInfo))
	}
}

func TestKeywordFlowContentUsesFirstNonVolumeChapter(t *testing.T) {
	// 关键字调试成功时，content 阶段必须收到第一个非卷章节及其下一章 URL。
	repo := &fakeDebugRepo{}
	content := &fakeDebugContent{}
	books := &fakeDebugBooks{chapters: []*book.BookChapter{
		{BookURL: "https://book.test/1", URL: "第一卷", Title: "第一卷", Index: 0, IsVolume: true},
		{BookURL: "https://book.test/1", URL: "https://book.test/c/1", Title: "第一章", Index: 1},
		{BookURL: "https://book.test/1", URL: "https://book.test/c/2", Title: "第二章", Index: 2},
	}}
	flow := NewKeywordFlow(
		&fakeDebugSearch{result: searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}},
		&fakeDebugSources{},
		books,
		content,
		NewPublisher(repo),
	)
	if err := flow.RunKeyword(context.Background(), &task.DebugSession{SessionID: "debug-keyword-content", SourceID: "source-id"}, "斗破"); err != nil {
		t.Fatalf("RunKeyword() error = %v", err)
	}
	if len(content.transient) != 1 {
		t.Fatalf("GetTransientContent calls = %d, want 1", len(content.transient))
	}
	call := content.transient[0]
	if call.chapter.Index != 1 || call.chapter.URL != "https://book.test/c/1" || call.chapter.IsVolume {
		t.Fatalf("chapter = %#v, want first non-volume chapter", call.chapter)
	}
	if call.nextChapterURL != "https://book.test/c/2" {
		t.Fatalf("nextChapterURL = %q, want https://book.test/c/2", call.nextChapterURL)
	}
}

type fakeDebugSearch struct {
	result searchapp.MultiSearchResult
	err    error
}

func (s *fakeDebugSearch) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	if s.err != nil {
		return searchapp.MultiSearchResult{}, s.err
	}
	return s.result, nil
}

type fakeDebugBooks struct {
	chapters []*book.BookChapter
	err      error
	// fetchedInfo/fetchedToc 记录 FetchInfo/FetchToc 收到的书，供各模式断言。
	fetchedInfo []book.Book
	fetchedToc  []book.Book
}

func (b *fakeDebugBooks) FetchInfo(ctx context.Context, src *source.BookSource, item book.Book) (book.Book, error) {
	b.fetchedInfo = append(b.fetchedInfo, item)
	if b.err != nil {
		return book.Book{}, b.err
	}
	return item, nil
}

func (b *fakeDebugBooks) FetchToc(ctx context.Context, src *source.BookSource, item book.Book) ([]*book.BookChapter, error) {
	b.fetchedToc = append(b.fetchedToc, item)
	if b.err != nil {
		return nil, b.err
	}
	return b.chapters, nil
}

type fakeDebugContent struct {
	err error
	// fromChapter 记录 GetContentFromChapter 收到的内存章节，供 content_url 模式断言。
	fromChapter []book.BookChapter
	// transient 记录 GetTransientContent 收到的章节与 nextChapterURL，供调试链路断言。
	transient []transientContentCall
}

// transientContentCall 记录一次 GetTransientContent 调用的入参。
type transientContentCall struct {
	chapter        book.BookChapter
	nextChapterURL string
}

func (c *fakeDebugContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	c.transient = append(c.transient, transientContentCall{chapter: chapter, nextChapterURL: nextChapterURL})
	return contentapp.Result{Content: "正文", Chapter: chapter}, c.err
}

func (c *fakeDebugContent) GetContentFromChapter(ctx context.Context, sourceID string, b book.Book, chapter book.BookChapter) (contentapp.Result, error) {
	c.fromChapter = append(c.fromChapter, chapter)
	return contentapp.Result{Content: "正文", Chapter: chapter}, c.err
}

type fakeDebugSources struct {
	src *source.BookSource
	err error
}

func (s *fakeDebugSources) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.src != nil {
		return s.src, nil
	}
	return &source.BookSource{SourceID: id, BookSourceURL: "https://source.test", BookSourceName: "Source"}, nil
}

type fakeDebugDiscovery struct {
	result discoveryapp.Result
	err    error
}

func (d *fakeDebugDiscovery) Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error) {
	if d.err != nil {
		return discoveryapp.Result{}, d.err
	}
	return d.result, nil
}

func discoveryResultWithBook(bookURL string) discoveryapp.Result {
	return discoveryapp.Result{Items: []book.SearchBook{{BookURL: bookURL, Origin: "https://source.test", OriginName: "Source", Name: "发现书", Author: "作者"}}}
}
