package debugapp

import (
	"context"
	"log/slog"
	"time"

	"reader/internal/domain/task"
)

type Service struct {
	repo      task.DebugRepository
	runner    *Runner
	publisher *Publisher
}

func NewService(repo task.DebugRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) WithRunner(runner *Runner) *Service {
	s.runner = runner
	return s
}

func (s *Service) WithPublisher(publisher *Publisher) *Service {
	s.publisher = publisher
	return s
}

func (s *Service) CreateSession(ctx context.Context, session *task.DebugSession) (*task.DebugSession, error) {
	if session.Status == "" {
		session.Status = task.DebugSessionPending
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	created, err := s.repo.FindSession(ctx, session.SessionID)
	if err != nil {
		return nil, err
	}
	if s.runner != nil {
		// 会话执行生命周期独立于创建请求，请求结束后仍在后台运行。
		s.runner.Start(context.Background(), created)
	}
	return created, nil
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	return s.repo.FindSession(ctx, sessionID)
}

// CancelSession 取消运行中的会话。已经是终态的会话不再改写状态，取消与自然结束先到者胜出。
func (s *Service) CancelSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	current, err := s.repo.FindSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !task.DebugSessionTerminal(current.Status) {
		if s.runner != nil {
			s.runner.Cancel(sessionID)
		}
		cancelledAt := time.Now().UTC()
		applied, err := s.repo.UpdateSessionStatus(ctx, sessionID, task.DebugSessionCancelled, &cancelledAt)
		if err != nil {
			return nil, err
		}
		if applied {
			s.publishCancelled(ctx, current)
		}
	}
	return s.repo.FindSession(ctx, sessionID)
}

// publishCancelled 发布会话取消的终态事件；取消请求的 ctx 可能已结束，因此用独立超时上下文发布。
func (s *Service) publishCancelled(ctx context.Context, session *task.DebugSession) {
	if s.publisher == nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusWriteTimeout)
	defer cancel()
	event := &task.DebugEvent{
		SessionID: session.SessionID,
		SourceID:  session.SourceID,
		Type:      task.DebugEventCancelled,
		Stage:     "cancel",
		Level:     task.DebugLevelWarn,
		Message:   "debug cancelled",
		TraceID:   session.TraceID,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.publisher.Publish(pubCtx, event); err != nil {
		// 终态事件写库失败不能让取消请求失败，但必须留下记录供排查。
		slog.Warn("publish debug cancelled event failed", "sessionId", session.SessionID, "error", err)
	}
}

func (s *Service) AppendEvent(ctx context.Context, event *task.DebugEvent) error {
	return s.repo.AppendEvent(ctx, event)
}

func (s *Service) Events(ctx context.Context, sessionID string, afterEventID int64) ([]*task.DebugEvent, error) {
	return s.repo.ListEvents(ctx, sessionID, afterEventID)
}
