package debugapp

import (
	"context"
	"errors"
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
	// slots 是运行中会话的并发上限信号量：Start 无条件 go func 会堆积任意数量
	// 的后台 goroutine 与多阶段网络抓取，满时直接把会话置 failed。
	slots chan struct{}
}

// defaultMaxRunningSessions 是未显式配置时的默认并发上限。
const defaultMaxRunningSessions = 4

func NewRunner(flow *KeywordFlow, repo task.DebugRepository) *Runner {
	return &Runner{repo: repo, flow: flow, runs: map[string]*runHandle{}, slots: make(chan struct{}, defaultMaxRunningSessions)}
}

// WithMaxRunningSessions 设置运行中调试会话的并发上限（参照 WithCheckConcurrency 惯例）。
func (r *Runner) WithMaxRunningSessions(max int) *Runner {
	if max <= 0 {
		max = defaultMaxRunningSessions
	}
	r.slots = make(chan struct{}, max)
	return r
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
	// 非阻塞获取并发槽：满时不再排队，直接把会话置 failed，
	// 避免脚本刷接口堆积任意数量的后台抓取 goroutine。
	select {
	case r.slots <- struct{}{}:
	default:
		cancel()
		r.mu.Lock()
		if r.runs[session.SessionID] == handle {
			delete(r.runs, session.SessionID)
		}
		r.mu.Unlock()
		go r.finish(session, errors.New("too many running debug sessions"), false)
		return
	}
	go func() {
		defer func() {
			<-r.slots
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
