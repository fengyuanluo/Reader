package repositories

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

func TestCheckRepositoryCreatesUpdatesAndFindsTask(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	createdAt := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	checkTask := &task.CheckTask{
		TaskID:    "check-1",
		Status:    task.CheckTaskPending,
		Total:     2,
		TraceID:   "trace-check",
		CreatedAt: createdAt,
	}
	if err := repo.CreateTask(ctx, checkTask); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	got, err := repo.FindTask(ctx, checkTask.TaskID)
	if err != nil {
		t.Fatalf("FindTask() error = %v", err)
	}
	if got.Status != task.CheckTaskPending || got.Total != 2 || got.TraceID != "trace-check" || !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("task = %#v", got)
	}

	if err := repo.UpdateTaskStatus(ctx, checkTask.TaskID, task.CheckTaskRunning, 0, nil); err != nil {
		t.Fatalf("UpdateTaskStatus(running) error = %v", err)
	}
	if err := repo.UpdateTaskProgress(ctx, checkTask.TaskID, 1); err != nil {
		t.Fatalf("UpdateTaskProgress(1) error = %v", err)
	}
	if err := repo.UpdateTaskProgress(ctx, checkTask.TaskID, 0); err != nil {
		t.Fatalf("UpdateTaskProgress(0) error = %v", err)
	}
	got, err = repo.FindTask(ctx, checkTask.TaskID)
	if err != nil || got.Finished != 1 {
		t.Fatalf("progress task = %#v, error = %v", got, err)
	}
	completedAt := createdAt.Add(time.Second)
	if err := repo.UpdateTaskStatus(ctx, checkTask.TaskID, task.CheckTaskSucceeded, 2, &completedAt); err != nil {
		t.Fatalf("UpdateTaskStatus(succeeded) error = %v", err)
	}
	got, err = repo.FindTask(ctx, checkTask.TaskID)
	if err != nil {
		t.Fatalf("FindTask() after update error = %v", err)
	}
	if got.Status != task.CheckTaskSucceeded || got.Finished != 2 || got.CompletedAt == nil || !got.CompletedAt.Equal(completedAt) {
		t.Fatalf("updated task = %#v", got)
	}
}

