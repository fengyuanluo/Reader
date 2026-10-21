package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

const TraceHeader = "X-Trace-Id"

type traceIDKey struct{}

func Trace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(TraceHeader)
		// 客户端提供的 traceId 必须是合法格式（16~64 位小写十六进制）；
		// 伪造/超长/含控制字符的值一律丢弃并生成新 ID，避免污染网关与内部日志。
		if !validTraceID(traceID) {
			traceID = newTraceID()
		}
		w.Header().Set(TraceHeader, traceID)
		next.ServeHTTP(w, r.WithContext(ContextWithTraceID(r.Context(), traceID)))
	})
}

// ValidTraceID 暴露入站 traceId 格式校验，供测试与其他包复用。
func ValidTraceID(traceID string) bool { return validTraceID(traceID) }

// validTraceID 校验入站 traceId 格式：16~64 位小写十六进制。
func validTraceID(traceID string) bool {
	if len(traceID) < 16 || len(traceID) > 64 {
		return false
	}
	for _, r := range traceID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

func TraceIDFromContext(ctx context.Context) string {
	traceID, _ := ctx.Value(traceIDKey{}).(string)
	return traceID
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code string, message string, details map[string]any) {
	traceID := TraceIDFromContext(r.Context())
	if traceID == "" {
		traceID = newTraceID()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(TraceHeader, traceID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": message,
		"details": details,
		"traceId": traceID,
	})
}

func newTraceID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "trace-unavailable"
	}
	return hex.EncodeToString(buf[:])
}
