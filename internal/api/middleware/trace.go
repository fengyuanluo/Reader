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
		if traceID == "" {
			traceID = newTraceID()
		}
		w.Header().Set(TraceHeader, traceID)
		next.ServeHTTP(w, r.WithContext(ContextWithTraceID(r.Context(), traceID)))
	})
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
