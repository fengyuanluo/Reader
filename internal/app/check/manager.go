package checkapp

import (
	"context"
	"time"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

type Runner interface {
	Start(ctx context.Context, checkTask *task.CheckTask, sources []*source.BookSource, opts Options, concurrency int) error
	Cancel(taskID string) bool
}

type Manager struct {
	repo    task.CheckRepository
	sources source.BookSourceRepository
	runner  Runner
}

func NewManager(repo task.CheckRepository, sources source.BookSourceRepository, runner Runner) *Manager {
	return &Manager{repo: repo, sources: sources, runner: runner}
}

func (m *Manager) Start(ctx context.Context, checkTask *task.CheckTask, sourceIDs []string, opts Options, concurrency int) (*task.CheckTask, error) {
	sources := make([]*source.BookSource, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		src, err := m.sources.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	if checkTask.CreatedAt.IsZero() {
		checkTask.CreatedAt = time.Now().UTC()
	}
	if checkTask.Status == "" {
		checkTask.Status = task.CheckTaskPending
	}
	if err := m.runner.Start(ctx, checkTask, sources, opts, concurrency); err != nil {
		return nil, err
	}
	return m.repo.FindTask(ctx, checkTask.TaskID)
}

func (m *Manager) Get(ctx context.Context, taskID string) (*task.CheckTask, error) {
	return m.repo.FindTask(ctx, taskID)
}

func (m *Manager) Cancel(ctx context.Context, taskID string) (*task.CheckTask, error) {
	m.runner.Cancel(taskID)
	completedAt := time.Now().UTC()
	current, err := m.repo.FindTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if current.Status == task.CheckTaskPending || current.Status == task.CheckTaskRunning {
		if err := m.repo.UpdateTaskStatus(ctx, taskID, task.CheckTaskCancelled, current.Finished, &completedAt); err != nil {
			return nil, err
		}
	}
	return m.repo.FindTask(ctx, taskID)
}
