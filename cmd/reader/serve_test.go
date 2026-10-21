package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runtime/debug"

	"reader/internal/api/middleware"
	"reader/internal/infra/config"
)

func testServeConfig(t *testing.T, token string) config.Config {
	t.Helper()
	return config.Config{
		Addr:              "127.0.0.1:0",
		SQLiteDSN:         filepath.Join(t.TempDir(), "reader.db"),
		Token:             token,
		Env:               config.EnvDevelopment,
		DefaultUserAgent:  "Reader/Test",
		FetchTimeout:      30 * time.Second,
		FetchMaxBodyBytes: 10 * 1024 * 1024,
		FetchMaxRedirects: 5,
		GlobalConcurrency: 8,
		SourceConcurrency: 2,
		ScriptTimeout:     10 * time.Second,
		DebugBodyLimit:    32 * 1024,
	}
}

func TestNewRuntimeServerOptions(t *testing.T) {
	cfg := testServeConfig(t, "secret")
	server, cleanup, err := newRuntimeServer(cfg)
	if err != nil {
		t.Fatalf("newRuntimeServer() error = %v", err)
	}
	defer cleanup()
	if server.Addr != cfg.Addr {
		t.Fatalf("Addr = %q, want %q", server.Addr, cfg.Addr)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf("timeouts header=%s read=%s idle=%s, want all > 0", server.ReadHeaderTimeout, server.ReadTimeout, server.IdleTimeout)
	}
	// SSE 长连接依赖无全局写截止。
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0", server.WriteTimeout)
	}
	if _, err := os.Stat(cfg.SQLiteDSN); err != nil {
		t.Fatalf("sqlite db not created: %v", err)
	}
}

func TestNewRuntimeServerRoutesAndAuth(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		path       string
		authHeader string
		wantStatus int
		wantCode   string
	}{
		{name: "health public", token: "secret", path: "/api/v1/health", wantStatus: http.StatusOK},
		{name: "business without token", token: "secret", path: "/api/v1/book-sources", wantStatus: http.StatusUnauthorized, wantCode: "auth_required"},
		{name: "business wrong token", token: "secret", path: "/api/v1/book-sources", authHeader: "Bearer wrong", wantStatus: http.StatusUnauthorized, wantCode: "auth_invalid"},
		{name: "business valid token", token: "secret", path: "/api/v1/book-sources", authHeader: "bearer secret", wantStatus: http.StatusOK},
		{name: "loopback without token", token: "", path: "/api/v1/book-sources", wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, cleanup, err := newRuntimeServer(testServeConfig(t, tt.token))
			if err != nil {
				t.Fatalf("newRuntimeServer() error = %v", err)
			}
			defer cleanup()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			server.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if rec.Header().Get(middleware.TraceHeader) == "" {
				t.Fatal("trace header is empty")
			}
			if tt.wantCode != "" {
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != tt.wantCode {
					t.Fatalf("error code = %q (%v), want %q", body.Code, err, tt.wantCode)
				}
			}
		})
	}
}

func TestRunServeRejectsEmptyTokenOnNonLoopback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data", "reader.db")
	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("READER_TOKEN", "")
			t.Setenv("READER_ENV", config.EnvDevelopment)
			t.Setenv("READER_SQLITE_DSN", dbPath)
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := runServe(context.Background(), []string{"--addr", addr}, stdout, stderr)
			if code != 2 {
				t.Fatalf("runServe() = %d, want 2; stderr: %s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "READER_TOKEN is required") {
				t.Fatalf("stderr = %q, want token error", stderr.String())
			}
			if _, err := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(err) {
				t.Fatalf("data dir must not be created on rejected start: %v", err)
			}
		})
	}
}

func TestRunServeShutsDownOnContextCancel(t *testing.T) {
	t.Setenv("READER_TOKEN", "")
	t.Setenv("READER_ENV", config.EnvDevelopment)
	t.Setenv("READER_SQLITE_DSN", filepath.Join(t.TempDir(), "reader.db"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runServe(ctx, []string{"--addr", "127.0.0.1:0"}, stdout, stderr); code != 0 {
		t.Fatalf("runServe() = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "reader server stopping") {
		t.Fatalf("stderr = %q, want JSON shutdown log", stderr.String())
	}
}

func TestRunServeHelpExitsZero(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runServe(context.Background(), []string{"--help"}, stdout, stderr); code != 0 {
		t.Fatalf("runServe(--help) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "--addr <string>") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

// S10-04：查询参数绑定错误必须是 application/json 的 Error 对象（含 traceId），
// 而非 ogen 默认的 text/plain 400。
func TestQueryParamBindingErrorReturnsJSONError(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "invalid page", path: "/api/v1/books?page=abc"},
		{name: "invalid pageSize", path: "/api/v1/book-sources?page=1&pageSize=abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, cleanup, err := newRuntimeServer(testServeConfig(t, "secret"))
			if err != nil {
				t.Fatalf("newRuntimeServer() error = %v", err)
			}
			defer cleanup()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			rec := httptest.NewRecorder()
			server.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json; body: %s", ct, rec.Body.String())
			}
			traceID := rec.Header().Get(middleware.TraceHeader)
			if traceID == "" {
				t.Fatal("X-Trace-Id header is empty")
			}
			var body struct {
				Code    string `json:"code"`
				TraceID string `json:"traceId"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v; body: %s", err, rec.Body.String())
			}
			if body.Code == "" || body.TraceID == "" {
				t.Fatalf("error body missing code/traceId: %+v", body)
			}
			if body.TraceID != traceID {
				t.Fatalf("body traceId = %q, header = %q", body.TraceID, traceID)
			}
		})
	}
}

// setGoMemoryLimit：显式 GOMEMLIMIT 优先（不覆盖软上限），可用的 cgroup/proc 探测不可得时保持默认。
func TestSetGoMemoryLimitRespectsGOMEMLIMIT(t *testing.T) {
	previous := debug.SetMemoryLimit(1 << 30) // 先设一个已知值
	defer debug.SetMemoryLimit(previous)

	cfg := testServeConfig(t, "secret")
	cfg.GoMemLimitRatio = 0.5
	t.Setenv("GOMEMLIMIT", "2GiB")
	setGoMemoryLimit(cfg)
	// 显式 GOMEMLIMIT 时 setGoMemoryLimit 必须不动软上限。
	if current := debug.SetMemoryLimit(1 << 30); current != 1<<30 {
		t.Fatalf("memory limit = %d, want 1GiB（显式 GOMEMLIMIT 应优先）", current)
	}
}

// S10-08：READER_DEBUG_BODY_LIMIT 接到 debug/check 请求体上限——超限请求返回 413。
func TestDebugBodyLimitConsumed(t *testing.T) {
	cfg := testServeConfig(t, "secret")
	cfg.DebugBodyLimit = 100
	server, cleanup, err := newRuntimeServer(cfg)
	if err != nil {
		t.Fatalf("newRuntimeServer() error = %v", err)
	}
	defer cleanup()

	big := strings.Repeat("x", 200)
	body := `{"keyword":"` + big + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/debug/book-source", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body: %s", rec.Code, rec.Body.String())
	}
}
