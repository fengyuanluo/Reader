package debugapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/task"
)

func TestPublisherReplaysHistoryAndPublishesLiveEvents(t *testing.T) {
	repo := &fakeDebugRepo{}
	publisher := NewPublisher(repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo.events = append(repo.events, &task.DebugEvent{EventID: 1, SessionID: "s1", Type: task.DebugEventStarted, Level: task.DebugLevelInfo, Message: "history"})
	ch, err := publisher.Subscribe(ctx, "s1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	assertDebugEvent(t, ch, "history")

	if err := publisher.Publish(context.Background(), &task.DebugEvent{SessionID: "s1", Type: task.DebugEventLog, Level: task.DebugLevelInfo, Message: "live"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	assertDebugEvent(t, ch, "live")
}

func TestPublisherRedactsMessageBeforePersisting(t *testing.T) {
	repo := &fakeDebugRepo{}
	publisher := NewPublisher(repo)

	err := publisher.Publish(context.Background(), &task.DebugEvent{SessionID: "s1", Type: task.DebugEventLog, Message: "GET https://api.example.test/s?apikey=k123 X-Api-Key: secret-key"})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	stored := repo.events[0].Message
	for _, secret := range []string{"k123", "secret-key"} {
		if strings.Contains(stored, secret) {
			t.Fatalf("stored message still contains %q: %s", secret, stored)
		}
	}
}

func assertDebugEvent(t *testing.T, ch <-chan *task.DebugEvent, want string) {
	t.Helper()
	select {
	case event := <-ch:
		if event.Message != want {
			t.Fatalf("event message = %q, want %q", event.Message, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for event %q", want)
	}
}

type fakeDebugRepo struct {
	events []*task.DebugEvent
}

func (r *fakeDebugRepo) CreateSession(ctx context.Context, session *task.DebugSession) error {
	return nil
}

func (r *fakeDebugRepo) FindSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	return &task.DebugSession{SessionID: sessionID}, nil
}

func (r *fakeDebugRepo) UpdateSessionStatus(ctx context.Context, sessionID string, status task.DebugSessionStatus, completedAt *time.Time) error {
	return nil
}

func (r *fakeDebugRepo) AppendEvent(ctx context.Context, event *task.DebugEvent) error {
	event.EventID = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	return nil
}

func (r *fakeDebugRepo) ListEvents(ctx context.Context, sessionID string, afterEventID int64) ([]*task.DebugEvent, error) {
	out := []*task.DebugEvent{}
	for _, event := range r.events {
		if event.SessionID == sessionID && event.EventID > afterEventID {
			out = append(out, event)
		}
	}
	return out, nil
}
