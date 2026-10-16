package debugapp

import (
	"context"
	"strings"
	"sync"
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

func TestPublisherDedupesEventPublishedDuringSubscribe(t *testing.T) {
	// 订阅注册与历史读取之间发布的事件必须只出现一次：既不能被丢弃，也不能重复推送。
	repo := &fakeDebugRepo{}
	publisher := NewPublisher(repo)
	repo.beforeListEvents = func() {
		repo.publishLocked(&task.DebugEvent{SessionID: "s1", Type: task.DebugEventLog, Level: task.DebugLevelInfo, Message: "during-subscribe"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := publisher.Subscribe(ctx, "s1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	assertDebugEvent(t, ch, "during-subscribe")
	select {
	case event := <-ch:
		t.Fatalf("duplicate event delivered: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPublisherDisconnectsLaggedSubscriber(t *testing.T) {
	// 缓冲写满时不能静默丢弃事件：订阅被断开，客户端可重连后按 Last-Event-ID 补齐。
	repo := &fakeDebugRepo{}
	publisher := NewPublisher(repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := publisher.Subscribe(ctx, "s1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	// 订阅者完全不消费：订阅缓冲与转发缓冲写满后必须断开连接而不是静默丢弃。
	const published = 3 * subscriberBuffer
	for range published {
		if err := publisher.Publish(context.Background(), &task.DebugEvent{SessionID: "s1", Type: task.DebugEventLog, Level: task.DebugLevelInfo, Message: "event"}); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}
	drained := 0
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				if drained == 0 || drained >= published {
					t.Fatalf("drained %d events before close, want a partial delivery of %d", drained, published)
				}
				if len(repo.events) != published {
					t.Fatalf("persisted events = %d, want %d", len(repo.events), published)
				}
				return
			}
			drained++
		case <-deadline:
			t.Fatalf("lagged subscriber was not disconnected after %d events", drained)
		}
	}
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
	mu               sync.Mutex
	events           []*task.DebugEvent
	sessions         map[string]*task.DebugSession
	beforeListEvents func()
}

func (r *fakeDebugRepo) CreateSession(ctx context.Context, session *task.DebugSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		r.sessions = map[string]*task.DebugSession{}
	}
	copy := *session
	r.sessions[session.SessionID] = &copy
	return nil
}

func (r *fakeDebugRepo) FindSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[sessionID]
	if session == nil {
		return &task.DebugSession{SessionID: sessionID}, nil
	}
	copy := *session
	return &copy, nil
}

func (r *fakeDebugRepo) UpdateSessionStatus(ctx context.Context, sessionID string, status task.DebugSessionStatus, completedAt *time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[sessionID]
	if session == nil {
		return false, nil
	}
	if task.DebugSessionTerminal(session.Status) {
		return false, nil
	}
	session.Status = status
	session.CompletedAt = completedAt
	return true, nil
}

func (r *fakeDebugRepo) FailInterruptedSessions(ctx context.Context, completedAt time.Time) (int64, error) {
	return 0, nil
}

func (r *fakeDebugRepo) AppendEvent(ctx context.Context, event *task.DebugEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishLocked(event)
	return nil
}

func (r *fakeDebugRepo) publishLocked(event *task.DebugEvent) {
	copy := *event
	copy.EventID = int64(len(r.events) + 1)
	r.events = append(r.events, &copy)
}

func (r *fakeDebugRepo) ListEvents(ctx context.Context, sessionID string, afterEventID int64) ([]*task.DebugEvent, error) {
	if hook := r.beforeListEvents; hook != nil {
		r.beforeListEvents = nil
		hook()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []*task.DebugEvent{}
	for _, event := range r.events {
		if event.SessionID == sessionID && event.EventID > afterEventID {
			out = append(out, event)
		}
	}
	return out, nil
}
