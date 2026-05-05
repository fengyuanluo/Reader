package checkworker

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"

	_ "modernc.org/sqlite"
)

func TestRunnerRunsSourcesConcurrentlyAndPersistsProgress(t *testing.T) {
	repo := newFakeCheckRepo()
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := NewRunner(repo, checker)
	sources := []*source.BookSource{runnerSource("s1"), runnerSource("s2")}
	checkTask := &task.CheckTask{TaskID: "task-1", TraceID: "trace-1"}

	if err := runner.Start(context.Background(), checkTask, sources, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskSucceeded)
	got, _ := repo.FindTask(context.Background(), checkTask.TaskID)
	if got.Finished != 2 || len(got.Items) != 2 {
		t.Fatalf("task = %#v", got)
	}
}

func TestRunnerAppliesSourceDiagnostics(t *testing.T) {
	repo := newFakeCheckRepo()
	sourceRepo := &runnerSourceRepo{sources: map[string]*source.BookSource{"s1": runnerSource("s1")}}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &failingRunnerContent{})
	runner := NewRunner(repo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(sourceRepo))
	checkTask := &task.CheckTask{TaskID: "task-diagnostics"}

	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{sourceRepo.sources["s1"]}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskFailed)
	if sourceRepo.sources["s1"].BookSourceGroup != "正文失效" {
		t.Fatalf("source = %#v", sourceRepo.sources["s1"])
	}
}

func TestRunnerCancelMarksTaskCancelled(t *testing.T) {
	repo := newFakeCheckRepo()
	content := &blockingRunnerContent{started: make(chan struct{}), released: make(chan struct{})}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, content)
	runner := NewRunner(repo, checker)
	checkTask := &task.CheckTask{TaskID: "task-cancel"}
	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{runnerSource("s1")}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-content.started:
	case <-time.After(time.Second):
		t.Fatalf("content did not start")
	}
	if !runner.Cancel(checkTask.TaskID) {
		t.Fatalf("Cancel() returned false")
	}
	select {
	case <-content.released:
	case <-time.After(time.Second):
		t.Fatalf("content did not release")
	}
	repo.waitForStatus(t, task.CheckTaskCancelled)
}

func TestRunnerFailsTaskWhenPersistingItemsFails(t *testing.T) {
	repo := newFakeCheckRepo()
	repo.saveItemErr = errors.New("save item failed")
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := NewRunner(repo, checker)
	checkTask := &task.CheckTask{TaskID: "task-save-error", TraceID: "trace-1"}

	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{runnerSource("s1")}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskFailed)
	got, _ := repo.FindTask(context.Background(), checkTask.TaskID)
	if got.Finished != 0 {
		t.Fatalf("finished = %d, want 0 when item persistence fails", got.Finished)
	}
	if len(got.Items) != 0 {
		t.Fatalf("items = %#v, want none persisted", got.Items)
	}
}

func TestRunnerPersistsItemsBeforeDiagnosticsFailure(t *testing.T) {
	repo := newFakeCheckRepo()
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := NewRunner(repo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(&failingRunnerSourceRepo{err: errors.New("save source failed")}))
	checkTask := &task.CheckTask{TaskID: "task-diagnostics-fail"}

	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{runnerSource("s1")}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskFailed)
	got, _ := repo.FindTask(context.Background(), checkTask.TaskID)
	if len(got.Items) != 1 {
		t.Fatalf("items = %#v, want 1 persisted item before diagnostics failure", got.Items)
	}
}

func TestRunnerPersistsAllItemsWithRealSQLiteRepository(t *testing.T) {
	repo := repositories.NewCheckRepository(openRunnerRepoTestDB(t))
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := NewRunner(repo, checker)
	sources := []*source.BookSource{
		runnerSource("s1"),
		runnerSource("s2"),
		runnerSource("s3"),
		runnerSource("s4"),
		runnerSource("s5"),
		runnerSource("s6"),
	}
	checkTask := &task.CheckTask{TaskID: "task-real-sqlite"}

	if err := runner.Start(context.Background(), checkTask, sources, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 4); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := repo.FindTask(context.Background(), checkTask.TaskID)
		if err != nil {
			t.Fatalf("FindTask() error = %v", err)
		}
		if got.Status == task.CheckTaskSucceeded || got.Status == task.CheckTaskFailed || got.Status == task.CheckTaskCancelled {
			if got.Finished != len(sources) {
				t.Fatalf("finished = %d, want %d", got.Finished, len(sources))
			}
			if len(got.Items) != len(sources) {
				t.Fatalf("items = %d, want %d", len(got.Items), len(sources))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for real sqlite runner completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runnerSource(id string) *source.BookSource {
	return &source.BookSource{SourceID: id, BookSourceURL: "https://" + id + ".test", BookSourceName: id}
}

type runnerSearch struct{}

func (runnerSearch) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	return searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}, nil
}

type runnerDiscovery struct{}

func (runnerDiscovery) Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error) {
	return discoveryapp.Result{}, nil
}

