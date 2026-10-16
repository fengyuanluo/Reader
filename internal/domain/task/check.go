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

// CheckItemPath 标记条目检查发生在上游的搜索链路还是发现链路。
// 校验按 Legado CheckSourceService 的语义生成分组名（如“搜索目录失效”与“发现目录失效”），
// 因此条目需要携带自己的来源链路。该字段仅用于诊断分组，不落库、不对外暴露。
type CheckItemPath string

const (
	CheckPathSearch    CheckItemPath = "search"
	CheckPathDiscovery CheckItemPath = "discovery"
)

// CheckEventType 是校验任务事件流的类型，取值与 OpenAPI CheckEvent 枚举一致。
type CheckEventType string

const (
	CheckEventStarted     CheckEventType = "check.started"
	CheckEventItemStarted CheckEventType = "check.item_started"
	CheckEventItemPassed  CheckEventType = "check.item_passed"
	CheckEventItemFailed  CheckEventType = "check.item_failed"
	CheckEventCompleted   CheckEventType = "check.completed"
	CheckEventCancelled   CheckEventType = "check.cancelled"
)

// IsTerminalCheckEvent 判断事件是否为任务终态事件，订阅方收到后可以收尾断开。
func IsTerminalCheckEvent(eventType CheckEventType) bool {
	return eventType == CheckEventCompleted || eventType == CheckEventCancelled
}

// CheckEvent 是校验任务的流式进度事件，由 runner 在检查过程中发布。
type CheckEvent struct {
	TaskID    string
	SourceID  string
	Type      CheckEventType
	Message   string
	Item      *CheckTaskItem
	TraceID   string
	CreatedAt time.Time
}

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
	Path           CheckItemPath
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

// CheckTaskTerminal 判断校验任务是否已进入终态；终态任务不再接受状态迁移。
func CheckTaskTerminal(status CheckTaskStatus) bool {
	switch status {
	case CheckTaskSucceeded, CheckTaskFailed, CheckTaskCancelled:
		return true
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
	UpdateTaskProgress(ctx context.Context, taskID string, finished int) error
	SaveItem(ctx context.Context, item *CheckTaskItem) error
	ListItems(ctx context.Context, taskID string) ([]*CheckTaskItem, error)
	// FailInterruptedTasks 把进程重启后遗留的未完成任务收敛为 failed，返回受影响行数。
	FailInterruptedTasks(ctx context.Context, completedAt time.Time) (int64, error)
}
