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
	req.Header.Set(TraceHeader, "trace-in")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if traceID != "trace-in" {
		t.Fatalf("traceID = %q, want trace-in", traceID)
	}
	if got := rec.Header().Get(TraceHeader); got != "trace-in" {
		t.Fatalf("response trace header = %q, want trace-in", got)
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