type runnerBooks struct{}

func (runnerBooks) Save(ctx context.Context, item *book.Book) (*book.Book, error) {
	return item, nil
}

func (runnerBooks) RefreshInfo(ctx context.Context, id string) (book.Book, error) {
	return book.Book{BookURL: "https://book.test/1", Name: "斗破苍穹", Author: "天蚕土豆"}, nil
}

func (runnerBooks) RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error) {
	return []*book.BookChapter{{BookURL: "https://book.test/1", URL: "https://book.test/c/1", Title: "第一章", Index: 0}}, &book.Book{BookURL: "https://book.test/1"}, nil
}

type runnerContent struct{}

func (runnerContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	return contentapp.Result{Content: "正文", Chapter: book.BookChapter{Index: index}}, &book.Book{}, nil
}

type failingRunnerContent struct{}

func (failingRunnerContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	return contentapp.Result{}, nil, contentapp.ErrContentEmpty
}

type blockingRunnerContent struct {
	started  chan struct{}
	released chan struct{}
}

func (c *blockingRunnerContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	close(c.started)
	<-ctx.Done()
	close(c.released)
	return contentapp.Result{}, nil, ctx.Err()
}

type fakeCheckRepo struct {
	mu          sync.Mutex
	cond        *sync.Cond
	tasks       map[string]*task.CheckTask
	items       map[string][]*task.CheckTaskItem
	saveItemErr error
}

type runnerSourceRepo struct {
	sources map[string]*source.BookSource
}

type failingRunnerSourceRepo struct {
	err error
}

func (r *runnerSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	copy := *r.sources[id]
	return &copy, nil
}

func (r *runnerSourceRepo) Save(ctx context.Context, src *source.BookSource) error {
	copy := *src
	r.sources[src.SourceID] = &copy
	return nil
}

func (r *failingRunnerSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	return &source.BookSource{SourceID: id, BookSourceURL: "https://" + id + ".test", BookSourceName: id}, nil
}

func (r *failingRunnerSourceRepo) Save(ctx context.Context, src *source.BookSource) error {
	return r.err
}

func newFakeCheckRepo() *fakeCheckRepo {
	repo := &fakeCheckRepo{tasks: map[string]*task.CheckTask{}, items: map[string][]*task.CheckTaskItem{}}
	repo.cond = sync.NewCond(&repo.mu)
	return repo
}

func (r *fakeCheckRepo) CreateTask(ctx context.Context, item *task.CheckTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *item
	r.tasks[item.TaskID] = &copy
	r.cond.Broadcast()
	return nil
}

func (r *fakeCheckRepo) FindTask(ctx context.Context, taskID string) (*task.CheckTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *r.tasks[taskID]
	copy.Items = append([]*task.CheckTaskItem(nil), r.items[taskID]...)
	return &copy, nil
}

func (r *fakeCheckRepo) UpdateTaskStatus(ctx context.Context, taskID string, status task.CheckTaskStatus, finished int, completedAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.tasks[taskID]
	item.Status = status
	item.Finished = finished
	item.CompletedAt = completedAt
	r.cond.Broadcast()
	return nil
}

func (r *fakeCheckRepo) SaveItem(ctx context.Context, item *task.CheckTaskItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveItemErr != nil {
		return r.saveItemErr
	}
	copy := *item
	r.items[item.TaskID] = append(r.items[item.TaskID], &copy)
	r.cond.Broadcast()
	return nil
}

func (r *fakeCheckRepo) ListItems(ctx context.Context, taskID string) ([]*task.CheckTaskItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*task.CheckTaskItem(nil), r.items[taskID]...), nil
}

func (r *fakeCheckRepo) waitForStatus(t *testing.T, status task.CheckTaskStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		for _, item := range r.tasks {
			if item.Status == status {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for status %s; tasks=%#v", status, r.tasks)
		}
		r.cond.Wait()
	}
}

func openRunnerRepoTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
