package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/task"
)

func TestDebugRepositoryCreatesUpdatesAndFindsSession(t *testing.T) {
	db := openRepoTestDB(t)
	repo := NewDebugRepository(db)
	ctx := context.Background()
	createdAt := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	session := &task.DebugSession{
		SessionID: "debug-1",
		SourceID:  "source-1",
		Mode:      task.DebugModeKeyword,
		Status:    task.DebugSessionPending,
		Keyword:   "斗破",
		TraceID:   "trace-1",
		CreatedAt: createdAt,
	}

	if err := repo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	got, err := repo.FindSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() error = %v", err)
	}
	if got.SourceID != "source-1" || got.Mode != task.DebugModeKeyword || got.Status != task.DebugSessionPending || got.Keyword != "斗破" || !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("session = %#v", got)
	}

	completedAt := createdAt.Add(time.Second)
	applied, err := repo.UpdateSessionStatus(ctx, session.SessionID, task.DebugSessionSucceeded, &completedAt)
	if err != nil {
		t.Fatalf("UpdateSessionStatus() error = %v", err)
	}
	if !applied {
		t.Fatalf("UpdateSessionStatus() applied = false")
	}
	got, err = repo.FindSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() after update error = %v", err)
	}
	if got.Status != task.DebugSessionSucceeded || got.CompletedAt == nil || !got.CompletedAt.Equal(completedAt) {
		t.Fatalf("updated session = %#v", got)
	}
}

func TestDebugRepositoryRejectsStatusUpdateOnTerminalSession(t *testing.T) {
	repo := NewDebugRepository(openRepoTestDB(t))
	ctx := context.Background()
	createdAt := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	session := &task.DebugSession{SessionID: "debug-terminal", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionRunning, CreatedAt: createdAt}
	if err := repo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	cancelledAt := createdAt.Add(time.Second)
	applied, err := repo.UpdateSessionStatus(ctx, session.SessionID, task.DebugSessionCancelled, &cancelledAt)
	if err != nil || !applied {
		t.Fatalf("cancel update applied=%v err=%v", applied, err)
	}
	// 已进入终态的会话不能再被并发结束路径改写状态。
	failedAt := createdAt.Add(2 * time.Second)
	applied, err = repo.UpdateSessionStatus(ctx, session.SessionID, task.DebugSessionFailed, &failedAt)
	if err != nil {
		t.Fatalf("UpdateSessionStatus(failed) error = %v", err)
	}
	if applied {
		t.Fatalf("terminal session accepted a second status update")
	}
	got, err := repo.FindSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() error = %v", err)
	}
	if got.Status != task.DebugSessionCancelled || got.CompletedAt == nil || !got.CompletedAt.Equal(cancelledAt) {
		t.Fatalf("session = %#v", got)
	}
}

func TestDebugRepositoryUpdateSessionStatusMissingSession(t *testing.T) {
	repo := NewDebugRepository(openRepoTestDB(t))
	completedAt := time.Now().UTC()
	if _, err := repo.UpdateSessionStatus(context.Background(), "missing", task.DebugSessionSucceeded, &completedAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDebugRepositoryFailsInterruptedSessions(t *testing.T) {
	repo := NewDebugRepository(openRepoTestDB(t))
	ctx := context.Background()
	createdAt := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	for _, session := range []*task.DebugSession{
		{SessionID: "debug-running", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionRunning, CreatedAt: createdAt},
		{SessionID: "debug-pending", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionPending, CreatedAt: createdAt},
		{SessionID: "debug-done", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionSucceeded, CreatedAt: createdAt},
	} {
		if err := repo.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%s) error = %v", session.SessionID, err)
		}
	}
	completedAt := createdAt.Add(time.Minute)
	count, err := repo.FailInterruptedSessions(ctx, completedAt)
	if err != nil {
		t.Fatalf("FailInterruptedSessions() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	for _, sessionID := range []string{"debug-running", "debug-pending"} {
		got, err := repo.FindSession(ctx, sessionID)
		if err != nil {
			t.Fatalf("FindSession(%s) error = %v", sessionID, err)
		}
		if got.Status != task.DebugSessionFailed || got.CompletedAt == nil {
			t.Fatalf("interrupted session = %#v", got)
		}
	}
	done, err := repo.FindSession(ctx, "debug-done")
	if err != nil {
		t.Fatalf("FindSession(debug-done) error = %v", err)
	}
	if done.Status != task.DebugSessionSucceeded {
		t.Fatalf("terminal session was overwritten: %#v", done)
	}
}

func TestDebugRepositoryAppendsAndListsEvents(t *testing.T) {
	db := openRepoTestDB(t)
	repo := NewDebugRepository(db)
	ctx := context.Background()
	session := &task.DebugSession{
		SessionID: "debug-2",
		SourceID:  "source-2",
		Mode:      task.DebugModeContentURL,
		Status:    task.DebugSessionRunning,
		URL:       "https://example.test/c/1",
		CreatedAt: time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC),
	}
	if err := repo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	first := &task.DebugEvent{
		SessionID: session.SessionID,
		Type:      task.DebugEventStarted,
		Level:     task.DebugLevelInfo,
		Message:   "started",
		TraceID:   "trace-2",
		CreatedAt: session.CreatedAt,
	}
	if err := repo.AppendEvent(ctx, first); err != nil {
		t.Fatalf("AppendEvent(first) error = %v", err)
	}
	second := &task.DebugEvent{
		SessionID: session.SessionID,
		Type:      task.DebugEventLog,
		Stage:     "content",
		Level:     task.DebugLevelDebug,
		Message:   "parsed",
		ElapsedMS: 12,
		Data:      map[string]any{"count": float64(1)},
		CreatedAt: session.CreatedAt.Add(time.Millisecond),
	}
	if err := repo.AppendEvent(ctx, second); err != nil {
		t.Fatalf("AppendEvent(second) error = %v", err)
	}
	if first.EventID == 0 || second.EventID <= first.EventID {
		t.Fatalf("event ids first=%d second=%d", first.EventID, second.EventID)
	}

	events, err := repo.ListEvents(ctx, session.SessionID, 0)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 2 || events[0].SourceID != "source-2" || events[1].Stage != "content" || events[1].Data["count"] != float64(1) {
		t.Fatalf("events = %#v", events)
	}
	events, err = repo.ListEvents(ctx, session.SessionID, first.EventID)
	if err != nil {
		t.Fatalf("ListEvents(after) error = %v", err)
	}
	if len(events) != 1 || events[0].EventID != second.EventID {
		t.Fatalf("after events = %#v", events)
	}
}

func TestListEventsUsesSessionIndex(t *testing.T) {
	db := openRepoTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+listEventsSQL, "debug-1", 0)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_debug_events_session") {
		t.Fatalf("ListEvents plan does not use idx_debug_events_session:\n%s", joined)
	}
}
