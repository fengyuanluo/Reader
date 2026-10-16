package debugapp

import (
	"context"
	"sync"
	"testing"
	"time"

	contentapp "reader/internal/app/content"
	"reader/internal/domain/book"
	"reader/internal/domain/task"
)

func TestRunnerMarksSessionSucceededWhenFlowCompletes(t *testing.T) {
	repo, runner, session := newRunnerSession(t, "debug-done", &fakeDebugContent{}, task.DebugSessionRunning)

	runner.Start(context.Background(), session)
	waitForSessionStatus(t, repo, session.SessionID, task.DebugSessionSucceeded)
	got, err := repo.FindSession(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() error = %v", err)
	}
	if got.CompletedAt == nil {
		t.Fatalf("completedAt is nil for %#v", got)
	}
}

func TestRunnerMarksSessionFailedWhenFlowFails(t *testing.T) {
	repo, runner, session := newRunnerSession(t, "debug-failed", &fakeDebugContent{err: contentapp.ErrContentEmpty}, task.DebugSessionRunning)

	runner.Start(context.Background(), session)
	waitForSessionStatus(t, repo, session.SessionID, task.DebugSessionFailed)
	got, err := repo.FindSession(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() error = %v", err)
	}
	if got.CompletedAt == nil {
		t.Fatalf("completedAt is nil for %#v", got)
	}
}

func TestRunnerCancelStopsRunningSession(t *testing.T) {
	content := newSequencedDebugContent(1)
	repo, runner, session := newRunnerSession(t, "debug-cancel", content, task.DebugSessionRunning)

	runner.Start(context.Background(), session)
	select {
	case <-content.started(0):
	case <-time.After(time.Second):
		t.Fatalf("content stage did not start")
	}
	if !runner.Cancel(session.SessionID) {
		t.Fatalf("Cancel() returned false")
	}
	select {
	case <-content.released(0):
	case <-time.After(time.Second):
		t.Fatalf("content stage did not observe cancellation")
	}
	// 被取消的运行不能把会话改成 failed，终态仍由 CancelSession 决定。
	time.Sleep(20 * time.Millisecond)
	got, err := repo.FindSession(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() error = %v", err)
	}
	if got.Status != task.DebugSessionRunning {
		t.Fatalf("status = %q, want running", got.Status)
	}
}

func TestRunnerKeepsNewerRunCancelHandleWhenSameSessionRestarts(t *testing.T) {
	content := newSequencedDebugContent(2)
	_, runner, session := newRunnerSession(t, "debug-rerun", content, task.DebugSessionRunning)

	runner.Start(context.Background(), session)
	select {
	case <-content.started(0):
	case <-time.After(time.Second):
		t.Fatalf("first run did not start")
	}
	// 同 ID 重跑：旧运行的清理不能删除新运行的 cancel 句柄。
	runner.Start(context.Background(), session)
	select {
	case <-content.started(1):
	case <-time.After(time.Second):
		t.Fatalf("second run did not start")
	}
	if !runner.Cancel(session.SessionID) {
		t.Fatalf("Cancel() returned false; the old run removed the new cancel handle")
	}
	select {
	case <-content.released(1):
	case <-time.After(time.Second):
		t.Fatalf("second run did not observe cancellation")
	}
}

func newRunnerSession(t *testing.T, sessionID string, content ContentService, status task.DebugSessionStatus) (*fakeDebugRepo, *Runner, *task.DebugSession) {
	t.Helper()
	repo := &fakeDebugRepo{}
	flow := NewKeywordFlow(&fakeDebugSearch{}, &fakeDebugBooks{}, content, NewPublisher(repo))
	runner := NewRunner(flow, repo)
	session := &task.DebugSession{SessionID: sessionID, SourceID: "source-id", Mode: task.DebugModeContentURL, Status: status, URL: "https://book.test/c/1", CreatedAt: time.Now().UTC()}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return repo, runner, session
}

func waitForSessionStatus(t *testing.T, repo *fakeDebugRepo, sessionID string, want task.DebugSessionStatus) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := repo.FindSession(context.Background(), sessionID)
		if err == nil && got.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := repo.FindSession(context.Background(), sessionID)
	t.Fatalf("session status = %q, want %q", got.Status, want)
}

// sequencedDebugContent 模拟多次运行：每次调用都被阻塞，直到对应运行的 ctx 被取消。
type sequencedDebugContent struct {
	mu         sync.Mutex
	calls      int
	startedCh  []chan struct{}
	releasedCh []chan struct{}
}

func newSequencedDebugContent(runs int) *sequencedDebugContent {
	content := &sequencedDebugContent{}
	for range runs {
		content.startedCh = append(content.startedCh, make(chan struct{}))
		content.releasedCh = append(content.releasedCh, make(chan struct{}))
	}
	return content
}

// started 返回第 index 次运行开始抓取正文的信号通道。
func (c *sequencedDebugContent) started(index int) chan struct{} { return c.startedCh[index] }

// released 返回第 index 次运行观察到上下文取消的信号通道。
func (c *sequencedDebugContent) released(index int) chan struct{} { return c.releasedCh[index] }

func (c *sequencedDebugContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	return contentapp.Result{}, nil, c.block(ctx)
}

func (c *sequencedDebugContent) GetContentFromChapter(ctx context.Context, sourceID string, b book.Book, chapter book.BookChapter) (contentapp.Result, error) {
	return contentapp.Result{}, c.block(ctx)
}

func (c *sequencedDebugContent) block(ctx context.Context) error {
	c.mu.Lock()
	index := c.calls
	if index >= len(c.startedCh) {
		index = len(c.startedCh) - 1
	}
	c.calls++
	c.mu.Unlock()
	close(c.startedCh[index])
	<-ctx.Done()
	close(c.releasedCh[index])
	return ctx.Err()
}
