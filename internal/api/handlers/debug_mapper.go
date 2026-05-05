package handlers

import (
	"reader/internal/api/generated"
	"reader/internal/domain/task"
)

func mapDebugEvent(event *task.DebugEvent) generated.DebugEvent {
	dto := generated.DebugEvent{
		ElapsedMs: event.ElapsedMS,
		Level:     generated.DebugEventLevel(event.Level),
		Message:   event.Message,
		SessionId: event.SessionID,
		SourceId:  event.SourceID,
		TraceId:   event.TraceID,
		Type:      generated.DebugEventType(event.Type),
	}
	if event.Stage != "" {
		dto.Stage = &event.Stage
	}
	if len(event.Data) > 0 {
		data := map[string]any(event.Data)
		dto.Data = &data
	}
	return dto
}
