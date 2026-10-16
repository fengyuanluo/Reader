package task

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidCheckTaskTransition = errors.New("invalid check task status transition")

type CheckTaskStatus string

const (
	CheckTaskPending   CheckTaskStatus = "pending"
	CheckTaskRunning   CheckTaskStatus = "running"
	CheckTaskSucceeded CheckTaskStatus = "succeeded"
	CheckTaskFailed    CheckTaskStatus = "failed"
	CheckTaskCancelled CheckTaskStatus = "cancelled"
)

type CheckTaskItemStatus string

const (
	CheckItemPending   CheckTaskItemStatus = "pending"
	CheckItemRunning   CheckTaskItemStatus = "running"
	CheckItemPassed    CheckTaskItemStatus = "passed"
	CheckItemFailed    CheckTaskItemStatus = "failed"
	CheckItemSkipped   CheckTaskItemStatus = "skipped"
	CheckItemCancelled CheckTaskItemStatus = "cancelled"
)

type CheckTask struct {
	TaskID      string
	Status      CheckTaskStatus
	Total       int
	Finished    int
	TraceID     string
	CreatedAt   time.Time
	CompletedAt *time.Time
	Items       []*CheckTaskItem
}

type CheckTaskItem struct {
	TaskID         string
	SourceID       string
	BookSourceURL  string
	BookSourceName string
	Status         CheckTaskItemStatus
	FailureCode    string
	FailureClass   string
	Message        string
	RespondTime    int64
	TraceID        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func CanTransitionCheckTask(from CheckTaskStatus, to CheckTaskStatus) bool {
	if from == "" {
		return to == CheckTaskPending
	}
	switch from {
	case CheckTaskPending:
		return to == CheckTaskRunning || to == CheckTaskCancelled
	case CheckTaskRunning:
		return to == CheckTaskSucceeded || to == CheckTaskFailed || to == CheckTaskCancelled
	case CheckTaskSucceeded, CheckTaskFailed, CheckTaskCancelled:
		return false
	default:
		return false
	}
}

// checkTaskStatuses 列出全部已持久化的任务状态，供按目标状态反推合法前驱。
var checkTaskStatuses = []CheckTaskStatus{CheckTaskPending, CheckTaskRunning, CheckTaskSucceeded, CheckTaskFailed, CheckTaskCancelled}

// CheckTaskPredecessors 返回可以迁移到 to 的全部状态，供仓储用条件 UPDATE 原子完成“校验+写入”。
func CheckTaskPredecessors(to CheckTaskStatus) []CheckTaskStatus {
	var from []CheckTaskStatus
	for _, status := range checkTaskStatuses {
		if CanTransitionCheckTask(status, to) {
			from = append(from, status)
		}
	}
	return from
}

type CheckRepository interface {
	CreateTask(ctx context.Context, task *CheckTask) error
	FindTask(ctx context.Context, taskID string) (*CheckTask, error)
	UpdateTaskStatus(ctx context.Context, taskID string, status CheckTaskStatus, finished int, completedAt *time.Time) error
	SaveItem(ctx context.Context, item *CheckTaskItem) error
	ListItems(ctx context.Context, taskID string) ([]*CheckTaskItem, error)
}
