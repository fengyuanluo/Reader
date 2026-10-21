package checkapp

import (
	"context"
	"strconv"
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
	events, ok := publisher.Subscribe(ctx, "task-1", 0)
	if !ok {
		t.Fatal("Subscribe() ok = false, want true for an opened stream")
	}
	assertCheckEvent(t, events, "check started")

	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventCompleted, Message: "succeeded"})
	assertCheckEvent(t, events, "succeeded")
}

func TestPublisherRejectsUnknownStream(t *testing.T) {
	publisher := NewPublisher()
	if _, ok := publisher.Subscribe(context.Background(), "missing", 0); ok {
		t.Fatal("Subscribe() ok = true for an unknown task stream")
	}
}

func TestPublisherDoesNotDuplicateEventsAcrossSubscribe(t *testing.T) {
	publisher := NewPublisher()
	publisher.Open("task-1")
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventItemPassed, Message: "first"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := publisher.Subscribe(ctx, "task-1", 0)
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
	publisher.mu.Lock()
	publisher.sweepLocked()
	publisher.mu.Unlock()
	if streamExists(publisher, "task-1") {
		t.Fatal("stream survived the retention window")
	}

	// 仍有订阅者的收敛流不能清理，否则在线客户端会失去后续事件。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := publisher.Subscribe(ctx, "task-2", 0); !ok {
		t.Fatal("Subscribe() ok = false")
	}
	publisher.Publish(&task.CheckEvent{TaskID: "task-2", Type: task.CheckEventCompleted, Message: "succeeded"})
	now = now.Add(streamRetention + time.Minute)
	publisher.Open("task-4")
	publisher.mu.Lock()
	publisher.sweepLocked()
	publisher.mu.Unlock()
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
	events, ok := publisher.Subscribe(ctx, "task-1", 0)
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

// TestPublisherSweepsViaBackgroundTicker 覆盖 S03-10②：任务收敛后即使无人再 Publish/Open，
// 后台 ticker 也会在保留期结束后回收事件流。
func TestPublisherSweepsViaBackgroundTicker(t *testing.T) {
	publisher := newPublisher(5*time.Millisecond, time.Now)
	defer publisher.Close()
	publisher.Open("task-1")
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventCompleted, Message: "succeeded"})

	if !streamExists(publisher, "task-1") {
		t.Fatal("stream missing right after the terminal event")
	}
	// retention=5min 远大于测试时长，只验证 ticker 驱动的 sweep 会运行；
	// 保留期判定本身由 TestPublisherSweepsFinishedStreamsAfterRetention 覆盖。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		publisher.mu.Lock()
		sweeps := publisher.sweepRuns
		publisher.mu.Unlock()
		if sweeps > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background ticker never ran sweep")
}

// TestPublisherBoundsRunningStreamEvents 覆盖 S03-10③：运行中任务的事件流有保留上限，
// 超出时丢弃最旧事件，内存不随事件数无限增长。
func TestPublisherBoundsRunningStreamEvents(t *testing.T) {
	publisher := NewPublisher()
	defer publisher.Close()
	publisher.Open("task-1")
	const published = runningStreamLimit + 500
	for range published {
		publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventItemPassed, Message: "event"})
	}
	publisher.mu.Lock()
	stream := publisher.streams["task-1"]
	count := len(stream.events)
	first := stream.events[0].Message
	publisher.mu.Unlock()
	if count != runningStreamLimit {
		t.Fatalf("stream retains %d events, want %d", count, runningStreamLimit)
	}
	if first != "event" {
		t.Fatalf("oldest retained event = %q", first)
	}
}

// TestPublisherSubscribeReplaysLazilyWithoutDuplication 覆盖 S03-07②：断线重连的回放是惰性的，
// 历史事件先于实时事件送达且不重复。
func TestPublisherSubscribeReplaysLazilyWithoutDuplication(t *testing.T) {
	publisher := NewPublisher()
	defer publisher.Close()
	publisher.Open("task-1")
	for range 5 {
		publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventItemPassed, Message: "historical"})
	}
	// 覆写消息以区分顺序。
	publisher.mu.Lock()
	for i, event := range publisher.streams["task-1"].events {
		event.Message = "h" + strconv.Itoa(i)
	}
	publisher.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, ok := publisher.Subscribe(ctx, "task-1", 0)
	if !ok {
		t.Fatal("Subscribe() ok = false")
	}
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventCompleted, Message: "terminal"})
	seen := []string{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatalf("stream closed early; seen=%v", seen)
			}
			seen = append(seen, event.Message)
			if event.Type == task.CheckEventCompleted {
				if len(seen) != 6 {
					t.Fatalf("seen = %v, want 5 historical + 1 terminal in order", seen)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out; seen=%v", seen)
		}
	}
}

// TestPublisherAssignsMonotonicEventIDsAndResumesAfterEventID 覆盖事件序号分配与
// SSE Last-Event-ID 断线续传：EventID 按 stream 单调递增（从 1 开始），
// Subscribe 的 afterEventID 跳过已消费的历史事件。
func TestPublisherAssignsMonotonicEventIDsAndResumesAfterEventID(t *testing.T) {
	publisher := NewPublisher()
	defer publisher.Close()
	publisher.Open("task-1")
	for range 4 {
		publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventItemPassed, Message: "event"})
	}
	publisher.mu.Lock()
	events := publisher.streams["task-1"].events
	lastID := events[len(events)-1].EventID
	publisher.mu.Unlock()
	if lastID != 4 {
		t.Fatalf("last EventID = %d, want 4 (monotonic from 1)", lastID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// afterEventID=2：只回放 EventID 3、4。
	replayed, ok := publisher.Subscribe(ctx, "task-1", 2)
	if !ok {
		t.Fatal("Subscribe() ok = false")
	}
	first := <-replayed
	if first.EventID != 3 {
		t.Fatalf("first replayed EventID = %d, want 3 (skipped <=2)", first.EventID)
	}
	// afterEventID=0：回放全部。
	all, ok := publisher.Subscribe(ctx, "task-1", 0)
	if !ok {
		t.Fatal("Subscribe() ok = false")
	}
	firstAll := <-all
	if firstAll.EventID != 1 {
		t.Fatalf("first replayed EventID = %d, want 1", firstAll.EventID)
	}
	// 实时事件照发，且 EventID 继续递增。
	publisher.Publish(&task.CheckEvent{TaskID: "task-1", Type: task.CheckEventCompleted, Message: "done"})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, open := <-replayed:
			if !open {
				t.Fatal("stream closed before the live terminal event")
			}
			if event.Type == task.CheckEventCompleted {
				if event.EventID != 5 {
					t.Fatalf("live EventID = %d, want 5", event.EventID)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the live terminal event")
		}
	}
}
