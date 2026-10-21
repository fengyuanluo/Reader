package checkworker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	checkapp "reader/internal/app/check"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

// runHandle 代表一次任务运行；同一 taskID 重跑时，旧运行的清理不能删除新运行的句柄。
type runHandle struct {
	cancel context.CancelFunc
}

// SourceLoader 按 sourceId 加载单个书源；校验并发槽只加载正在校验的源，用完即释放（S00-02）。
type SourceLoader interface {
	FindByID(ctx context.Context, id string) (*source.BookSource, error)
}

type Runner struct {
	repo        task.CheckRepository
	loader      SourceLoader
	checker     *checkapp.Checker
	diagnostics *checkapp.DiagnosticsUpdater
	events      *checkapp.Publisher
	logger      *slog.Logger
	mu          sync.Mutex
	cancels     map[string]*runHandle
}

func NewRunner(repo task.CheckRepository, checker *checkapp.Checker) *Runner {
	return &Runner{repo: repo, checker: checker, logger: slog.Default(), cancels: map[string]*runHandle{}}
}

// WithSourceLoader 注入按 sourceId 惰性加载书源的仓储；未注入时回退到 repo 本身（若实现了 SourceLoader）。
func (r *Runner) WithSourceLoader(loader SourceLoader) *Runner {
	r.loader = loader
	return r
}

// WithLogger 注入结构化日志；持久化失败必须留痕，否则排障只能靠猜（S03-08）。
func (r *Runner) WithLogger(logger *slog.Logger) *Runner {
	if logger != nil {
		r.logger = logger
	}
	return r
}

// WithDiagnostics 保留给既有装配：诊断更新器仍由 runner 持有。
func (r *Runner) WithDiagnostics(updater *checkapp.DiagnosticsUpdater) *Runner {
	r.diagnostics = updater
	return r
}

func (r *Runner) WithEvents(publisher *checkapp.Publisher) *Runner {
	r.events = publisher
	return r
}

// Start 创建并启动校验任务。sources 只用于读取 ID 元数据：runner 内部只保留 sourceID 列表，
// 每个源在 worker 拿到并发槽后再通过 loader 惰性加载（S00-02），避免整个任务生命周期
// 持有全部完整书源（规则 + RawJSON）。
func (r *Runner) Start(ctx context.Context, checkTask *task.CheckTask, sourceIDs []string, opts checkapp.Options, concurrency int) error {
	if concurrency < 1 {
		concurrency = 1
	}
	if checkTask.Status == "" {
		checkTask.Status = task.CheckTaskPending
	}
	if checkTask.CreatedAt.IsZero() {
		checkTask.CreatedAt = time.Now().UTC()
	}
	checkTask.Total = len(sourceIDs)
	if err := r.repo.CreateTask(ctx, checkTask); err != nil {
		return err
	}
	// 状态推进失败时任务会永久停在 pending 且没有取消句柄（S03-14），
	// 重试一次后仍未成功则把任务收敛到可取消/可再跑的终态。
	if err := r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, task.CheckTaskRunning, 0, nil); err != nil {
		r.logger.Error("check task start: mark running failed, retrying", "taskId", checkTask.TaskID, "err", err)
		if err := r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, task.CheckTaskRunning, 0, nil); err != nil {
			r.logger.Error("check task start: mark running failed twice, cancelling task", "taskId", checkTask.TaskID, "err", err)
			_ = r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, task.CheckTaskCancelled, 0, new(time.Now().UTC()))
			return err
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	handle := &runHandle{cancel: cancel}
	r.mu.Lock()
	r.cancels[checkTask.TaskID] = handle
	r.mu.Unlock()
	// 先建立事件流，订阅者才能在任务执行前连上并收到 check.started。
	if r.events != nil {
		r.events.Open(checkTask.TaskID)
	}
	go r.run(runCtx, handle, checkTask, sourceIDs, opts, concurrency)
	return nil
}

