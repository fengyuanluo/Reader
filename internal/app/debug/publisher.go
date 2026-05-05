package debugapp

import (
	"context"
	"sync"

	"reader/internal/domain/task"
)

type Publisher struct {
	repo task.DebugRepository
	mu   sync.Mutex
	subs map[string]map[chan *task.DebugEvent]struct{}
}

func NewPublisher(repo task.DebugRepository) *Publisher {
	return &Publisher{repo: repo, subs: map[string]map[chan *task.DebugEvent]struct{}{}}
}

func (p *Publisher) Publish(ctx context.Context, event *task.DebugEvent) error {
	if err := p.repo.AppendEvent(ctx, event); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.subs[event.SessionID] {
		select {
		case ch <- event:
		default:
		}
	}
	return nil
}

func (p *Publisher) Subscribe(ctx context.Context, sessionID string, afterEventID int64) (<-chan *task.DebugEvent, error) {
	history, err := p.repo.ListEvents(ctx, sessionID, afterEventID)
	if err != nil {
		return nil, err
	}
	ch := make(chan *task.DebugEvent, len(history)+16)
	for _, event := range history {
		ch <- event
	}

	p.mu.Lock()
	if p.subs[sessionID] == nil {
		p.subs[sessionID] = map[chan *task.DebugEvent]struct{}{}
	}
	p.subs[sessionID][ch] = struct{}{}
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		p.mu.Lock()
		delete(p.subs[sessionID], ch)
		if len(p.subs[sessionID]) == 0 {
			delete(p.subs, sessionID)
		}
		p.mu.Unlock()
		close(ch)
	}()
	return ch, nil
}
