package debugapp

import (
	"context"
	"testing"
	"time"

	contentapp "reader/internal/app/content"
	"reader/internal/domain/book"
	"reader/internal/domain/task"
)

func TestRunnerCancelStopsRunningSession(t *testing.T) {
	repo := &fakeDebugRepo{}
	content := &blockingDebugContent{started: make(chan struct{}), released: make(chan struct{})}
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugBooks{}, content, NewPublisher(repo))
	runner := NewRunner(flow)

	runner.Start(context.Background(), &task.DebugSession{SessionID: "debug-cancel", SourceID: "source-id", Mode: task.DebugModeContentURL, URL: "https://book.test/c/1"})
	select {
	case <-content.started:
	case <-time.After(time.Second):
		t.Fatalf("content stage did not start")
	}
	if !runner.Cancel("debug-cancel") {
		t.Fatalf("Cancel() returned false")
	}
	select {
	case <-content.released:
	case <-time.After(time.Second):
		t.Fatalf("content stage did not observe cancellation")
	}
}

type blockingDebugContent struct {
	started  chan struct{}
	released chan struct{}
}

func (c *blockingDebugContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	close(c.started)
	<-ctx.Done()
	close(c.released)
	return contentapp.Result{}, nil, ctx.Err()
}
