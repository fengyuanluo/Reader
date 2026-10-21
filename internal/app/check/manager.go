package checkapp

import (
	"context"
	"errors"
	"time"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

type Runner interface {
	Start(ctx context.Context, checkTask *task.CheckTask, sourceIDs []string, opts Options, concurrency int) error
	Cancel(taskID string) bool
}

// DefaultConcurrency 是校验任务的默认并发上限（同时校验的书源数），
// 装配时用 config.CheckConcurrency（READER_CHECK_CONCURRENCY）覆盖。
const DefaultConcurrency = 16

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
// 调用方需要退回按持久化状态收尾。afterEventID 大于 0 时跳过已消费的历史事件（SSE 断线续传）。
func (m *Manager) Subscribe(ctx context.Context, taskID string, afterEventID int64) (<-chan *task.CheckEvent, bool) {
	if m.events == nil {
		return nil, false
	}
	return m.events.Subscribe(ctx, taskID, afterEventID)
}

// Start 创建并启动校验任务；concurrency 为同时校验的书源数上限，非正数时回退到 DefaultConcurrency。
//
// 请求 ctx 在进入 Start 后立即脱钩（context.WithoutCancel）：后台校验任务由 runner 生命周期持有，
// 客户端断开或反向代理超时不能中止整个任务（S03-01）。Start 只做存在性校验（分批查询，S00-02），
// 不再逐个 FindByID 解析全部规则与 RawJSON；书源在 worker 拿到并发槽后再惰性加载。
func (m *Manager) Start(ctx context.Context, checkTask *task.CheckTask, sourceIDs []string, opts Options, concurrency int) error {
	ctx = context.WithoutCancel(ctx)
	if concurrency < 1 {
		concurrency = DefaultConcurrency
	}
	// 防重入：已有 pending/running 的校验任务时拒绝（对齐 Legado CheckSourceService 的“已有书源在校验”语义，S03-04）。
	if active, err := m.repo.HasUnfinishedTasks(ctx); err != nil {
		return err
	} else if active {
		return task.ErrCheckTaskRunning
	}
	missing, err := m.repo.FindMissingSourceIDs(ctx, sourceIDs)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return errors.Join(source.ErrNotFound, errors.New("sourceId 不存在: "+missing[0]))
	}
	if checkTask.CreatedAt.IsZero() {
		checkTask.CreatedAt = time.Now().UTC()
	}
	if checkTask.Status == "" {
		checkTask.Status = task.CheckTaskPending
	}
	if err := m.runner.Start(ctx, checkTask, sourceIDs, opts, concurrency); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Get(ctx context.Context, taskID string) (*task.CheckTask, error) {
	return m.repo.FindTask(ctx, taskID)
}

// ListItems 按页读取任务条目（S03-06）：任务详情的条目不再一次性全量返回，
// 支持按状态过滤；total 为该过滤条件下的条目总数。
func (m *Manager) ListItems(ctx context.Context, taskID string, status *task.CheckTaskItemStatus, page, pageSize int) ([]*task.CheckTaskItem, int, error) {
	return m.repo.ListItemsPage(ctx, taskID, status, page, pageSize)
}

// Cancel 请求取消任务；终态统一由 runner 收尾路径写入（S03-05），
// 这里不再直接把任务标 cancelled，避免与 runner 的终态写竞争、finished 回退。
// 任务不是由本进程执行（无运行句柄）时 ok 为 false，调用方按持久化状态收尾。
func (m *Manager) Cancel(ctx context.Context, taskID string) (*task.CheckTask, bool, error) {
	ok := m.runner.Cancel(taskID)
	current, err := m.repo.FindTask(ctx, taskID)
	if err != nil {
		return nil, false, err
	}
	return current, ok, nil
}
