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
	"reader/internal/domain/task"
)

func TestKeywordFlowPublishesSearchInfoTocContentStages(t *testing.T) {
	repo := &fakeDebugRepo{}
	publisher := NewPublisher(repo)
	flow := NewKeywordFlow(
		&fakeDebugSearch{result: searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}},
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
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugBooks{}, &fakeDebugContent{}, NewPublisher(repo))
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
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugBooks{}, &fakeDebugContent{}, NewPublisher(repo))
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
}

func (b *fakeDebugBooks) Save(ctx context.Context, item *book.Book) (*book.Book, error) {
	if b.err != nil {
		return nil, b.err
	}
	return item, nil
}

func (b *fakeDebugBooks) RefreshInfo(ctx context.Context, id string) (book.Book, error) {
	return book.Book{BookURL: "https://book.test/1", Name: "斗破苍穹", Author: "天蚕土豆"}, b.err
}

func (b *fakeDebugBooks) RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error) {
	return b.chapters, &book.Book{BookURL: "https://book.test/1", Name: "斗破苍穹", Author: "天蚕土豆"}, b.err
}

type fakeDebugContent struct {
	err error
}

func (c *fakeDebugContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	return contentapp.Result{Content: "正文", Chapter: book.BookChapter{Index: index}}, &book.Book{}, c.err
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
