package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	"reader/internal/domain/task"
	sqliterepo "reader/internal/infra/sqlite/repositories"
)

func (h *SourceHandler) CreateDebugSession(w http.ResponseWriter, r *http.Request) {
	if h.debugSvc == nil {
		writeNotConfigured(w, r, "debug service is not configured")
		return
	}
	var body generated.DebugSessionCreateRequest
	raw, ok := h.readJSONBody(w, r, h.debugBodyLimit)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeValidationError(w, r, err.Error())
		return
	}
	if strings.TrimSpace(body.SourceId) == "" {
		writeValidationError(w, r, "sourceId is required")
		return
	}
	// mode 必须落在 DebugMode 枚举内，否则会话永远无法被正确执行。
	if !validDebugMode(body.Mode) {
		writeValidationError(w, r, "mode must be one of keyword, info_url, toc_url, content_url, discovery_url")
		return
	}
	if body.Mode == generated.Keyword {
		if body.Keyword == nil || strings.TrimSpace(*body.Keyword) == "" {
			writeValidationError(w, r, "keyword is required")
			return
		}
	} else if body.Url == nil || strings.TrimSpace(*body.Url) == "" {
		writeValidationError(w, r, "url is required")
		return
	}
	session := debugSessionFromCreateRequest(body, middleware.TraceIDFromContext(r.Context()))
	created, err := h.debugSvc.CreateSession(r.Context(), &session)
	if err != nil {
		writeDebugError(w, r, err)
		return
	}
	// 调试会话是异步资源：创建即返回 202 Accepted，进度由 SSE 事件流推送。
	writeJSON(w, http.StatusAccepted, mapDebugSession(created))
}

func (h *SourceHandler) GetDebugSession(w http.ResponseWriter, r *http.Request, sessionId string) {
	if h.debugSvc == nil {
		writeNotConfigured(w, r, "debug service is not configured")
		return
	}
	session, err := h.debugSvc.GetSession(r.Context(), sessionId)
	if err != nil {
		writeDebugError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapDebugSession(session))
}

func (h *SourceHandler) CancelDebugSession(w http.ResponseWriter, r *http.Request, sessionId string) {
	if h.debugSvc == nil {
		writeNotConfigured(w, r, "debug service is not configured")
		return
	}
	session, err := h.debugSvc.CancelSession(r.Context(), sessionId)
	if err != nil {
		writeDebugError(w, r, err)
		return
	}
	// 取消是异步请求：返回 202，终态由会话状态与事件流体现。
	writeJSON(w, http.StatusAccepted, mapDebugSession(session))
}

func (h *SourceHandler) StreamDebugSessionEvents(w http.ResponseWriter, r *http.Request, sessionId string) {
	if h.debugSvc == nil || h.debug == nil {
		writeNotConfigured(w, r, "debug service is not configured")
		return
	}
	ctx := r.Context()
	// 订阅前先确认会话存在：不存在的会话立即 404，不能静默挂起连接。
	session, err := h.debugSvc.GetSession(ctx, sessionId)
	if err != nil {
		writeDebugError(w, r, err)
		return
	}
	afterEventID := lastEventID(r)
	if task.DebugSessionTerminal(session.Status) {
		// 会话已收敛，不会再有新事件：回放历史后立即结束响应。
		if err := h.replayDebugEvents(w, r, sessionId, afterEventID); err != nil {
			writeDebugError(w, r, err)
		}
		return
	}
	events, err := h.debug.Subscribe(ctx, sessionId, afterEventID)
	if err != nil {
		writeDebugError(w, r, err)
		return
	}
	flusher, ok := prepareSSE(w, r)
	if !ok {
		return
	}
	heartbeat := time.NewTicker(h.sseHeartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			encoded, ok := decodeSSEEvent(mapDebugEvent(event))
			if !ok {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.EventID, event.Type, encoded); err != nil {
				return
			}
			flusher.Flush()
			if isTerminalDebugEvent(event.Type) {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w, flusher); err != nil {
				return
			}
			// 事件流没有终态事件但会话已收敛（例如阶段失败）时，靠状态检测主动收尾。
			current, err := h.debugSvc.GetSession(ctx, sessionId)
			if err == nil && task.DebugSessionTerminal(current.Status) {
				return
			}
		}
	}
}

func (h *SourceHandler) replayDebugEvents(w http.ResponseWriter, r *http.Request, sessionId string, afterEventID int64) error {
	events, err := h.debugSvc.Events(r.Context(), sessionId, afterEventID)
	if err != nil {
		return err
	}
	flusher, ok := prepareSSE(w, r)
	if !ok {
		return nil
	}
	for _, event := range events {
		encoded, ok := decodeSSEEvent(mapDebugEvent(event))
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.EventID, event.Type, encoded); err != nil {
			return nil
		}
	}
	flusher.Flush()
	return nil
}

func writeDebugError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sqliterepo.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "debug_session_not_found", "debug session not found", nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}
