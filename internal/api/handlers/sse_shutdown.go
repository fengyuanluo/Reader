package handlers

import (
	"context"
	"net/http"
	"sync"
)

// sseStreamRegistry 记录活跃 SSE 连接的 context 取消函数：收到 SIGTERM 时
// CloseEventStreams 先取消全部事件流再 Shutdown，避免活跃连接使优雅关闭超时（S10-01）。
type sseStreamRegistry struct {
	mu      sync.Mutex
	cancels map[*context.CancelFunc]struct{}
}

func newSSEStreamRegistry() *sseStreamRegistry {
	return &sseStreamRegistry{cancels: map[*context.CancelFunc]struct{}{}}
}

// register 注册一个 SSE 流 context；返回包了取消的 context。
func (r *sseStreamRegistry) register(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancels[&cancel] = struct{}{}
	r.mu.Unlock()
	return ctx, func() {
		r.mu.Lock()
		delete(r.cancels, &cancel)
		r.mu.Unlock()
		cancel()
	}
}

// cancelAll 取消全部活跃 SSE 流并清空注册表；返回取消的流数量。
func (r *sseStreamRegistry) cancelAll() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for cancel := range r.cancels {
		(*cancel)()
		delete(r.cancels, cancel)
		n++
	}
	return n
}

// WithEventStreamShutdown 包装 handler：把挂载在活跃 SSE 路由上的请求 context 纳入注册表，
// 供 CloseEventStreams 在进程关闭时统一取消（S10-01）。
func (h *SourceHandler) WithEventStreamShutdown(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.isEventStreamPath(r.URL.Path) {
			ctx, done := h.sseStreams.register(r.Context())
			defer done()
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isEventStreamPath 判断路径是否为活跃 SSE 事件流（关闭时需要主动断开）。
// 事件流挂载在 /api/v1/check/tasks/{taskId}/events 与 /api/v1/debug/sessions/{sessionId}/events。
func (h *SourceHandler) isEventStreamPath(path string) bool {
	const (
		checkPrefix  = "/api/v1/check/tasks/"
		debugPrefix  = "/api/v1/debug/sessions/"
		eventsSuffix = "/events"
	)
	return (len(path) > len(checkPrefix)+len(eventsSuffix) && path[:len(checkPrefix)] == checkPrefix && path[len(path)-len(eventsSuffix):] == eventsSuffix) ||
		(len(path) > len(debugPrefix)+len(eventsSuffix) && path[:len(debugPrefix)] == debugPrefix && path[len(path)-len(eventsSuffix):] == eventsSuffix)
}

// CloseEventStreams 取消全部活跃 SSE 事件流连接：SIGTERM 后由 serve.go 在 Shutdown 前调用，
// 避免活跃连接使优雅关闭必然超时（S10-01）。返回断开的流数量。
func (h *SourceHandler) CloseEventStreams() int {
	return h.sseStreams.cancelAll()
}
