package checkapp

import (
	"context"
	"sync"
	"time"

	"reader/internal/domain/task"
)

const (
	// subscriberBuffer 是单个订阅者的实时事件缓冲；写满说明消费端跟不上发布速度。
	subscriberBuffer = 16
	// streamRetention 是任务收敛后事件流的保留时长，留出窗口让客户端补订阅终态事件。
	streamRetention = 5 * time.Minute
)

type checkSubscriber struct {
	ch     chan *task.CheckEvent
	closed bool
}

// checkStream 保存单个任务的事件流：事件用于回放，subs 是本进程内的实时订阅者。
type checkStream struct {
	events []*task.CheckEvent
	subs   map[*checkSubscriber]struct{}
	doneAt time.Time
}

// Publisher 发布并分发校验任务事件。
// 事件只在内存中保留（任务进度本身由 check_tasks/check_task_items 持久化），
// 因此 Open 必须在任务开始执行前建立事件流，否则订阅者拿不到早期事件。
type Publisher struct {
	mu      sync.Mutex
	streams map[string]*checkStream
	now     func() time.Time
}

func NewPublisher() *Publisher {
	return &Publisher{streams: map[string]*checkStream{}, now: time.Now}
}

// Open 为任务建立空事件流，供订阅者在任务开始前先行订阅。
func (p *Publisher) Open(taskID string) {
	if taskID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	p.streamLocked(taskID)
}

// Publish 追加事件并推送给订阅者。追加与推送在同一把锁内完成，
// 因此订阅者从事件流读到的历史与实时事件不会重复、也不会漏发。
func (p *Publisher) Publish(event *task.CheckEvent) {
	if event == nil || event.TaskID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	stream := p.streamLocked(event.TaskID)
	stream.events = append(stream.events, event)
	for sub := range stream.subs {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			// 缓冲写满时断开该订阅，而不是静默丢弃事件：客户端可重新订阅并回放事件流。
			p.closeSubscriberLocked(stream, sub)
		}
	}
	if task.IsTerminalCheckEvent(event.Type) {
		stream.doneAt = p.now()
	}
}

// Subscribe 回放已有事件并继续接收实时事件；事件流不存在时 ok 为 false。
func (p *Publisher) Subscribe(ctx context.Context, taskID string) (<-chan *task.CheckEvent, bool) {
	p.mu.Lock()
	p.sweepLocked()
	stream := p.streams[taskID]
	if stream == nil {
		p.mu.Unlock()
		return nil, false
	}
	sub := &checkSubscriber{ch: make(chan *task.CheckEvent, subscriberBuffer)}
	if stream.subs == nil {
		stream.subs = map[*checkSubscriber]struct{}{}
	}
	stream.subs[sub] = struct{}{}
	history := append([]*task.CheckEvent(nil), stream.events...)
	p.mu.Unlock()

	out := make(chan *task.CheckEvent, len(history)+subscriberBuffer)
	for _, event := range history {
		out <- event
	}
	go p.forward(ctx, stream, sub, out)
	return out, true
}

func (p *Publisher) forward(ctx context.Context, stream *checkStream, sub *checkSubscriber, out chan *task.CheckEvent) {
	defer close(out)
	defer p.unsubscribe(stream, sub)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.ch:
			if !ok {
				return
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (p *Publisher) unsubscribe(stream *checkStream, sub *checkSubscriber) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeSubscriberLocked(stream, sub)
}

func (p *Publisher) closeSubscriberLocked(stream *checkStream, sub *checkSubscriber) {
	delete(stream.subs, sub)
	if !sub.closed {
		sub.closed = true
		close(sub.ch)
	}
}

func (p *Publisher) streamLocked(taskID string) *checkStream {
	stream := p.streams[taskID]
	if stream == nil {
		stream = &checkStream{}
		p.streams[taskID] = stream
	}
	return stream
}

// sweepLocked 清理超过保留期且已收敛的事件流，避免内存随校验历史无限增长。
func (p *Publisher) sweepLocked() {
	now := p.now()
	for taskID, stream := range p.streams {
		if stream.doneAt.IsZero() || len(stream.subs) > 0 {
			continue
		}
		if now.Sub(stream.doneAt) >= streamRetention {
			delete(p.streams, taskID)
		}
	}
}
