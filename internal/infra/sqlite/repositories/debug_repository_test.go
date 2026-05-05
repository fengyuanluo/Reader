package repositories

import (
	"context"
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
	if err := repo.UpdateSessionStatus(ctx, session.SessionID, task.DebugSessionSucceeded, &completedAt); err != nil {
		t.Fatalf("UpdateSessionStatus() error = %v", err)
	}
	got, err = repo.FindSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("FindSession() after update error = %v", err)
	}
	if got.Status != task.DebugSessionSucceeded || got.CompletedAt == nil || !got.CompletedAt.Equal(completedAt) {
		t.Fatalf("updated session = %#v", got)
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
