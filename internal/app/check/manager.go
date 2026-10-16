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

// DefaultConcurrency 是校验任务的默认并发上限（同时校验的书源数），
// 装配时用 config.SourceConcurrency（READER_SOURCE_CONCURRENCY）覆盖。
const DefaultConcurrency = 4

type Manager struct {
	repo    task.CheckRepository
	sources source.BookSourceRepository
	runner  Runner
	events  *Publisher
}

func NewManager(repo task.CheckRepository, sources source.BookSourceRepository, runner Runner) *Manager {
	return &Manager{repo: repo, sources: sources, runner: runner}
}

func (m *Manager) WithEvents(publisher *Publisher) *Manager {
	m.events = publisher
	return m
}

// Subscribe 返回任务事件流；事件流不存在（任务不是由本进程执行或已过保留期）时 ok 为 false，
// 调用方需要退回按持久化状态收尾。
func (m *Manager) Subscribe(ctx context.Context, taskID string) (<-chan *task.CheckEvent, bool) {
	if m.events == nil {
		return nil, false
	}
	return m.events.Subscribe(ctx, taskID)
}

// Start 创建并启动校验任务；concurrency 为同时校验的书源数上限，非正数时回退到 DefaultConcurrency。
func (m *Manager) Start(ctx context.Context, checkTask *task.CheckTask, sourceIDs []string, opts Options, concurrency int) (*task.CheckTask, error) {
	if concurrency < 1 {
		concurrency = DefaultConcurrency
	}
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
