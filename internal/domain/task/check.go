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

type CheckStageFlow string

const (
	CheckFlowSearch    CheckStageFlow = "search"
	CheckFlowDiscovery CheckStageFlow = "discovery"
)

type CheckStageName string

const (
	CheckStageSearch    CheckStageName = "search"
	CheckStageDiscovery CheckStageName = "discovery"
	CheckStageInfo      CheckStageName = "info"
	CheckStageToc       CheckStageName = "toc"
	CheckStageContent   CheckStageName = "content"
)

type CheckStageResult struct {
	Flow         CheckStageFlow      `json:"flow"`
	Stage        CheckStageName      `json:"stage"`
	Status       CheckTaskItemStatus `json:"status"`
	FailureCode  string              `json:"failureCode,omitempty"`
	FailureClass string              `json:"failureClass,omitempty"`
	Message      string              `json:"message,omitempty"`
	ElapsedMS    int64               `json:"elapsedMs,omitempty"`
}

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
	KeywordUsed    string
	TimeoutMS      int64
	Stages         []CheckStageResult
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

func ValidateCheckTaskTransition(from CheckTaskStatus, to CheckTaskStatus) error {
	if CanTransitionCheckTask(from, to) {
		return nil
	}
	return ErrInvalidCheckTaskTransition
}

type CheckRepository interface {
	CreateTask(ctx context.Context, task *CheckTask) error
	FindTask(ctx context.Context, taskID string) (*CheckTask, error)
	UpdateTaskStatus(ctx context.Context, taskID string, status CheckTaskStatus, finished int, completedAt *time.Time) error
	UpdateTaskProgress(ctx context.Context, taskID string, finished int) error
	SaveItem(ctx context.Context, item *CheckTaskItem) error
	ListItems(ctx context.Context, taskID string) ([]*CheckTaskItem, error)
}