func TestCheckRepositoryRejectsInvalidTaskTransition(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	checkTask := &task.CheckTask{TaskID: "check-invalid", Status: task.CheckTaskPending, Total: 1, CreatedAt: time.Now().UTC()}
	if err := repo.CreateTask(ctx, checkTask); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	err := repo.UpdateTaskStatus(ctx, checkTask.TaskID, task.CheckTaskSucceeded, 1, nil)
	if !errors.Is(err, task.ErrInvalidCheckTaskTransition) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRepositorySavesAndListsItems(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	checkTask := &task.CheckTask{TaskID: "check-items", Status: task.CheckTaskPending, Total: 1, CreatedAt: time.Now().UTC()}
	if err := repo.CreateTask(ctx, checkTask); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	item := &task.CheckTaskItem{
		TaskID:         checkTask.TaskID,
		SourceID:       "source-1",
		BookSourceURL:  "https://source.test",
		BookSourceName: "Source",
		Status:         task.CheckItemRunning,
		TraceID:        "trace-item",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := repo.SaveItem(ctx, item); err != nil {
		t.Fatalf("SaveItem(running) error = %v", err)
	}
	item.Status = task.CheckItemFailed
	item.FailureCode = "search_failed"
	item.FailureClass = "source_search_empty_result"
	item.Message = "empty search"
	item.RespondTime = 180000
	item.KeywordUsed = "源关键词"
	item.TimeoutMS = 180000
	item.Stages = []task.CheckStageResult{{Flow: task.CheckFlowSearch, Stage: task.CheckStageSearch, Status: task.CheckItemFailed, FailureCode: "search_failed", FailureClass: "source_search_empty_result", Message: "empty search"}}
	if err := repo.SaveItem(ctx, item); err != nil {
		t.Fatalf("SaveItem(failed) error = %v", err)
	}
	items, err := repo.ListItems(ctx, checkTask.TaskID)
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	if len(items) != 1 || items[0].Status != task.CheckItemFailed || items[0].FailureCode != "search_failed" || items[0].FailureClass != "source_search_empty_result" || items[0].RespondTime != 180000 || items[0].KeywordUsed != "源关键词" || items[0].TimeoutMS != 180000 || len(items[0].Stages) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if _, err := repo.FindTask(ctx, checkTask.TaskID); err != nil {
		t.Fatalf("FindTask() error = %v", err)
	}
	// FindTask 只返回任务行（S03-06），条目走 ListItems。
	if items := mustListItems(t, repo, checkTask.TaskID); len(items) != 1 || items[0].SourceID != "source-1" {
		t.Fatalf("task items = %#v", items)
	}
}

func TestUpdateTaskStatusIsAtomicUnderConcurrency(t *testing.T) {
	db := openRepoTestDB(t)
	// 放开连接池，让两个更新真正并发执行（WAL + busy_timeout 保证不报锁错误）。
	db.SetMaxOpenConns(4)
	repo := NewCheckRepository(db)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		taskID := "race-" + string(rune('a'+i))
		if err := repo.CreateTask(ctx, &task.CheckTask{TaskID: taskID, Status: task.CheckTaskPending, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if err := repo.UpdateTaskStatus(ctx, taskID, task.CheckTaskRunning, 0, nil); err != nil {
			t.Fatalf("UpdateTaskStatus(running) error = %v", err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j, status := range []task.CheckTaskStatus{task.CheckTaskSucceeded, task.CheckTaskCancelled} {
			wg.Add(1)
			go func(j int, status task.CheckTaskStatus) {
				defer wg.Done()
				errs[j] = repo.UpdateTaskStatus(ctx, taskID, status, 1, nil)
			}(j, status)
		}
		wg.Wait()
		succeeded := 0
		for _, err := range errs {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, task.ErrInvalidCheckTaskTransition):
			default:
				t.Fatalf("UpdateTaskStatus() unexpected error = %v", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("task %s: %d concurrent terminal transitions succeeded, want exactly 1 (errs=%v)", taskID, succeeded, errs)
		}
	}
}

func TestFailInterruptedTasks(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	createdAt := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	for _, checkTask := range []*task.CheckTask{
		{TaskID: "running", Status: task.CheckTaskRunning, Total: 2, CreatedAt: createdAt},
		{TaskID: "pending", Status: task.CheckTaskPending, Total: 1, CreatedAt: createdAt},
		{TaskID: "done", Status: task.CheckTaskSucceeded, Total: 1, CreatedAt: createdAt},
	} {
		if err := repo.CreateTask(ctx, checkTask); err != nil {
			t.Fatalf("CreateTask(%s) error = %v", checkTask.TaskID, err)
		}
	}
	completedAt := createdAt.Add(time.Minute)
	count, err := repo.FailInterruptedTasks(ctx, completedAt)
	if err != nil {
		t.Fatalf("FailInterruptedTasks() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	for _, taskID := range []string{"running", "pending"} {
		got, err := repo.FindTask(ctx, taskID)
		if err != nil {
			t.Fatalf("FindTask(%s) error = %v", taskID, err)
		}
		if got.Status != task.CheckTaskFailed || got.CompletedAt == nil {
			t.Fatalf("interrupted task = %#v", got)
		}
	}
	done, err := repo.FindTask(ctx, "done")
	if err != nil {
		t.Fatalf("FindTask(done) error = %v", err)
	}
	if done.Status != task.CheckTaskSucceeded {
		t.Fatalf("terminal task was overwritten: %#v", done)
	}
}

func TestUpdateTaskStatusErrors(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &task.CheckTask{TaskID: "done", Status: task.CheckTaskPending, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	cases := []struct {
		name    string
		taskID  string
		status  task.CheckTaskStatus
		wantErr error
	}{
		{name: "missing task", taskID: "missing", status: task.CheckTaskRunning, wantErr: ErrNotFound},
		{name: "invalid transition", taskID: "done", status: task.CheckTaskSucceeded, wantErr: task.ErrInvalidCheckTaskTransition},
		{name: "no predecessor", taskID: "done", status: task.CheckTaskPending, wantErr: task.ErrInvalidCheckTaskTransition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.UpdateTaskStatus(ctx, tc.taskID, tc.status, 0, nil); !errors.Is(err, tc.wantErr) {
				t.Fatalf("UpdateTaskStatus() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// mustListItems 读取任务条目（FindTask 只返回任务行，S03-06）。
func mustListItems(t *testing.T, repo *CheckRepository, taskID string) []*task.CheckTaskItem {
	t.Helper()
	items, err := repo.ListItems(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	return items
}

// TestSaveItemWithProgressMergesItemAndProgressWrites 覆盖 S03-02：SaveItemWithProgress
// 在单个事务内保存条目并推进 finished，进度只增不减。
func TestSaveItemWithProgressMergesItemAndProgressWrites(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	createdAt := time.Now().UTC()
	if err := repo.CreateTask(ctx, &task.CheckTask{TaskID: "t-merge", Status: task.CheckTaskPending, Total: 2, CreatedAt: createdAt}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := repo.UpdateTaskStatus(ctx, "t-merge", task.CheckTaskRunning, 0, nil); err != nil {
		t.Fatalf("UpdateTaskStatus(running) error = %v", err)
	}
	now := time.Now().UTC()
	item := &task.CheckTaskItem{TaskID: "t-merge", SourceID: "s1", Status: task.CheckItemPassed, CreatedAt: now, UpdatedAt: now}
	if err := repo.SaveItemWithProgress(ctx, item, 1); err != nil {
		t.Fatalf("SaveItemWithProgress(1) error = %v", err)
	}
	// finished 只增不减：回退值被忽略。
	if err := repo.SaveItemWithProgress(ctx, &task.CheckTaskItem{TaskID: "t-merge", SourceID: "s2", Status: task.CheckItemPassed, CreatedAt: now, UpdatedAt: now}, 0); err != nil {
		t.Fatalf("SaveItemWithProgress(0) error = %v", err)
	}
	got, err := repo.FindTask(ctx, "t-merge")
	if err != nil || got.Finished != 1 {
		t.Fatalf("task = %#v, error = %v, want finished=1", got, err)
	}
	if items := mustListItems(t, repo, "t-merge"); len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
}

// TestSaveItemsBatchPersistsAll 覆盖 S03-11：SaveItems 在单个事务内批量保存条目。
func TestSaveItemsBatchPersistsAll(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &task.CheckTask{TaskID: "t-batch", Status: task.CheckTaskPending, Total: 3, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	now := time.Now().UTC()
	queued := []*task.CheckTaskItem{
		{TaskID: "t-batch", SourceID: "s1", Status: task.CheckItemCancelled, Message: "check cancelled", CreatedAt: now, UpdatedAt: now},
		{TaskID: "t-batch", SourceID: "s2", Status: task.CheckItemCancelled, Message: "check cancelled", CreatedAt: now, UpdatedAt: now},
		{TaskID: "t-batch", SourceID: "s3", Status: task.CheckItemCancelled, Message: "check cancelled", CreatedAt: now, UpdatedAt: now},
	}
	if err := repo.SaveItems(ctx, queued); err != nil {
		t.Fatalf("SaveItems() error = %v", err)
	}
	items := mustListItems(t, repo, "t-batch")
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for _, item := range items {
		if item.Status != task.CheckItemCancelled {
			t.Fatalf("item %s = %s, want cancelled", item.SourceID, item.Status)
		}
	}
	// 空批次是 no-op。
	if err := repo.SaveItems(ctx, nil); err != nil {
		t.Fatalf("SaveItems(nil) error = %v", err)
	}
}

// TestListItemsPageReturnsPagedItems 覆盖 S03-06：ListItemsPage 按页读取条目并返回 total。
func TestListItemsPageReturnsPagedItems(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &task.CheckTask{TaskID: "t-page", Status: task.CheckTaskPending, Total: 5, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		status := task.CheckItemPassed
		if id == "s5" {
			status = task.CheckItemFailed
		}
		if err := repo.SaveItem(ctx, &task.CheckTaskItem{TaskID: "t-page", SourceID: id, Status: status, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveItem(%s) error = %v", id, err)
		}
	}
	page1, total, err := repo.ListItemsPage(ctx, "t-page", nil, 1, 2)
	if err != nil {
		t.Fatalf("ListItemsPage() error = %v", err)
	}
	if len(page1) != 2 || total != 5 {
		t.Fatalf("page1 = %d items, total = %d, want 2 and 5", len(page1), total)
	}
	page3, total, err := repo.ListItemsPage(ctx, "t-page", nil, 3, 2)
	if err != nil {
		t.Fatalf("ListItemsPage(3) error = %v", err)
	}
	if len(page3) != 1 || total != 5 {
		t.Fatalf("page3 = %d items, total = %d, want 1 and 5", len(page3), total)
	}
	// 不重不漏：三页并起来是全部 5 条。
	page2, _, err := repo.ListItemsPage(ctx, "t-page", nil, 2, 2)
	if err != nil {
		t.Fatalf("ListItemsPage(2) error = %v", err)
	}
	seen := map[string]bool{}
	for _, item := range append(append(append([]*task.CheckTaskItem{}, page1...), page2...), page3...) {
		seen[item.SourceID] = true
	}
	if len(seen) != 5 {
		t.Fatalf("paged items = %#v, want 5 unique sources", seen)
	}
	// 状态过滤。
	failed := task.CheckItemFailed
	filtered, total, err := repo.ListItemsPage(ctx, "t-page", &failed, 1, 10)
	if err != nil {
		t.Fatalf("ListItemsPage(filtered) error = %v", err)
	}
	if len(filtered) != 1 || total != 1 || filtered[0].SourceID != "s5" {
		t.Fatalf("filtered = %#v, total = %d, want only s5", filtered, total)
	}
}

// TestHasUnfinishedTasksDetectsActiveRuns 覆盖 S03-04：HasUnfinishedTasks 识别 pending/running 任务。
func TestHasUnfinishedTasksDetectsActiveRuns(t *testing.T) {
	repo := NewCheckRepository(openRepoTestDB(t))
	ctx := context.Background()
	if active, err := repo.HasUnfinishedTasks(ctx); err != nil || active {
		t.Fatalf("HasUnfinishedTasks() = %v, %v, want false", active, err)
	}
	if err := repo.CreateTask(ctx, &task.CheckTask{TaskID: "t-1", Status: task.CheckTaskPending, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if active, err := repo.HasUnfinishedTasks(ctx); err != nil || !active {
		t.Fatalf("HasUnfinishedTasks() = %v, %v, want true for pending", active, err)
	}
	completedAt := time.Now().UTC()
	// 状态机语义：pending 只能迁移到 running/cancelled，终态前先推进到 running。
	if err := repo.UpdateTaskStatus(ctx, "t-1", task.CheckTaskRunning, 0, nil); err != nil {
		t.Fatalf("UpdateTaskStatus(running) error = %v", err)
	}
	if err := repo.UpdateTaskStatus(ctx, "t-1", task.CheckTaskSucceeded, 0, &completedAt); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}
	if active, err := repo.HasUnfinishedTasks(ctx); err != nil || active {
		t.Fatalf("HasUnfinishedTasks() = %v, %v, want false for terminal", active, err)
	}
}

// TestFindMissingSourceIDsReportsUnknown 覆盖 S00-02：FindMissingSourceIDs 分批返回不存在的 sourceId。
func TestFindMissingSourceIDsReportsUnknown(t *testing.T) {
	db := openRepoTestDB(t)
	repo := NewCheckRepository(db)
	ctx := context.Background()
	sourceRepo := NewBookSourceRepository(db)
	for _, id := range []string{"s1", "s2", "s3"} {
		if err := sourceRepo.Save(ctx, &source.BookSource{SourceID: id, BookSourceURL: "https://" + id + ".test", BookSourceName: id, SearchURL: "https://" + id + ".test/search"}); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}
	missing, err := repo.FindMissingSourceIDs(ctx, []string{"s1", "missing", "s3", "gone"})
	if err != nil {
		t.Fatalf("FindMissingSourceIDs() error = %v", err)
	}
	if len(missing) != 2 || missing[0] != "missing" || missing[1] != "gone" {
		t.Fatalf("missing = %#v, want [missing gone]", missing)
	}
	missing, err = repo.FindMissingSourceIDs(ctx, []string{"s1", "s2"})
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %#v, error = %v, want none", missing, err)
	}
}
