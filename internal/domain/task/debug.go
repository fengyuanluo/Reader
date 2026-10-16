package task

import (
	"context"
	"time"
)

type DebugMode string

const (
	DebugModeKeyword      DebugMode = "keyword"
	DebugModeInfoURL      DebugMode = "info_url"
	DebugModeTocURL       DebugMode = "toc_url"
	DebugModeContentURL   DebugMode = "content_url"
	DebugModeDiscoveryURL DebugMode = "discovery_url"
)

type DebugSessionStatus string

const (
	DebugSessionPending   DebugSessionStatus = "pending"
	DebugSessionRunning   DebugSessionStatus = "running"
	DebugSessionSucceeded DebugSessionStatus = "succeeded"
	DebugSessionFailed    DebugSessionStatus = "failed"
	DebugSessionCancelled DebugSessionStatus = "cancelled"
)

// DebugSessionTerminal 判断会话是否已进入终态；终态会话不再接受状态迁移。
func DebugSessionTerminal(status DebugSessionStatus) bool {
	switch status {
	case DebugSessionSucceeded, DebugSessionFailed, DebugSessionCancelled:
		return true
	default:
		return false
	}
}

type DebugEventType string

const (
	DebugEventStarted        DebugEventType = "debug.started"
	DebugEventStageStarted   DebugEventType = "debug.stage_started"
	DebugEventLog            DebugEventType = "debug.log"
	DebugEventStageCompleted DebugEventType = "debug.stage_completed"
	DebugEventStageFailed    DebugEventType = "debug.stage_failed"
	DebugEventCompleted      DebugEventType = "debug.completed"
	DebugEventCancelled      DebugEventType = "debug.cancelled"
)

type DebugEventLevel string

const (
	DebugLevelDebug DebugEventLevel = "debug"
	DebugLevelInfo  DebugEventLevel = "info"
	DebugLevelWarn  DebugEventLevel = "warn"
	DebugLevelError DebugEventLevel = "error"
)

type DebugSession struct {
	SessionID   string
	SourceID    string
	Mode        DebugMode
	Status      DebugSessionStatus
	Keyword     string
	URL         string
	TraceID     string
	CreatedAt   time.Time
	CompletedAt *time.Time
}

type DebugEvent struct {
	EventID   int64
	SessionID string
	SourceID  string
	Type      DebugEventType
	Stage     string
	Level     DebugEventLevel
	Message   string
	ElapsedMS int64
	TraceID   string
	Data      map[string]any
	CreatedAt time.Time
}

type DebugRepository interface {
	CreateSession(ctx context.Context, session *DebugSession) error
	FindSession(ctx context.Context, sessionID string) (*DebugSession, error)
	// UpdateSessionStatus 只在会话尚未进入终态时写入终态，返回是否真正生效；
	// 会话不存在返回 ErrNotFound。取消与自然结束并发时，先到者胜出。
	UpdateSessionStatus(ctx context.Context, sessionID string, status DebugSessionStatus, completedAt *time.Time) (bool, error)
	AppendEvent(ctx context.Context, event *DebugEvent) error
	ListEvents(ctx context.Context, sessionID string, afterEventID int64) ([]*DebugEvent, error)
	// FailInterruptedSessions 把进程重启后遗留的未结束会话收敛为 failed，返回受影响行数。
	FailInterruptedSessions(ctx context.Context, completedAt time.Time) (int64, error)
}
