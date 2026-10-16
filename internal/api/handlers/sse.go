package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"reader/internal/api/middleware"
	"reader/internal/domain/task"
)

// defaultSSEHeartbeatInterval 是 SSE 心跳间隔；心跳既是防代理超时的保活，也是检测会话收敛的周期。
const defaultSSEHeartbeatInterval = 15 * time.Second

// sseHeartbeatInterval 返回当前心跳间隔，测试可以缩短它。
func (h *SourceHandler) sseHeartbeatInterval() time.Duration {
	if h.heartbeat > 0 {
		return h.heartbeat
	}
	return defaultSSEHeartbeatInterval
}

// prepareSSE 校验流式能力并写好 SSE 响应头。
func prepareSSE(w http.ResponseWriter, r *http.Request) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "streaming is not supported", nil)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, true
}

// writeSSEWriter 写入一条 SSE 消息；返回错误表示连接已断开，调用方应结束循环。
func writeSSEMessage(w http.ResponseWriter, flusher http.Flusher, eventType string, data []byte) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// writeSSEHeartbeat 发送一条 SSE 注释作为心跳，不产生客户端事件。
func writeSSEHeartbeat(w http.ResponseWriter, flusher http.Flusher) error {
	if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// lastEventID 读取 Last-Event-ID 头，供断线重连时从指定事件续传。
func lastEventID(r *http.Request) int64 {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func decodeSSEEvent(event any) ([]byte, bool) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// isTerminalDebugEvent 判断调试事件是否为会话终态事件。
func isTerminalDebugEvent(eventType task.DebugEventType) bool {
	return eventType == task.DebugEventCompleted || eventType == task.DebugEventCancelled
}
