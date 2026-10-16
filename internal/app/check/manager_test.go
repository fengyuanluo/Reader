package checkapp

import (
	"context"
	"testing"
	"time"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

// TestManagerAppliesDefaultConcurrency 覆盖未注入配置时的并发上限回退（R-026）。
func TestManagerAppliesDefaultConcurrency(t *testing.T) {
	runner := &recordingRunner{repo: &memoryCheckRepository{}}
	manager := NewManager(runner.repo, &stubSourceRepository{}, runner)
	checkTask := &task.CheckTask{TaskID: "task-1"}

	if _, err := manager.Start(context.Background(), checkTask, []string{"source-1"}, Options{}, 0); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if runner.concurrency != DefaultConcurrency {
		t.Fatalf("concurrency = %d, want default %d", runner.concurrency, DefaultConcurrency)
	}
}

// TestManagerPassesInjectedConcurrency 覆盖装配方注入的并发上限（来自 config.SourceConcurrency）。
func TestManagerPassesInjectedConcurrency(t *testing.T) {
	runner := &recordingRunner{repo: &memoryCheckRepository{}}
	manager := NewManager(runner.repo, &stubSourceRepository{}, runner)
	checkTask := &task.CheckTask{TaskID: "task-1"}

	if _, err := manager.Start(context.Background(), checkTask, []string{"source-1"}, Options{}, 7); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if runner.concurrency != 7 {
		t.Fatalf("concurrency = %d, want 7", runner.concurrency)
	}
	if runner.sources != 1 {
		t.Fatalf("sources = %d, want 1", runner.sources)
	}
}

type recordingRunner struct {
	repo        task.CheckRepository
	concurrency int
	sources     int
}

func (r *recordingRunner) Start(ctx context.Context, checkTask *task.CheckTask, sources []*source.BookSource, _ Options, concurrency int) error {
	r.concurrency = concurrency
	r.sources = len(sources)
	checkTask.Total = len(sources)
	if checkTask.Status == "" {
		checkTask.Status = task.CheckTaskPending
	}
	if checkTask.CreatedAt.IsZero() {
		checkTask.CreatedAt = time.Now().UTC()
	}
	return r.repo.CreateTask(ctx, checkTask)
}

func (r *recordingRunner) Cancel(string) bool { return false }

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

func (s *stubSourceRepository) Delete(context.Context, string) error { return nil }

func (s *stubSourceRepository) UpdateEnabled(context.Context, string, bool) error { return nil }

func (s *stubSourceRepository) UpdateExploreEnabled(context.Context, string, bool) error {
	return nil
}

func (s *stubSourceRepository) Reorder(context.Context, []source.SourceOrder) error { return nil }

type memoryCheckRepository struct {
	tasks map[string]*task.CheckTask
}

func (r *memoryCheckRepository) CreateTask(_ context.Context, checkTask *task.CheckTask) error {
	if r.tasks == nil {
		r.tasks = map[string]*task.CheckTask{}
	}
	r.tasks[checkTask.TaskID] = checkTask
	return nil
}

func (r *memoryCheckRepository) FindTask(_ context.Context, taskID string) (*task.CheckTask, error) {
	return r.tasks[taskID], nil
}

func (r *memoryCheckRepository) UpdateTaskStatus(context.Context, string, task.CheckTaskStatus, int, *time.Time) error {
	return nil
}

func (r *memoryCheckRepository) SaveItem(context.Context, *task.CheckTaskItem) error { return nil }

func (r *memoryCheckRepository) ListItems(context.Context, string) ([]*task.CheckTaskItem, error) {
	return nil, nil
}

func (r *memoryCheckRepository) FailInterruptedTasks(context.Context, time.Time) (int64, error) {
	return 0, nil
}
