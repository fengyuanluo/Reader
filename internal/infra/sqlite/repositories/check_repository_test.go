package repositories

import (
	"context"
	"errors"
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
