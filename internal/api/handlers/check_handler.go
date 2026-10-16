package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
	sqliterepo "reader/internal/infra/sqlite/repositories"
)

func (h *SourceHandler) CreateBookSourceCheckTask(w http.ResponseWriter, r *http.Request) {
	if h.check == nil {
		writeNotConfigured(w, r, "check service is not configured")
		return
	}
	var body generated.CheckTaskCreateRequest
	raw, ok := h.readJSONBody(w, r, h.bodyLimit)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeValidationError(w, r, err.Error())
		return
	}
	if len(body.SourceIds) == 0 {
		writeValidationError(w, r, "sourceIds is required")
		return
	}
	checkTask := &task.CheckTask{
		TaskID:    "check-" + time.Now().UTC().Format("20060102150405.000000000"),
		Status:    task.CheckTaskPending,
		TraceID:   middleware.TraceIDFromContext(r.Context()),
		CreatedAt: time.Now().UTC(),
	}
	created, err := h.check.Start(r.Context(), checkTask, body.SourceIds, checkOptionsFromDTO(body.Options), h.checkConcurrency)
	if err != nil {
		writeCheckCreateError(w, r, err)
		return
	}
	// 校验任务是异步资源：创建即返回 202 Accepted，进度由任务状态与事件流提供。
	writeJSON(w, http.StatusAccepted, mapCheckTask(created))
}

func (h *SourceHandler) GetCheckTask(w http.ResponseWriter, r *http.Request, taskId string) {
	if h.check == nil {
		writeNotConfigured(w, r, "check service is not configured")
		return
	}
	checkTask, err := h.check.Get(r.Context(), taskId)
	if err != nil {
		writeCheckError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapCheckTask(checkTask))
}

func (h *SourceHandler) CancelCheckTask(w http.ResponseWriter, r *http.Request, taskId string) {
	if h.check == nil {
		writeNotConfigured(w, r, "check service is not configured")
		return
	}
	checkTask, err := h.check.Cancel(r.Context(), taskId)
	if err != nil {
		writeCheckError(w, r, err)
		return
	}
	// 取消是异步请求：返回 202，终态由任务状态体现。
	writeJSON(w, http.StatusAccepted, mapCheckTask(checkTask))
}

func (h *SourceHandler) StreamCheckTaskEvents(w http.ResponseWriter, r *http.Request, taskId string) {
	if h.check == nil {
		writeNotConfigured(w, r, "check service is not configured")
		return
	}
	checkTask, err := h.check.Get(r.Context(), taskId)
	if err != nil {
		writeCheckError(w, r, err)
		return
	}
	events, ok := h.check.Subscribe(r.Context(), taskId)
	if !ok {
		// 事件流不存在：任务已收敛且超过保留期时用一条终态事件收尾；
		// 运行中的任务没有事件流说明装配异常，直接报错而不是挂起连接。
		if task.CheckTaskTerminal(checkTask.Status) {
			flusher, prepared := prepareSSE(w, r)
			if !prepared {
				return
			}
			h.writeCheckEvent(w, flusher, terminalCheckEvent(checkTask))
			return
		}
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "check task event stream is unavailable", nil)
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
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if h.writeCheckEvent(w, flusher, event) {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w, flusher); err != nil {
				return
			}
		}
	}
}

// writeCheckEvent 写入单条校验事件；返回 true 表示连接已断开或事件为终态，调用方应结束响应。
func (h *SourceHandler) writeCheckEvent(w http.ResponseWriter, flusher http.Flusher, event *task.CheckEvent) bool {
	if encoded, ok := decodeSSEEvent(mapCheckEvent(event)); ok {
		if err := writeSSEMessage(w, flusher, string(event.Type), encoded); err != nil {
			return true
		}
	}
	return task.IsTerminalCheckEvent(event.Type)
}

func terminalCheckEvent(checkTask *task.CheckTask) *task.CheckEvent {
	return &task.CheckEvent{
		TaskID:    checkTask.TaskID,
		Type:      task.CheckEventType(checkEventType(checkTask.Status)),
		Message:   string(checkTask.Status),
		TraceID:   checkTask.TraceID,
		CreatedAt: time.Now().UTC(),
	}
}

// checkEventType 把校验任务状态映射为事件类型，与 OpenAPI CheckEventType 取值保持一致。
func checkEventType(status task.CheckTaskStatus) generated.CheckEventType {
	switch status {
	case task.CheckTaskPending:
		return generated.CheckStarted
	case task.CheckTaskSucceeded, task.CheckTaskFailed:
		return generated.CheckCompleted
	case task.CheckTaskCancelled:
		return generated.CheckCancelled
	default:
		return generated.CheckProgress
	}
}

// writeCheckCreateError 处理创建校验任务的错误：sourceId 不存在属于请求内容错误，
// 返回 spec 已声明的 400（而不是仅供查询任务的 404）。
func writeCheckCreateError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, source.ErrNotFound) {
		middleware.WriteError(w, r, http.StatusBadRequest, "source_not_found", "book source not found", nil)
		return
	}
	writeCheckError(w, r, err)
}

func writeCheckError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sqliterepo.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "task_not_found", "check task not found", nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}
