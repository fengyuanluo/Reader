package checkworker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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
	if len(repo.progressUpdates) == 0 || repo.progressUpdates[len(repo.progressUpdates)-1] != 2 {
		t.Fatalf("progress updates = %#v", repo.progressUpdates)
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
	if sourceRepo.sources["s1"].BookSourceGroup != "搜索正文失效" {
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

	waitForCheckTaskStatus(t, repo, checkTask.TaskID, func(got *task.CheckTask) error {
		if got.Finished != len(sources) {
			return fmt.Errorf("finished = %d, want %d", got.Finished, len(sources))
		}
		if len(got.Items) != len(sources) {
			return fmt.Errorf("items = %d, want %d", len(got.Items), len(sources))
		}
		return nil
	})
}

// waitForCheckTaskStatus 轮询任务直到进入终态并通过 check 断言；deadline 统一为 5s，避免固定 sleep 预算在高负载下 flaky。
func waitForCheckTaskStatus(t *testing.T, repo task.CheckRepository, taskID string, check func(*task.CheckTask) error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		got, err := repo.FindTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("FindTask() error = %v", err)
		}
		if task.CheckTaskTerminal(got.Status) {
			if check != nil {
				if err := check(got); err != nil {
					t.Fatalf("check task = %#v: %v", got, err)
				}
			}
			return
		}
		lastErr = fmt.Errorf("status = %s", got.Status)
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for terminal check task: %v", lastErr)
}

func TestRunnerCancelsQueuedItemsInsteadOfFailingThem(t *testing.T) {
	repo := newFakeCheckRepo()
	content := &blockingRunnerContent{started: make(chan struct{}), released: make(chan struct{})}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, content)
	runner := NewRunner(repo, checker)
	sources := []*source.BookSource{runnerSource("s1"), runnerSource("s2"), runnerSource("s3")}
	checkTask := &task.CheckTask{TaskID: "task-queued"}
	if err := runner.Start(context.Background(), checkTask, sources, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
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
	got, err := repo.FindTask(context.Background(), checkTask.TaskID)
	if err != nil {
		t.Fatalf("FindTask() error = %v", err)
	}
	if got.Finished != len(sources) || len(got.Items) != len(sources) {
		t.Fatalf("task = %#v, want every item accounted for", got)
	}
	for _, item := range got.Items {
		if item.Status != task.CheckItemCancelled {
			t.Fatalf("item %s = %#v, want cancelled (排队条目不能被记为 failed)", item.SourceID, item)
		}
	}
}

func TestRunnerBoundsConcurrentChecks(t *testing.T) {
	repo := newFakeCheckRepo()
	content := &concurrencyTrackingContent{}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, content)
	runner := NewRunner(repo, checker)
	sources := make([]*source.BookSource, 0, 8)
	for i := range 8 {
		sources = append(sources, runnerSource("s"+strconv.Itoa(i)))
	}
	checkTask := &task.CheckTask{TaskID: "task-bounded"}
	if err := runner.Start(context.Background(), checkTask, sources, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskSucceeded)
	if peak := content.peakLoad(); peak > 2 {
		t.Fatalf("peak concurrent checks = %d, want <= 2", peak)
	}
}

func TestRunnerPublishesCheckEvents(t *testing.T) {
	repo := newFakeCheckRepo()
	publisher := checkapp.NewPublisher()
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := NewRunner(repo, checker).WithEvents(publisher)
	checkTask := &task.CheckTask{TaskID: "task-events", TraceID: "trace-events"}
	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{runnerSource("s1")}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, ok := publisher.Subscribe(ctx, checkTask.TaskID)
	if !ok {
		t.Fatal("Subscribe() ok = false, want the task stream")
	}
	seen := map[task.CheckEventType]bool{}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event == nil {
				t.Fatalf("event stream closed before the terminal event; seen=%v", seen)
			}
			if event.TraceID != "trace-events" {
				t.Fatalf("event traceId = %q", event.TraceID)
			}
			seen[event.Type] = true
			if event.Type == task.CheckEventItemPassed && event.Item == nil {
				t.Fatalf("item event has no item payload: %#v", event)
			}
			if task.IsTerminalCheckEvent(event.Type) {
				for _, want := range []task.CheckEventType{task.CheckEventStarted, task.CheckEventItemStarted, task.CheckEventItemPassed, task.CheckEventCompleted} {
					if !seen[want] {
						t.Fatalf("missing event %s; seen=%v", want, seen)
					}
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for check events; seen=%v", seen)
		}
	}
}

