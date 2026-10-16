package repositories

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

	if err := repo.UpdateTaskStatus(ctx, checkTask.TaskID, task.CheckTaskRunning, 1, nil); err != nil {
		t.Fatalf("UpdateTaskStatus(running) error = %v", err)
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
	if err := repo.SaveItem(ctx, item); err != nil {
		t.Fatalf("SaveItem(failed) error = %v", err)
	}
	items, err := repo.ListItems(ctx, checkTask.TaskID)
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	if len(items) != 1 || items[0].Status != task.CheckItemFailed || items[0].FailureCode != "search_failed" || items[0].FailureClass != "source_search_empty_result" || items[0].RespondTime != 180000 {
		t.Fatalf("items = %#v", items)
	}
	got, err := repo.FindTask(ctx, checkTask.TaskID)
	if err != nil {
		t.Fatalf("FindTask() error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].SourceID != "source-1" {
		t.Fatalf("task items = %#v", got.Items)
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
