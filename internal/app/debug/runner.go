package debugapp

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"reader/internal/domain/task"
)

// statusWriteTimeout 限制会话终态落库/事件发布的时间，避免请求 ctx 结束后卡住。
const statusWriteTimeout = 5 * time.Second

// runHandle 代表一次会话运行；同一 sessionID 重跑时，旧运行的清理不能删除新运行的句柄。
type runHandle struct {
	cancel context.CancelFunc
}

type Runner struct {
	repo task.DebugRepository
	flow *KeywordFlow
	mu   sync.Mutex
	runs map[string]*runHandle
}

func NewRunner(flow *KeywordFlow, repo task.DebugRepository) *Runner {
	return &Runner{repo: repo, flow: flow, runs: map[string]*runHandle{}}
}

func (r *Runner) Start(ctx context.Context, session *task.DebugSession) {
	runCtx, cancel := context.WithCancel(ctx)
	handle := &runHandle{cancel: cancel}
	r.mu.Lock()
	if existing := r.runs[session.SessionID]; existing != nil {
		existing.cancel()
	}
	r.runs[session.SessionID] = handle
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			// 只有仍属于本次运行的句柄才删除，避免误删同 ID 新运行的 cancel。
			if r.runs[session.SessionID] == handle {
				delete(r.runs, session.SessionID)
			}
			r.mu.Unlock()
		}()
		var err error
		if session.Mode == task.DebugModeKeyword {
			err = r.flow.RunKeyword(runCtx, session, session.Keyword)
		} else {
			err = r.flow.RunURL(runCtx, session)
		}
		r.finish(session, err, runCtx.Err() != nil)
	}()
}

// finish 按 flow 的返回结果落库会话终态并补上 completedAt。
// runCtx 已被取消说明终态由 CancelSession 负责，这里不再覆写。
func (r *Runner) finish(session *task.DebugSession, runErr error, cancelled bool) {
	if cancelled {
		return
	}
	status := task.DebugSessionSucceeded
	if runErr != nil {
		status = task.DebugSessionFailed
	}
	completedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), statusWriteTimeout)
	defer cancel()
	applied, err := r.repo.UpdateSessionStatus(ctx, session.SessionID, status, &completedAt)
	if err != nil {
		slog.Warn("update debug session status failed", "sessionId", session.SessionID, "status", status, "error", err)
		return
	}
	if !applied {
		slog.Debug("debug session already terminal, skip status update", "sessionId", session.SessionID, "status", status)
	}
}

func (r *Runner) Cancel(sessionID string) bool {
	r.mu.Lock()
	handle := r.runs[sessionID]
	if handle != nil {
		delete(r.runs, sessionID)
	}
	r.mu.Unlock()
	if handle == nil {
		return false
	}
	handle.cancel()
	return true
}