func TestRunnerKeepsCancelHandleWhenDuplicateStartFails(t *testing.T) {
	repo := repositories.NewCheckRepository(openRunnerRepoTestDB(t))
	content := &blockingRunnerContent{started: make(chan struct{}), released: make(chan struct{})}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, content)
	runner := NewRunner(repo, checker)
	checkTask := &task.CheckTask{TaskID: "task-duplicate"}
	opts := checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}
	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{runnerSource("s1")}, opts, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-content.started:
	case <-time.After(time.Second):
		t.Fatalf("content did not start")
	}
	// 重复 taskId 的 Start 在注册 cancel 之前就失败，不能影响运行中任务的取消句柄。
	if err := runner.Start(context.Background(), checkTask, []*source.BookSource{runnerSource("s1")}, opts, 1); err == nil {
		t.Fatalf("duplicate Start() error = nil, want create task failure")
	}
	if !runner.Cancel(checkTask.TaskID) {
		t.Fatalf("Cancel() returned false; the running task lost its cancel handle")
	}
	select {
	case <-content.released:
	case <-time.After(time.Second):
		t.Fatalf("content did not release")
	}
}

// concurrencyTrackingContent 统计同时在跑的正文抓取数量，用于验证并发上限。
type concurrencyTrackingContent struct {
	mu      sync.Mutex
	current int
	peak    int
}

func (c *concurrencyTrackingContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	c.mu.Lock()
	c.current++
	if c.current > c.peak {
		c.peak = c.current
	}
	c.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	return contentapp.Result{Content: "正文", Chapter: chapter}, nil
}

func (c *concurrencyTrackingContent) peakLoad() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

func runnerSource(id string) *source.BookSource {
	return &source.BookSource{SourceID: id, BookSourceURL: "https://" + id + ".test", BookSourceName: id, SearchURL: "https://" + id + ".test/search"}
}

type runnerSearch struct{}

func (runnerSearch) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	return searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}, nil
}

type runnerDiscovery struct{}

func (runnerDiscovery) Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error) {
	return discoveryapp.Result{}, nil
}

func (runnerDiscovery) Categories(ctx context.Context, sourceID string) ([]discoveryapp.Category, error) {
	return []discoveryapp.Category{{Title: "玄幻", URL: "https://source.test/discovery"}}, nil
}

type runnerBooks struct{}

func (runnerBooks) FetchInfo(ctx context.Context, src *source.BookSource, b book.Book) (book.Book, error) {
	return book.Book{BookURL: b.BookURL, Name: b.Name, Author: b.Author}, nil
}

func (runnerBooks) FetchToc(ctx context.Context, src *source.BookSource, b book.Book) ([]*book.BookChapter, error) {
	return []*book.BookChapter{{BookURL: b.BookURL, URL: "https://book.test/c/1", Title: "第一章", Index: 0}}, nil
}

type runnerContent struct{}

func (runnerContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	return contentapp.Result{Content: "正文", Chapter: chapter}, nil
}

type failingRunnerContent struct{}

func (failingRunnerContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	return contentapp.Result{}, contentapp.ErrContentEmpty
}

type blockingRunnerContent struct {
	started  chan struct{}
	released chan struct{}
}

func (c *blockingRunnerContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	close(c.started)
	<-ctx.Done()
	close(c.released)
	return contentapp.Result{}, ctx.Err()
}

type fakeCheckRepo struct {
	mu              sync.Mutex
	cond            *sync.Cond
	tasks           map[string]*task.CheckTask
	items           map[string][]*task.CheckTaskItem
	saveItemErr     error
	progressUpdates []int
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

func (r *fakeCheckRepo) UpdateTaskProgress(ctx context.Context, taskID string, finished int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.tasks[taskID]
	if item.Status == task.CheckTaskRunning && finished > item.Finished {
		item.Finished = finished
		r.progressUpdates = append(r.progressUpdates, finished)
	}
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

func (r *fakeCheckRepo) FailInterruptedTasks(ctx context.Context, completedAt time.Time) (int64, error) {
	return 0, nil
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
