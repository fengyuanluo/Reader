package checkworker

import (
	"context"
	"sync"
	"time"

	checkapp "reader/internal/app/check"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

type Runner struct {
	repo        task.CheckRepository
	checker     *checkapp.Checker
	diagnostics *checkapp.DiagnosticsUpdater
	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
}

func NewRunner(repo task.CheckRepository, checker *checkapp.Checker) *Runner {
	return &Runner{repo: repo, checker: checker, cancels: map[string]context.CancelFunc{}}
}

func (r *Runner) WithDiagnostics(updater *checkapp.DiagnosticsUpdater) *Runner {
	r.diagnostics = updater
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
	r.mu.Lock()
	r.cancels[checkTask.TaskID] = cancel
	r.mu.Unlock()
	go r.run(runCtx, checkTask, sources, opts, concurrency)
	return nil
}

func (r *Runner) Cancel(taskID string) bool {
	r.mu.Lock()
	cancel := r.cancels[taskID]
	if cancel != nil {
		delete(r.cancels, taskID)
	}
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *Runner) run(ctx context.Context, checkTask *task.CheckTask, sources []*source.BookSource, opts checkapp.Options, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	finished := 0
	hasFailure := false
	persistFailure := false
	for _, src := range sources {
		if ctx.Err() != nil {
			break
		}
		src := src
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item, _ := r.checker.CheckSource(ctx, src, opts)
			item.TaskID = checkTask.TaskID
			item.UpdatedAt = time.Now().UTC()
			if item.CreatedAt.IsZero() {
				item.CreatedAt = item.UpdatedAt
			}
			if ctx.Err() != nil && item.Status != task.CheckItemFailed {
				item.Status = task.CheckItemCancelled
				item.Message = ctx.Err().Error()
			}
			if err := r.repo.SaveItem(context.Background(), item); err != nil {
				mu.Lock()
				persistFailure = true
				mu.Unlock()
				return
			}
			mu.Lock()
			if item.Status == task.CheckItemFailed {
				hasFailure = true
			}
			finished++
			currentFinished := finished
			mu.Unlock()
			if err := r.repo.UpdateTaskProgress(context.Background(), checkTask.TaskID, currentFinished); err != nil {
				mu.Lock()
				persistFailure = true
				mu.Unlock()
			}
			if r.diagnostics != nil {
				if err := r.diagnostics.Apply(context.Background(), item); err != nil {
					mu.Lock()
					persistFailure = true
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	delete(r.cancels, checkTask.TaskID)
	r.mu.Unlock()
	completedAt := time.Now().UTC()
	mu.Lock()
	finalFinished := finished
	finalHasFailure := hasFailure
	finalPersistFailure := persistFailure
	mu.Unlock()
	status := task.CheckTaskSucceeded
	if ctx.Err() != nil {
		status = task.CheckTaskCancelled
	} else if finalPersistFailure || finalHasFailure {
		status = task.CheckTaskFailed
	}
	_ = r.repo.UpdateTaskStatus(context.Background(), checkTask.TaskID, status, finalFinished, &completedAt)
}
