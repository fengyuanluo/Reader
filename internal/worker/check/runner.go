package checkworker

import (
	"context"
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

type Runner struct {
	repo        task.CheckRepository
	checker     *checkapp.Checker
	diagnostics *checkapp.DiagnosticsUpdater
	events      *checkapp.Publisher
	mu          sync.Mutex
	cancels     map[string]*runHandle
}

func NewRunner(repo task.CheckRepository, checker *checkapp.Checker) *Runner {
	return &Runner{repo: repo, checker: checker, cancels: map[string]*runHandle{}}
}

func (r *Runner) WithDiagnostics(updater *checkapp.DiagnosticsUpdater) *Runner {
	r.diagnostics = updater
	return r
}

func (r *Runner) WithEvents(publisher *checkapp.Publisher) *Runner {
	r.events = publisher
	return r
}

func (r *Runner) Start(ctx context.Context, checkTask *task.CheckTask, sources []*source.BookSource, opts checkapp.Options, concurrency int) error {
	if concurrency < 1 {
		concurrency = 1
	}
	if checkTask.Status == "" {
		checkTask.Status = task.CheckTaskPending
	}
	if checkTask.CreatedAt.IsZero() {
		checkTask.CreatedAt = time.Now().UTC()
	}
	checkTask.Total = len(sources)
	if err := r.repo.CreateTask(ctx, checkTask); err != nil {
		return err
	}
	if err := r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, task.CheckTaskRunning, 0, nil); err != nil {
		return err
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
	go r.run(runCtx, handle, checkTask, sources, opts, concurrency)
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

func (r *Runner) run(ctx context.Context, handle *runHandle, checkTask *task.CheckTask, sources []*source.BookSource, opts checkapp.Options, concurrency int) {
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
	for ; started < len(sources); started++ {
		if !acquire(ctx, sem) {
			break
		}
		src := sources[started]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.publish(&task.CheckEvent{TaskID: checkTask.TaskID, SourceID: src.SourceID, Type: task.CheckEventItemStarted, Message: src.BookSourceName, TraceID: checkTask.TraceID})
			item, _ := r.checker.CheckSource(ctx, src, opts)
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
			if item.Status == task.CheckItemFailed {
				mu.Lock()
				hasFailure = true
				mu.Unlock()
			}
			if err := r.repo.SaveItem(context.Background(), item); err != nil {
				mu.Lock()
				persistFailure = true
				mu.Unlock()
				return
			}
			mu.Lock()
			finished++
			mu.Unlock()
			if r.diagnostics != nil {
				if err := r.diagnostics.Apply(context.Background(), item); err != nil {
					mu.Lock()
					persistFailure = true
					mu.Unlock()
				}
			}
			r.publishItem(checkTask, item)
		}()
	}
	wg.Wait()
	// 取消后排队未执行的条目也落成 cancelled，任务条目状态与任务终态保持一致。
	for _, src := range sources[started:] {
		item := cancelledItem(checkTask, src, ctx.Err())
		if err := r.repo.SaveItem(context.Background(), item); err != nil {
			persistFailure = true
			continue
		}
		finished++
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
	_ = r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, status, finished, &completedAt)
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

func cancelledItem(checkTask *task.CheckTask, src *source.BookSource, cause error) *task.CheckTaskItem {
	now := time.Now().UTC()
	message := "check cancelled"
	if cause != nil {
		message = cause.Error()
	}
	item := &task.CheckTaskItem{
		TaskID:         checkTask.TaskID,
		SourceID:       src.SourceID,
		BookSourceURL:  src.BookSourceURL,
		BookSourceName: src.BookSourceName,
		Status:         task.CheckItemCancelled,
		Message:        message,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return item
}

func terminalEventType(status task.CheckTaskStatus) task.CheckEventType {
	if status == task.CheckTaskCancelled {
		return task.CheckEventCancelled
	}
	return task.CheckEventCompleted
}

func (r *Runner) publishItem(checkTask *task.CheckTask, item *task.CheckTaskItem) {
	eventType := task.CheckEventItemPassed
	if item.Status == task.CheckItemFailed {
		eventType = task.CheckEventItemFailed
	}
	r.publish(&task.CheckEvent{
		TaskID:   checkTask.TaskID,
		SourceID: item.SourceID,
		Type:     eventType,
		Message:  item.Message,
		Item:     item,
		TraceID:  checkTask.TraceID,
	})
}

func (r *Runner) publish(event *task.CheckEvent) {
	if r.events == nil {
		return
	}
	r.events.Publish(event)
}
