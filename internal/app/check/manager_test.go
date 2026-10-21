package checkapp

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
)

// TestManagerAppliesDefaultConcurrency 覆盖未注入配置时的并发上限回退（R-026）。
func TestManagerAppliesDefaultConcurrency(t *testing.T) {
	runner := &recordingRunner{repo: newMemoryCheckRepository()}
	manager := NewManager(runner.repo, &stubSourceRepository{}, runner)
	checkTask := &task.CheckTask{TaskID: "task-1"}

	if err := manager.Start(context.Background(), checkTask, []string{"source-1"}, Options{}, 0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if runner.concurrency != DefaultConcurrency {
		t.Fatalf("concurrency = %d, want default %d", runner.concurrency, DefaultConcurrency)
	}
}

// TestManagerPassesInjectedConcurrency 覆盖装配方注入的并发上限（来自 config.SourceConcurrency）。
func TestManagerPassesInjectedConcurrency(t *testing.T) {
	runner := &recordingRunner{repo: newMemoryCheckRepository()}
	manager := NewManager(runner.repo, &stubSourceRepository{}, runner)
	checkTask := &task.CheckTask{TaskID: "task-1"}

	if err := manager.Start(context.Background(), checkTask, []string{"source-1"}, Options{}, 7); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if runner.concurrency != 7 {
		t.Fatalf("concurrency = %d, want 7", runner.concurrency)
	}
	if len(runner.sourceIDs) != 1 {
		t.Fatalf("sourceIDs = %d, want 1", len(runner.sourceIDs))
	}
}

// TestManagerStartOnlyVerifiesExistence 覆盖 S00-02：Start 不再逐个 FindByID 解析全部书源，
// 只做存在性校验；runner 只收到 sourceID 列表。
func TestManagerStartOnlyVerifiesExistence(t *testing.T) {
	sourceRepo := &countingSourceRepository{BookSourceRepository: &stubSourceRepository{}}
	runner := &recordingRunner{repo: newMemoryCheckRepository()}
	manager := NewManager(runner.repo, sourceRepo, runner)
	checkTask := &task.CheckTask{TaskID: "task-1"}

	if err := manager.Start(context.Background(), checkTask, []string{"a", "b"}, Options{}, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if sourceRepo.findByIDCalls != 0 {
		t.Fatalf("FindByID calls = %d, want 0 (Start must not load full sources)", sourceRepo.findByIDCalls)
	}
	if len(runner.sourceIDs) != 2 || runner.sourceIDs[0] != "a" || runner.sourceIDs[1] != "b" {
		t.Fatalf("runner sourceIDs = %#v", runner.sourceIDs)
	}
}

// TestManagerRejectsUnknownSource 覆盖存在性校验：sourceId 不存在返回 source.ErrNotFound。
// memory 仓储没有书源表，用真实 SQLite 仓储验证。
func TestManagerRejectsUnknownSource(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	checkRepo := repositories.NewCheckRepository(db)
	sourceRepo := repositories.NewBookSourceRepository(db)
	manager := NewManager(checkRepo, sourceRepo, &recordingRunner{repo: checkRepo})
	err = manager.Start(context.Background(), &task.CheckTask{TaskID: "task-1"}, []string{"missing"}, Options{}, 1)
	if !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("Start() error = %v, want source.ErrNotFound", err)
	}
}

// TestManagerRejectsDuplicateRun 覆盖 S03-04 防重入：已有未完成任务时再次 Start 返回
// task.ErrCheckTaskRunning（对齐 Legado“已有书源在校验”）。
func TestManagerRejectsDuplicateRun(t *testing.T) {
	repo := newMemoryCheckRepository()
	repo.tasks["running-task"] = &task.CheckTask{TaskID: "running-task", Status: task.CheckTaskRunning}
	manager := NewManager(repo, &stubSourceRepository{}, &recordingRunner{repo: repo})
	err := manager.Start(context.Background(), &task.CheckTask{TaskID: "task-2"}, []string{"a"}, Options{}, 1)
	if !errors.Is(err, task.ErrCheckTaskRunning) {
		t.Fatalf("Start() error = %v, want task.ErrCheckTaskRunning", err)
	}
}

// TestManagerStartDetachedFromRequestContext 覆盖 S03-01：请求 ctx 取消后任务仍继续，
// Start 返回后任务照常执行到终态。
func TestManagerStartDetachedFromRequestContext(t *testing.T) {
	repo := newMemoryCheckRepository()
	runner := &recordingRunner{repo: repo, done: make(chan struct{})}
	manager := NewManager(repo, &stubSourceRepository{}, runner)
	checkTask := &task.CheckTask{TaskID: "task-1"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 请求已取消
	if err := manager.Start(ctx, checkTask, []string{"source-1"}, Options{}, 1); err != nil {
		t.Fatalf("Start() with cancelled ctx error = %v", err)
	}
	select {
	case <-runner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner.Start never called; request ctx cancellation must not abort the task")
	}
}

// TestManagerCancelDoesNotWriteTerminalState 覆盖 S03-05：Cancel 只置取消函数，
// 不直接把任务标 cancelled；终态由 runner 收尾路径写入。
func TestManagerCancelDoesNotWriteTerminalState(t *testing.T) {
	repo := newMemoryCheckRepository()
	repo.tasks["task-1"] = &task.CheckTask{TaskID: "task-1", Status: task.CheckTaskRunning, Total: 1}
	runner := &recordingRunner{repo: repo}
	manager := NewManager(repo, &stubSourceRepository{}, runner)
	runner.onCancel = func() bool { return true }

	if _, ok, err := manager.Cancel(context.Background(), "task-1"); err != nil || !ok {
		t.Fatalf("Cancel() = %v, ok=%v", err, ok)
	}
	if repo.tasks["task-1"].Status != task.CheckTaskRunning {
		t.Fatalf("status after Cancel = %s, want running (terminal state written by runner)", repo.tasks["task-1"].Status)
	}
	if !runner.cancelled {
		t.Fatal("runner.Cancel not called")
	}
}

type recordingRunner struct {
	repo        task.CheckRepository
	concurrency int
	sourceIDs   []string
	cancelled   bool
	onCancel    func() bool
	done        chan struct{}
}

func (r *recordingRunner) Start(ctx context.Context, checkTask *task.CheckTask, sourceIDs []string, _ Options, concurrency int) error {
	r.concurrency = concurrency
	r.sourceIDs = sourceIDs
	checkTask.Total = len(sourceIDs)
	if checkTask.Status == "" {
		checkTask.Status = task.CheckTaskPending
	}
	if checkTask.CreatedAt.IsZero() {
		checkTask.CreatedAt = time.Now().UTC()
	}
	if err := r.repo.CreateTask(ctx, checkTask); err != nil {
		return err
	}
	if r.done != nil {
		close(r.done)
	}
	return nil
}

func (r *recordingRunner) Cancel(string) bool {
	r.cancelled = true
	if r.onCancel != nil {
		return r.onCancel()
	}
	return false
}

// countingSourceRepository 统计 FindByID 调用次数，用于断言 Start 不再加载完整书源。
type countingSourceRepository struct {
	BookSourceRepository source.BookSourceRepository
	findByIDCalls        int
}

func (c *countingSourceRepository) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	c.findByIDCalls++
	return c.BookSourceRepository.FindByID(ctx, id)
}

func (c *countingSourceRepository) Save(ctx context.Context, src *source.BookSource) error {
	return c.BookSourceRepository.Save(ctx, src)
}

func (c *countingSourceRepository) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	return c.BookSourceRepository.SaveMany(ctx, sources)
}

