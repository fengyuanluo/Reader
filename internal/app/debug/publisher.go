package debugapp

import (
	"context"
	"log/slog"
	"sync"

	"reader/internal/domain/task"
	"reader/internal/infra/logging"
)

// subscriberBuffer 是单个订阅者的实时事件缓冲；缓冲被写满说明消费端跟不上发布速度。
const subscriberBuffer = 16

// subscriber 是某个 SSE 连接在 Publisher 中的注册项。
// ch 由 Publisher 关闭（缓冲溢出）或订阅上下文结束时关闭，closed 防止重复关闭。
type subscriber struct {
	ch     chan *task.DebugEvent
	closed bool
}

type Publisher struct {
	repo task.DebugRepository
	mu   sync.Mutex
	subs map[string]map[*subscriber]struct{}
}

func NewPublisher(repo task.DebugRepository) *Publisher {
	return &Publisher{repo: repo, subs: map[string]map[*subscriber]struct{}{}}
}

func (p *Publisher) Publish(ctx context.Context, event *task.DebugEvent) error {
	// 调试事件会持久化并推送给 SSE 订阅者，消息中的凭证必须在落库前脱敏。
	event.Message = logging.RedactString(event.Message)
	if err := p.repo.AppendEvent(ctx, event); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for sub := range p.subs[event.SessionID] {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			// 静默丢弃会让客户端永久缺失事件，改为断开该订阅：
			// 客户端凭 Last-Event-ID 重连后可从库里补齐，事件不会真正丢失。
			slog.Warn("debug event subscriber lagged, closing subscription", "sessionId", event.SessionID, "eventId", event.EventID)
			p.closeSubscriberLocked(event.SessionID, sub)
		}
	}
	return nil
}

func (p *Publisher) Subscribe(ctx context.Context, sessionID string, afterEventID int64) (<-chan *task.DebugEvent, error) {
	sub := &subscriber{ch: make(chan *task.DebugEvent, subscriberBuffer)}
	p.mu.Lock()
	// 先注册订阅再读历史：注册之后发布的事件一定进入 channel，
	// 与历史重复的部分按 EventID 去重，注册窗口内不再丢事件。
	if p.subs[sessionID] == nil {
		p.subs[sessionID] = map[*subscriber]struct{}{}
	}
	p.subs[sessionID][sub] = struct{}{}
	p.mu.Unlock()

	history, err := p.repo.ListEvents(ctx, sessionID, afterEventID)
	if err != nil {
		p.unsubscribe(sessionID, sub)
		return nil, err
	}
	delivered := make(map[int64]struct{}, len(history))
	out := make(chan *task.DebugEvent, len(history)+subscriberBuffer)
	for _, event := range history {
		if event.EventID > 0 {
			delivered[event.EventID] = struct{}{}
		}
		out <- event
	}
	go p.forward(ctx, sessionID, sub, out, delivered)
	return out, nil
}

// forward 把历史事件与实时事件合并到单一有序通道，并按 EventID 去重。
func (p *Publisher) forward(ctx context.Context, sessionID string, sub *subscriber, out chan *task.DebugEvent, delivered map[int64]struct{}) {
	defer close(out)
	defer p.unsubscribe(sessionID, sub)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.ch:
			if !ok {
				return
			}
			if event.EventID > 0 {
				if _, dup := delivered[event.EventID]; dup {
					continue
				}
				delivered[event.EventID] = struct{}{}
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (p *Publisher) unsubscribe(sessionID string, sub *subscriber) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeSubscriberLocked(sessionID, sub)
}

func (p *Publisher) closeSubscriberLocked(sessionID string, sub *subscriber) {
	if subs := p.subs[sessionID]; subs != nil {
		delete(subs, sub)
		if len(subs) == 0 {
			delete(p.subs, sessionID)
		}
	}
	if !sub.closed {
		sub.closed = true
		close(sub.ch)
	}
}
