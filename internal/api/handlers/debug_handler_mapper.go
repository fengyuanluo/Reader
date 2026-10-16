package handlers

import (
	"time"

	"reader/internal/api/generated"
	"reader/internal/domain/task"
)

// validDebugMode 校验请求中的 mode 是否属于 DebugMode 枚举，避免创建永远无法执行的会话。
func validDebugMode(mode generated.DebugMode) bool {
	switch mode {
	case generated.Keyword, generated.InfoUrl, generated.TocUrl, generated.ContentUrl, generated.DiscoveryUrl:
		return true
	default:
		return false
	}
}

func debugSessionFromCreateRequest(body generated.DebugSessionCreateRequest, traceID string) task.DebugSession {
	return task.DebugSession{
		SessionID: "debug-" + time.Now().UTC().Format("20060102150405.000000000"),
		SourceID:  body.SourceId,
		Mode:      task.DebugMode(body.Mode),
		Status:    task.DebugSessionRunning,
		Keyword:   stringValue(body.Keyword),
		URL:       stringValue(body.Url),
		TraceID:   traceID,
		CreatedAt: time.Now().UTC(),
	}
}

func mapDebugSession(session *task.DebugSession) generated.DebugSession {
	dto := generated.DebugSession{
		CreatedAt: session.CreatedAt,
		Mode:      generated.DebugMode(session.Mode),
		SessionId: session.SessionID,
		SourceId:  session.SourceID,
		Status:    generated.DebugSessionStatus(session.Status),
	}
	if session.Keyword != "" {
		dto.Keyword = &session.Keyword
	}
	if session.URL != "" {
		dto.Url = &session.URL
	}
	if session.TraceID != "" {
		dto.TraceId = &session.TraceID
	}
	if session.CompletedAt != nil {
		dto.CompletedAt = session.CompletedAt
	}
	return dto
}