func (c *countingSourceRepository) FindByURL(ctx context.Context, url string) (*source.BookSource, error) {
	return c.BookSourceRepository.FindByURL(ctx, url)
}

func (c *countingSourceRepository) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	return c.BookSourceRepository.List(ctx, filter)
}

func (c *countingSourceRepository) Delete(ctx context.Context, id string) error {
	return c.BookSourceRepository.Delete(ctx, id)
}

func (c *countingSourceRepository) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return c.BookSourceRepository.UpdateEnabled(ctx, id, enabled)
}

func (c *countingSourceRepository) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return c.BookSourceRepository.UpdateExploreEnabled(ctx, id, enabled)
}

func (c *countingSourceRepository) Reorder(ctx context.Context, orders []source.SourceOrder) error {
	return c.BookSourceRepository.Reorder(ctx, orders)
}

type stubSourceRepository struct{}

func (s *stubSourceRepository) FindByID(_ context.Context, id string) (*source.BookSource, error) {
	return &source.BookSource{SourceID: id, BookSourceURL: "https://" + id + ".test"}, nil
}

func (s *stubSourceRepository) Save(context.Context, *source.BookSource) error { return nil }

func (s *stubSourceRepository) SaveMany(context.Context, []*source.BookSource) error { return nil }

func (s *stubSourceRepository) FindByURL(_ context.Context, url string) (*source.BookSource, error) {
	return &source.BookSource{BookSourceURL: url}, nil
}

