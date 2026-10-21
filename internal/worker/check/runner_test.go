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
	runner := newTestRunner(repo, checker)
	sources := []*source.BookSource{runnerSource("s1"), runnerSource("s2")}
	checkTask := &task.CheckTask{TaskID: "task-1", TraceID: "trace-1"}

	if err := runner.Start(context.Background(), checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskSucceeded)
	got, _ := repo.FindTask(context.Background(), checkTask.TaskID)
	if got.Finished != 2 || len(itemsOf(repo, checkTask.TaskID)) != 2 {
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
	runner := newTestRunner(repo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(sourceRepo))
	checkTask := &task.CheckTask{TaskID: "task-diagnostics"}

	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
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
	runner := newTestRunner(repo, checker)
	checkTask := &task.CheckTask{TaskID: "task-cancel"}
	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
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
	runner := newTestRunner(repo, checker)
	checkTask := &task.CheckTask{TaskID: "task-save-error", TraceID: "trace-1"}

	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskFailed)
	got, _ := repo.FindTask(context.Background(), checkTask.TaskID)
	if got.Finished != 0 {
		t.Fatalf("finished = %d, want 0 when item persistence fails", got.Finished)
	}
	if len(itemsOf(repo, checkTask.TaskID)) != 0 {
		t.Fatalf("items = %#v, want none persisted", itemsOf(repo, checkTask.TaskID))
	}
}

func TestRunnerPersistsItemsBeforeDiagnosticsFailure(t *testing.T) {
	repo := newFakeCheckRepo()
	// content 失败使条目为 failed：诊断写入器失败（save source failed）只影响诊断，
	// 不影响条目落库与任务终态推进。
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &failingRunnerContent{})
	runner := newTestRunner(repo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(&failingRunnerSourceRepo{err: errors.New("save source failed")}))
	checkTask := &task.CheckTask{TaskID: "task-diagnostics-fail"}

	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskFailed)
	if len(itemsOf(repo, checkTask.TaskID)) != 1 {
		t.Fatalf("items = %#v, want 1 persisted item before diagnostics failure", itemsOf(repo, checkTask.TaskID))
	}
}

