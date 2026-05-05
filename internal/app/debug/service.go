package debugapp

import (
	"context"
	"time"

	"reader/internal/domain/task"
)

type Service struct {
	repo   task.DebugRepository
	runner *Runner
}

func NewService(repo task.DebugRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) WithRunner(runner *Runner) *Service {
	s.runner = runner
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
		s.runner.Start(context.Background(), created)
	}
	return created, nil
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	return s.repo.FindSession(ctx, sessionID)
}

func (s *Service) CancelSession(ctx context.Context, sessionID string) (*task.DebugSession, error) {
	cancelledAt := time.Now().UTC()
	if s.runner != nil {
		s.runner.Cancel(sessionID)
	}
	if err := s.repo.UpdateSessionStatus(ctx, sessionID, task.DebugSessionCancelled, &cancelledAt); err != nil {
		return nil, err
	}
	return s.repo.FindSession(ctx, sessionID)
}

func (s *Service) AppendEvent(ctx context.Context, event *task.DebugEvent) error {
	return s.repo.AppendEvent(ctx, event)
}

func (s *Service) Events(ctx context.Context, sessionID string, afterEventID int64) ([]*task.DebugEvent, error) {
	return s.repo.ListEvents(ctx, sessionID, afterEventID)
}
