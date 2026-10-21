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
	// defaultSweepInterval 是后台清理定时器间隔：任务收敛后即使无人再 Publish，
	// 事件流也会在保留期结束后被回收（S03-10），而不是滞留到下一次任意任务活动。
	defaultSweepInterval = time.Minute
	// runningStreamLimit 是单个运行中任务事件流的保留上限：超出时丢弃最旧事件。
	// 事件只用于回放与实时推送，大任务下全量保留会让每个流占用数 MB 内存（S03-10）。
	runningStreamLimit = 1000
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
	// lastEventID 是任务流内已分配的最大事件序号，Publish 时单调递增。
	lastEventID int64
}

// Publisher 发布并分发校验事件。
// 事件只在内存中保留（任务进度本身由 check_tasks/check_task_items 持久化），
// 因此 Open 必须在任务开始执行前建立事件流，否则订阅者拿不到早期事件。
type Publisher struct {
	mu            sync.Mutex
	streams       map[string]*checkStream
	now           func() time.Time
	sweepInterval time.Duration
	// sweepRuns 统计后台 sweep 执行次数，供测试断言 ticker 驱动的清理确实运行。
	sweepRuns int
	stop      chan struct{}
	stopOnce  sync.Once
}

func NewPublisher() *Publisher {
	return newPublisher(defaultSweepInterval, time.Now)
}

// newPublisher 供测试注入 sweep 间隔与时钟。
func newPublisher(sweepInterval time.Duration, now func() time.Time) *Publisher {
	if sweepInterval <= 0 {
		sweepInterval = defaultSweepInterval
	}
	p := &Publisher{
		streams:       map[string]*checkStream{},
		now:           now,
		sweepInterval: sweepInterval,
		stop:          make(chan struct{}),
	}
	go p.sweepLoop()
	return p
}

// Close 停止后台清理定时器；进程退出或测试收尾时调用。
func (p *Publisher) Close() {
	p.stopOnce.Do(func() { close(p.stop) })
}

// sweepLoop 是后台定时清理：任务收敛后即使无人再 Publish/Open，
// 事件流也会在保留期结束后被回收（S03-10）。
func (p *Publisher) sweepLoop() {
	ticker := time.NewTicker(p.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			p.sweepLocked()
			p.sweepRuns++
			p.mu.Unlock()
		}
	}
}

// Open 为任务建立空事件流，供订阅者在任务开始前先行订阅。
func (p *Publisher) Open(taskID string) {
	if taskID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streamLocked(taskID)
}

// Publish 追加事件并推送给订阅者。追加与推送在同一把锁内完成，
// 因此订阅者从事件流读到的历史与实时事件不会重复、也不会漏发。
// 每条事件分配任务流内单调递增的 EventID（从 1 开始），供 SSE 断线续传；
// 运行中任务的事件流有保留上限：超出时丢弃最旧事件（S03-10）。
func (p *Publisher) Publish(event *task.CheckEvent) {
	if event == nil || event.TaskID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	stream := p.streamLocked(event.TaskID)
	stream.lastEventID++
	event.EventID = stream.lastEventID
	stream.events = append(stream.events, event)
	if overflow := len(stream.events) - runningStreamLimit; overflow > 0 {
		stream.events = append([]*task.CheckEvent(nil), stream.events[overflow:]...)
	}
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
// afterEventID 小于等于 0 时回放全部历史；大于 0 时跳过 EventID<=afterEventID 的历史事件
// （SSE Last-Event-ID 断线续传），实时事件照发。
// 回放是惰性的：forward goroutine 先把历史事件排入 out，再切实时事件，
// 不在锁外构造大缓冲，也不对历史做 O(events) 通道预填（S03-07）。
func (p *Publisher) Subscribe(ctx context.Context, taskID string, afterEventID int64) (<-chan *task.CheckEvent, bool) {
	p.mu.Lock()
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
	// 切片头在锁内捕获；append 只在 Publish 丢弃最旧事件时替换底层切片，
	// 因此这里读到的切片内容不可变，可以安全地在锁外遍历。
	history := stream.events
	p.mu.Unlock()
	if afterEventID > 0 {
		// 跳过 EventID<=afterEventID 的已消费事件。
		for len(history) > 0 && history[0].EventID <= afterEventID {
			history = history[1:]
		}
	}

	out := make(chan *task.CheckEvent, subscriberBuffer)
	go p.forward(ctx, stream, sub, history, out)
	return out, true
}

func (p *Publisher) forward(ctx context.Context, stream *checkStream, sub *checkSubscriber, history []*task.CheckEvent, out chan *task.CheckEvent) {
	defer close(out)
	defer p.unsubscribe(stream, sub)
	// 先排空历史回放，再转发实时事件；订阅者从 out 读到的顺序与发布顺序一致。
	for _, event := range history {
		select {
		case out <- event:
		case <-ctx.Done():
			return
		}
	}
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
