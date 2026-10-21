package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceMiddlewarePropagatesIncomingTraceID(t *testing.T) {
	var traceID string
	handler := Trace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set(TraceHeader, "0123456789abcdef0123456789abcdef")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if traceID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("traceID = %q, want 0123456789abcdef0123456789abcdef", traceID)
	}
	if got := rec.Header().Get(TraceHeader); got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("response trace header = %q, want 0123456789abcdef0123456789abcdef", got)
	}
}

func TestTraceMiddlewareGeneratesTraceID(t *testing.T) {
	var traceID string
	handler := Trace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if traceID == "" {
		t.Fatal("traceID is empty")
	}
	if got := rec.Header().Get(TraceHeader); got != traceID {
		t.Fatalf("response trace header = %q, want %q", got, traceID)
	}
}

// S10-07：客户端提供的 X-Trace-Id 必须是合法格式（16~64 位小写十六进制），
// 伪造/超长/含控制字符的值被丢弃并替换为新生成的合法 ID。
func TestTraceMiddlewareRejectsInvalidIncomingTraceID(t *testing.T) {
	invalidIDs := []string{
		"trace-in",         // 非 hex
		"0123456789ABCDEF", // 大写
		"short",            // 过短
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", // 超长
		"0123456789abcdef 0123456789abcedf",                                                // 含空格
	}
	for _, invalid := range invalidIDs {
		t.Run(invalid, func(t *testing.T) {
			var traceID string
			handler := Trace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				traceID = TraceIDFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			req.Header.Set(TraceHeader, invalid)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if traceID == invalid {
				t.Fatalf("traceID = %q, want a newly generated valid ID", traceID)
			}
			if !ValidTraceID(traceID) {
				t.Fatalf("traceID = %q, want valid hex ID", traceID)
			}
			if got := rec.Header().Get(TraceHeader); got != traceID {
				t.Fatalf("response trace header = %q, want %q", got, traceID)
			}
		})
	}
}

func TestValidTraceID(t *testing.T) {
	valid := []string{"0123456789abcdef", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	invalid := []string{"", "short", "TRACE", "with space", "0123456789abcdefg"}
	for _, id := range valid {
		if !ValidTraceID(id) {
			t.Fatalf("ValidTraceID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if ValidTraceID(id) {
			t.Fatalf("ValidTraceID(%q) = true, want false", id)
		}
	}
}

func TestWriteErrorIncludesTraceID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	ctx := ContextWithTraceID(req.Context(), "trace-error")
	rec := httptest.NewRecorder()

	WriteError(rec, req.WithContext(ctx), http.StatusUnauthorized, "auth_required", "missing bearer token", nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["traceId"] != "trace-error" {
		t.Fatalf("traceId = %v, want trace-error", body["traceId"])
	}
}