func TestRunnerPersistsAllItemsWithRealSQLiteRepository(t *testing.T) {
	repo := repositories.NewCheckRepository(openRunnerRepoTestDB(t))
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := newTestRunner(repo, checker)
	sources := []*source.BookSource{
		runnerSource("s1"),
		runnerSource("s2"),
		runnerSource("s3"),
		runnerSource("s4"),
		runnerSource("s5"),
		runnerSource("s6"),
	}
	checkTask := &task.CheckTask{TaskID: "task-real-sqlite"}

	if err := runner.Start(context.Background(), checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 4); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	waitForCheckTaskStatus(t, repo, checkTask.TaskID, func(got *task.CheckTask) error {
		if got.Finished != len(sources) {
			return fmt.Errorf("finished = %d, want %d", got.Finished, len(sources))
		}
		if items := itemsOf(repo, got.TaskID); len(items) != len(sources) {
			return fmt.Errorf("items = %d, want %d", len(items), len(sources))
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
	runner := newTestRunner(repo, checker)
	sources := []*source.BookSource{runnerSource("s1"), runnerSource("s2"), runnerSource("s3")}
	checkTask := &task.CheckTask{TaskID: "task-queued"}
	if err := runner.Start(context.Background(), checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
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
	items := itemsOf(repo, checkTask.TaskID)
	if got.Finished != len(sources) || len(items) != len(sources) {
		t.Fatalf("task = %#v, items=%d, want every item accounted for", got, len(items))
	}
	for _, item := range items {
		if item.Status != task.CheckItemCancelled {
			t.Fatalf("item %s = %#v, want cancelled (排队条目不能被记为 failed)", item.SourceID, item)
		}
	}
}

func TestRunnerBoundsConcurrentChecks(t *testing.T) {
	repo := newFakeCheckRepo()
	content := &concurrencyTrackingContent{}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, content)
	runner := newTestRunner(repo, checker)
	sources := make([]*source.BookSource, 0, 8)
	for i := range 8 {
		sources = append(sources, runnerSource("s"+strconv.Itoa(i)))
	}
	checkTask := &task.CheckTask{TaskID: "task-bounded"}
	if err := runner.Start(context.Background(), checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 2); err != nil {
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
	runner := newTestRunner(repo, checker).WithEvents(publisher)
	checkTask := &task.CheckTask{TaskID: "task-events", TraceID: "trace-events"}
	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, ok := publisher.Subscribe(ctx, checkTask.TaskID, 0)
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
			if event.Type == task.CheckEventItemPassed && event.Item != nil {
				t.Fatalf("item event must only carry a summary (no full item payload): %#v", event)
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
	runner := newTestRunner(repo, checker)
	checkTask := &task.CheckTask{TaskID: "task-duplicate"}
	opts := checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}
	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, opts, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-content.started:
	case <-time.After(time.Second):
		t.Fatalf("content did not start")
	}
	// 重复 taskId 的 Start 在注册 cancel 之前就失败，不能影响运行中任务的取消句柄。
	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, opts, 1); err == nil {
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

// UpdateSourceDiagnostics 模拟窄列更新：只改 group/comment/respondTime，不整行覆盖。
func (r *runnerSourceRepo) UpdateSourceDiagnostics(ctx context.Context, sourceID string, respondTime int64, transform func(group, comment string) (string, string)) error {
	src, err := r.FindByID(ctx, sourceID)
	if err != nil {
		return err
	}
	src.BookSourceGroup, src.BookSourceComment = transform(src.BookSourceGroup, src.BookSourceComment)
	src.RespondTime = respondTime
	return r.Save(ctx, src)
}

func (r *failingRunnerSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	return &source.BookSource{SourceID: id, BookSourceURL: "https://" + id + ".test", BookSourceName: id}, nil
}

func (r *failingRunnerSourceRepo) Save(ctx context.Context, src *source.BookSource) error {
	return r.err
}

// UpdateSourceDiagnostics 模拟窄列更新失败：语义与 Save 一致，返回注入的错误。
func (r *failingRunnerSourceRepo) UpdateSourceDiagnostics(ctx context.Context, sourceID string, respondTime int64, transform func(group, comment string) (string, string)) error {
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

// sourceIDs 提取书源 ID 列表：runner.Start 只接收 sourceID（S00-02）。
func sourceIDs(sources []*source.BookSource) []string {
	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.SourceID)
	}
	return ids
}

// newTestRunner 构造带真实 SQLite 源仓储 loader 的 runner：校验并发槽惰性加载书源。
func newTestRunner(repo task.CheckRepository, checker *checkapp.Checker) *Runner {
	loader := &runnerSourceRepo{sources: map[string]*source.BookSource{
		"s1": runnerSource("s1"), "s2": runnerSource("s2"), "s3": runnerSource("s3"),
		"s4": runnerSource("s4"), "s5": runnerSource("s5"), "s6": runnerSource("s6"),
	}}
	for i := range 20 {
		id := "s" + strconv.Itoa(i)
		loader.sources[id] = runnerSource(id)
	}
	return NewRunner(repo, checker).WithSourceLoader(loader)
}

func (r *fakeCheckRepo) SaveItemWithProgress(ctx context.Context, item *task.CheckTaskItem, finished int) error {
	if err := r.SaveItem(ctx, item); err != nil {
		return err
	}
	return r.UpdateTaskProgress(ctx, item.TaskID, finished)
}

func (r *fakeCheckRepo) SaveItems(ctx context.Context, items []*task.CheckTaskItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveItemErr != nil {
		return r.saveItemErr
	}
	for _, item := range items {
		copy := *item
		r.items[item.TaskID] = append(r.items[item.TaskID], &copy)
		r.cond.Broadcast()
	}
	return nil
}

func (r *fakeCheckRepo) ListItemsPage(ctx context.Context, taskID string, status *task.CheckTaskItemStatus, page, pageSize int) ([]*task.CheckTaskItem, int, error) {
	all, err := r.ListItems(ctx, taskID)
	if err != nil {
		return nil, 0, err
	}
	if status != nil && *status != "" {
		filtered := all[:0]
		for _, item := range all {
			if item.Status == *status {
				filtered = append(filtered, item)
			}
		}
		all = filtered
	}
	return all, len(all), nil
}

func (r *fakeCheckRepo) HasUnfinishedTasks(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if t.Status == task.CheckTaskPending || t.Status == task.CheckTaskRunning {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeCheckRepo) FindMissingSourceIDs(ctx context.Context, sourceIDs []string) ([]string, error) {
	return nil, nil
}

// itemsOf 读取任务条目：FindTask 只返回任务行，条目走 ListItems（S03-06）。
func itemsOf(repo task.CheckRepository, taskID string) []*task.CheckTaskItem {
	items, _ := repo.ListItems(context.Background(), taskID)
	return items
}

// TestRunnerLoadsSourceOnlyWhenSlotAcquired 覆盖 S00-02：runner 只保留 sourceID，
// 书源在 worker 拿到并发槽后才通过 loader 惰性加载；Start 不再解析任何规则。
func TestRunnerLoadsSourceOnlyWhenSlotAcquired(t *testing.T) {
	repo := newFakeCheckRepo()
	loader := &countingSourceLoader{inner: runnerSourceRepo{sources: map[string]*source.BookSource{
		"s1": runnerSource("s1"), "s2": runnerSource("s2"),
	}}}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := NewRunner(repo, checker).WithSourceLoader(loader)
	sources := []*source.BookSource{runnerSource("s1"), runnerSource("s2")}
	checkTask := &task.CheckTask{TaskID: "task-lazy-load"}

	if err := runner.Start(context.Background(), checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true}, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskSucceeded)
	if loader.loads() != 2 {
		t.Fatalf("FindByID calls = %d, want 2 (one per source, loaded in workers)", loader.loads())
	}
}

// TestRunnerContinuesAfterRequestContextCancel 覆盖 S03-01/S03-04 验收：请求 ctx 取消后任务继续，
// 全部条目仍校验并落库。
func TestRunnerContinuesAfterRequestContextCancel(t *testing.T) {
	repo := newFakeCheckRepo()
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := newTestRunner(repo, checker)
	sources := []*source.BookSource{runnerSource("s1"), runnerSource("s2")}
	checkTask := &task.CheckTask{TaskID: "task-ctx-cancel"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 请求已取消
	if err := runner.Start(ctx, checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 2); err != nil {
		t.Fatalf("Start() with cancelled ctx error = %v", err)
	}
	repo.waitForStatus(t, task.CheckTaskSucceeded)
	items := itemsOf(repo, checkTask.TaskID)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 after request ctx cancellation", len(items))
	}
	for _, item := range items {
		if item.Status == task.CheckItemCancelled {
			t.Fatalf("item %s = cancelled, want a real check result", item.SourceID)
		}
	}
}

// TestRunnerCancelAndTerminalRaceKeepsInvariant 覆盖 S03-05 验收：Cancel 与 run 终态写并发时，
// 状态机不变式成立——终态是一次合法迁移写入，finished 与已落库条目数一致。
func TestRunnerCancelAndTerminalRaceKeepsInvariant(t *testing.T) {
	repo := newFakeCheckRepo()
	content := &racingRunnerContent{delay: time.Millisecond}
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, content)
	runner := newTestRunner(repo, checker)
	sources := make([]*source.BookSource, 0, 4)
	for i := range 4 {
		sources = append(sources, runnerSource("s"+strconv.Itoa(i+1)))
	}
	checkTask := &task.CheckTask{TaskID: "task-race"}

	if err := runner.Start(context.Background(), checkTask, sourceIDs(sources), checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// 与条目完成并发地请求取消：Cancel 只置取消函数，不改库。
	runner.Cancel(checkTask.TaskID)
	repo.waitForTerminalStatus(t)
	items := itemsOf(repo, checkTask.TaskID)
	got := repo.tasks[checkTask.TaskID]
	if !task.CheckTaskTerminal(got.Status) {
		t.Fatalf("status = %s, want terminal", got.Status)
	}
	// 取消路径下在途条目统一记 cancelled，未启动条目批量落 cancelled；终态 finished 与条目数一致。
	if got.Finished != len(items) {
		t.Fatalf("finished = %d, persisted items = %d, want equal", got.Finished, len(items))
	}
}

func (r *fakeCheckRepo) waitForTerminalStatus(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		for _, item := range r.tasks {
			if task.CheckTaskTerminal(item.Status) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for terminal status; tasks=%#v", r.tasks)
		}
		r.cond.Wait()
	}
}

// countingSourceLoader 统计 FindByID 调用次数，用于断言书源按并发槽惰性加载。
type countingSourceLoader struct {
	mu    sync.Mutex
	inner runnerSourceRepo
	count int
}

func (c *countingSourceLoader) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	return c.inner.FindByID(ctx, id)
}

func (c *countingSourceLoader) loads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// racingRunnerContent 每次正文抓取带微小延迟，制造 Cancel 与条目完成的竞争窗口。
type racingRunnerContent struct {
	delay time.Duration
}

func (c *racingRunnerContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	time.Sleep(c.delay)
	return contentapp.Result{Content: "正文", Chapter: chapter}, nil
}

// TestRunnerPublishesItemFailedEventWhenPersistFails 覆盖 S03-07③/S03-08：SaveItem 失败也要发
// item_failed 终态事件（订阅者事件流才能收敛），且任务标 failed、finished 不含丢失项。
func TestRunnerPublishesItemFailedEventWhenPersistFails(t *testing.T) {
	repo := newFakeCheckRepo()
	repo.saveItemErr = errors.New("save item failed")
	publisher := checkapp.NewPublisher()
	checker := checkapp.NewChecker(&runnerSearch{}, &runnerDiscovery{}, &runnerBooks{}, &runnerContent{})
	runner := newTestRunner(repo, checker).WithEvents(publisher)
	checkTask := &task.CheckTask{TaskID: "task-event-on-persist-fail"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publisher.Open(checkTask.TaskID)
	events, ok := publisher.Subscribe(ctx, checkTask.TaskID, 0)
	if !ok {
		t.Fatal("Subscribe() ok = false")
	}
	if err := runner.Start(context.Background(), checkTask, []string{"s1"}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	seenItemFailed := false
	sawTerminal := false
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event == nil {
				t.Fatalf("stream closed before terminal; itemFailed=%v", seenItemFailed)
			}
			if event.Type == task.CheckEventItemFailed {
				seenItemFailed = true
			}
			if task.IsTerminalCheckEvent(event.Type) {
				if event.Type != task.CheckEventCompleted {
					// persistFailure 使任务终态为 failed（CheckEventCompleted）。
					t.Fatalf("terminal event = %s, want check.completed", event.Type)
				}
				sawTerminal = true
			}
			if sawTerminal {
				if !seenItemFailed {
					t.Fatal("persist failure never published item_failed; subscriber sees no terminal for the source")
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out; itemFailed=%v terminal=%v", seenItemFailed, sawTerminal)
		}
	}
}
