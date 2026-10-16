package checkapp

import (
	"context"
	"testing"
	"time"

	"reader/internal/domain/task"
)

func TestPublisherReplaysAndPublishesLiveEvents(t *testing.T) {
	publisher := NewPublisher()
	publisher.Open("task-1")
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventStarted, Message: "check started"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, ok := publisher.Subscribe(ctx, "task-1")
	if !ok {
		t.Fatal("Subscribe() ok = false, want true for an opened stream")
	}
	assertCheckEvent(t, events, "check started")

	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventCompleted, Message: "succeeded"})
	assertCheckEvent(t, events, "succeeded")
}

func TestPublisherRejectsUnknownStream(t *testing.T) {
	publisher := NewPublisher()
	if _, ok := publisher.Subscribe(context.Background(), "missing"); ok {
		t.Fatal("Subscribe() ok = true for an unknown task stream")
	}
}

func TestPublisherDoesNotDuplicateEventsAcrossSubscribe(t *testing.T) {
	publisher := NewPublisher()
	publisher.Open("task-1")
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventItemPassed, Message: "first"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := publisher.Subscribe(ctx, "task-1")
	assertCheckEvent(t, events, "first")
	select {
	case event := <-events:
		t.Fatalf("duplicate event delivered: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPublisherSweepsFinishedStreamsAfterRetention(t *testing.T) {
	publisher := NewPublisher()
	now := time.Now()
	publisher.now = func() time.Time { return now }
	publisher.Open("task-1")
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventCompleted, Message: "succeeded"})

	now = now.Add(streamRetention - time.Second)
	publisher.Open("task-2")
	if !streamExists(publisher, "task-1") {
		t.Fatal("stream was swept before the retention window elapsed")
	}
	now = now.Add(2 * time.Second)
	publisher.Open("task-3")
	if streamExists(publisher, "task-1") {
		t.Fatal("stream survived the retention window")
	}

	// 仍有订阅者的收敛流不能清理，否则在线客户端会失去后续事件。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := publisher.Subscribe(ctx, "task-2"); !ok {
		t.Fatal("Subscribe() ok = false")
	}
	publisher.Publish(&task.CheckEvent{TaskID: "task-2", Type: task.CheckEventCompleted, Message: "succeeded"})
	now = now.Add(streamRetention + time.Minute)
	publisher.Open("task-4")
	if !streamExists(publisher, "task-2") {
		t.Fatal("stream with an active subscriber was swept")
	}
}

func streamExists(publisher *Publisher, taskID string) bool {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	_, ok := publisher.streams[taskID]
	return ok
}

func TestPublisherDisconnectsLaggedSubscriber(t *testing.T) {
	publisher := NewPublisher()
	publisher.Open("task-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, ok := publisher.Subscribe(ctx, "task-1")
	if !ok {
		t.Fatal("Subscribe() ok = false")
	}
	const published = 3 * subscriberBuffer
	for range published {
		publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventItemPassed, Message: "event"})
	}
	drained := 0
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				if drained == 0 || drained >= published {
					t.Fatalf("drained %d events before close, want a partial delivery of %d", drained, published)
				}
				return
			}
			drained++
		case <-deadline:
			t.Fatalf("lagged subscriber was not disconnected after %d events", drained)
		}
	}
}

func assertCheckEvent(t *testing.T, events <-chan *task.CheckEvent, wantMessage string) {
	t.Helper()
	select {
	case event := <-events:
		if event.Message != wantMessage {
			t.Fatalf("event message = %q, want %q", event.Message, wantMessage)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for event %q", wantMessage)
	}
}
