package debugapp

import (
	"context"
	"sync"

	"reader/internal/domain/task"
)

type Runner struct {
	flow *KeywordFlow
	mu   sync.Mutex
	runs map[string]context.CancelFunc
}

func NewRunner(flow *KeywordFlow) *Runner {
	return &Runner{flow: flow, runs: map[string]context.CancelFunc{}}
}

func (r *Runner) Start(ctx context.Context, session *task.DebugSession) {
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if existing := r.runs[session.SessionID]; existing != nil {
		existing()
	}
	r.runs[session.SessionID] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.runs, session.SessionID)
			r.mu.Unlock()
		}()
		if session.Mode == task.DebugModeKeyword {
			_ = r.flow.RunKeyword(runCtx, session, session.Keyword)
			return
		}
		_ = r.flow.RunURL(runCtx, session)
	}()
}

func (r *Runner) Cancel(sessionID string) bool {
	r.mu.Lock()
	cancel := r.runs[sessionID]
	if cancel != nil {
		delete(r.runs, sessionID)
	}
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}