func (s *stubSourceRepository) List(context.Context, source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	return source.Page[*source.BookSource]{}, nil
}

type memoryCheckRepository struct {
	tasks map[string]*task.CheckTask
}

func newMemoryCheckRepository() *memoryCheckRepository {
	return &memoryCheckRepository{tasks: map[string]*task.CheckTask{}}
}

func (r *memoryCheckRepository) CreateTask(_ context.Context, checkTask *task.CheckTask) error {
	r.tasks[checkTask.TaskID] = checkTask
	return nil
}

func (r *memoryCheckRepository) FindTask(_ context.Context, taskID string) (*task.CheckTask, error) {
	return r.tasks[taskID], nil
}

func (r *memoryCheckRepository) UpdateTaskStatus(context.Context, string, task.CheckTaskStatus, int, *time.Time) error {
	return nil
}

func (r *memoryCheckRepository) UpdateTaskProgress(context.Context, string, int) error { return nil }

func (r *memoryCheckRepository) SaveItem(context.Context, *task.CheckTaskItem) error { return nil }

func (r *memoryCheckRepository) SaveItemWithProgress(context.Context, *task.CheckTaskItem, int) error {
	return nil
}

func (r *memoryCheckRepository) SaveItems(context.Context, []*task.CheckTaskItem) error { return nil }

func (r *memoryCheckRepository) ListItemsPage(context.Context, string, *task.CheckTaskItemStatus, int, int) ([]*task.CheckTaskItem, int, error) {
	return nil, 0, nil
}

func (r *memoryCheckRepository) ListItems(context.Context, string) ([]*task.CheckTaskItem, error) {
	return nil, nil
}

func (r *memoryCheckRepository) HasUnfinishedTasks(context.Context) (bool, error) {
	for _, t := range r.tasks {
		if t.Status == task.CheckTaskPending || t.Status == task.CheckTaskRunning {
			return true, nil
		}
	}
	return false, nil
}

func (r *memoryCheckRepository) FindMissingSourceIDs(_ context.Context, sourceIDs []string) ([]string, error) {
	// source_id 出现在任务表即视为存在（memory 仓储没有独立的书源表）。
	missing := []string{}
	return missing, nil
}

func (r *memoryCheckRepository) FailInterruptedTasks(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s *stubSourceRepository) Delete(context.Context, string) error { return nil }

func (s *stubSourceRepository) UpdateEnabled(context.Context, string, bool) error { return nil }

func (s *stubSourceRepository) UpdateExploreEnabled(context.Context, string, bool) error {
	return nil
}

func (s *stubSourceRepository) Reorder(context.Context, []source.SourceOrder) error { return nil }
