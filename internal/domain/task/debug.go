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
	UpdateSessionStatus(ctx context.Context, sessionID string, status DebugSessionStatus, completedAt *time.Time) error
	AppendEvent(ctx context.Context, event *DebugEvent) error
	ListEvents(ctx context.Context, sessionID string, afterEventID int64) ([]*DebugEvent, error)
}