func (r *Runner) Cancel(taskID string) bool {
	r.mu.Lock()
	handle := r.cancels[taskID]
	if handle != nil {
		delete(r.cancels, taskID)
	}
	r.mu.Unlock()
	if handle == nil {
		return false
	}
	handle.cancel()
	return true
}

// loadSource 按 sourceId 加载单个书源；加载失败返回携带失败信息的条目。
func (r *Runner) loadSource(ctx context.Context, checkTask *task.CheckTask, sourceID string) (*source.BookSource, *task.CheckTaskItem) {
	loader := r.loader
	if loader == nil {
		if l, ok := r.repo.(SourceLoader); ok {
			loader = l
		}
	}
	if loader == nil {
		// 没有可用的书源加载器：无法校验，按失败条目返回而不是 panic。
		now := time.Now().UTC()
		return nil, &task.CheckTaskItem{
			TaskID:      checkTask.TaskID,
			SourceID:    sourceID,
			Status:      task.CheckItemFailed,
			FailureCode: "source_not_found",
			Message:     "书源加载器不可用",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	src, err := loader.FindByID(ctx, sourceID)
	if err != nil {
		now := time.Now().UTC()
		return nil, &task.CheckTaskItem{
			TaskID:      checkTask.TaskID,
			SourceID:    sourceID,
			Status:      task.CheckItemFailed,
			FailureCode: "source_not_found",
			Message:     "加载书源失败: " + err.Error(),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	return src, nil
}

func (r *Runner) run(ctx context.Context, handle *runHandle, checkTask *task.CheckTask, sourceIDs []string, opts checkapp.Options, concurrency int) {
	r.publish(&task.CheckEvent{TaskID: checkTask.TaskID, Type: task.CheckEventStarted, Message: "check started", TraceID: checkTask.TraceID})
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	finished := 0
	hasFailure := false
	persistFailure := false
	// 先占并发槽再建 goroutine：同时在跑的 goroutine 数不超过 concurrency，
	// 也保证取消后剩余条目仍处于“未执行”状态，可以被统一记为 cancelled。
	started := 0
	for ; started < len(sourceIDs); started++ {
		if !acquire(ctx, sem) {
			break
		}
		sourceID := sourceIDs[started]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.publish(&task.CheckEvent{TaskID: checkTask.TaskID, SourceID: sourceID, Type: task.CheckEventItemStarted, TraceID: checkTask.TraceID})
			// 拿到并发槽后才加载书源，用完即释放（S00-02）。
			src, loadErr := r.loadSource(context.Background(), checkTask, sourceID)
			var item *task.CheckTaskItem
			if loadErr != nil {
				item = loadErr
			} else {
				item, _ = r.checker.CheckSource(ctx, src, opts)
			}
			item.TaskID = checkTask.TaskID
			item.UpdatedAt = time.Now().UTC()
			if item.CreatedAt.IsZero() {
				item.CreatedAt = item.UpdatedAt
			}
			if ctx.Err() != nil {
				// 取消导致的失败不是真的校验失败，统一按状态机语义记为 cancelled。
				item.Status = task.CheckItemCancelled
				item.FailureCode = ""
				item.FailureClass = ""
				item.Message = ctx.Err().Error()
			}
			mu.Lock()
			if item.Status == task.CheckItemFailed {
				hasFailure = true
			}
			finished++
			progress := finished
			mu.Unlock()
			// finished 只增不减，进度在 SaveItem 同一事务内推进（S03-02）。
			if err := r.repo.SaveItemWithProgress(context.Background(), item, progress); err != nil {
				r.logger.Error("check task persist item failed", "taskId", checkTask.TaskID, "sourceId", sourceID, "err", err)
				time.Sleep(time.Millisecond)
				if err := r.repo.SaveItemWithProgress(context.Background(), item, progress); err != nil {
					r.logger.Error("check task persist item failed after retry", "taskId", checkTask.TaskID, "sourceId", sourceID, "err", err)
					mu.Lock()
					persistFailure = true
					finished--
					mu.Unlock()
					// 保存失败也要发终态事件，订阅者的事件流才能收敛（S03-07③）。
					r.publishItemSummary(checkTask, sourceID, task.CheckItemFailed, "条目持久化失败: "+err.Error())
					return
				}
			}
			if r.diagnostics != nil {
				if err := r.diagnostics.Apply(context.Background(), item); err != nil {
					r.logger.Error("check task apply diagnostics failed", "taskId", checkTask.TaskID, "sourceId", sourceID, "err", err)
				}
			}
			r.publishItem(checkTask, item)
		}()
	}
	wg.Wait()
	// 取消后排队未执行的条目也落成 cancelled，任务条目状态与任务终态保持一致；
	// 批量落库避免数百条串行写拖长收尾（S03-11）。
	queued := make([]*task.CheckTaskItem, 0, len(sourceIDs)-started)
	for _, sourceID := range sourceIDs[started:] {
		queued = append(queued, cancelledItem(checkTask, sourceID, ctx.Err()))
	}
	if err := r.repo.SaveItems(context.Background(), queued); err != nil {
		r.logger.Error("check task persist queued items failed", "taskId", checkTask.TaskID, "err", err)
		persistFailure = true
	} else {
		finished += len(queued)
	}
	r.mu.Lock()
	// 只有仍属于本次运行的句柄才删除，避免误删同 ID 新运行的 cancel。
	if r.cancels[checkTask.TaskID] == handle {
		delete(r.cancels, checkTask.TaskID)
	}
	r.mu.Unlock()
	completedAt := time.Now().UTC()
	status := task.CheckTaskSucceeded
	if ctx.Err() != nil {
		status = task.CheckTaskCancelled
	} else if persistFailure || hasFailure {
		status = task.CheckTaskFailed
	}
	// 终态统一由 runner 收尾路径写入（S03-05）：取消请求不直接改库，避免与终态写竞争。
	if err := r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, status, finished, &completedAt); err != nil {
		r.logger.Error("check task finalize failed", "taskId", checkTask.TaskID, "status", string(status), "err", err)
	}
	r.publish(&task.CheckEvent{TaskID: checkTask.TaskID, Type: terminalEventType(status), Message: string(status), TraceID: checkTask.TraceID})
}

// acquire 占用一个并发槽；任务被取消时返回 false，调用方停止为剩余条目创建 goroutine。
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func cancelledItem(checkTask *task.CheckTask, sourceID string, cause error) *task.CheckTaskItem {
	now := time.Now().UTC()
	message := "check cancelled"
	if cause != nil {
		message = cause.Error()
	}
	item := &task.CheckTaskItem{
		TaskID:    checkTask.TaskID,
		SourceID:  sourceID,
		Status:    task.CheckItemCancelled,
		Message:   message,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return item
}

func terminalEventType(status task.CheckTaskStatus) task.CheckEventType {
	if status == task.CheckTaskCancelled {
		return task.CheckEventCancelled
	}
	return task.CheckEventCompleted
}

// publishItem 发布条目终态事件：事件只携带摘要（sourceID/status/message），
// 完整结果由 DB 承载，避免大任务下每个事件流占用数 MB 内存（S03-10）。
func (r *Runner) publishItem(checkTask *task.CheckTask, item *task.CheckTaskItem) {
	r.publishItemSummary(checkTask, item.SourceID, item.Status, item.Message)
}

func (r *Runner) publishItemSummary(checkTask *task.CheckTask, sourceID string, status task.CheckTaskItemStatus, message string) {
	eventType := task.CheckEventItemPassed
	if status == task.CheckItemFailed {
		eventType = task.CheckEventItemFailed
	}
	r.publish(&task.CheckEvent{
		TaskID:   checkTask.TaskID,
		SourceID: sourceID,
		Type:     eventType,
		Message:  message,
		TraceID:  checkTask.TraceID,
	})
}

func (r *Runner) publish(event *task.CheckEvent) {
	if r.events == nil {
		return
	}
	r.events.Publish(event)
}
