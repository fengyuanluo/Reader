package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reader/internal/api/middleware"
	"testing"
	"time"

	"reader/internal/infra/config"
)

func TestNewServerSetsBaselineHTTPServerOptions(t *testing.T) {
	server := newServer(":0")

	if server.Addr != ":0" {
		t.Fatalf("server Addr = %q, want :0", server.Addr)
	}
	if server.Handler == nil {
		t.Fatal("server Handler is nil")
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 5s", server.ReadHeaderTimeout)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(middleware.TraceHeader); got == "" {
		t.Fatal("trace header is empty")
	}
}

func TestNewServerProtectsBusinessRoutes(t *testing.T) {
	server := newServerWithToken(":0", "secret")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)
	rec := httptest.NewRecorder()

	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get(middleware.TraceHeader); got == "" {
		t.Fatal("trace header is empty")
	}
}

func TestNewRuntimeServerConstrainsSQLiteConnections(t *testing.T) {
	dbPath := t.TempDir() + "/reader.db"
	cfg := config.Config{
		Addr:              "127.0.0.1:0",
		SQLiteDSN:         dbPath,
		Token:             "secret",
		Env:               config.EnvDevelopment,
		DefaultUserAgent:  "Reader/Test",
		FetchTimeout:      30 * time.Second,
		FetchMaxBodyBytes: 10 * 1024 * 1024,
		FetchMaxRedirects: 5,
		GlobalConcurrency: 8,
		SourceConcurrency: 2,
		ScriptTimeout:     10 * time.Second,
		RegexTimeout:      500 * time.Millisecond,
		DebugBodyLimit:    32 * 1024,
	}

	server, cleanup, err := newRuntimeServer(cfg)
	if err != nil {
		t.Fatalf("newRuntimeServer() error = %v", err)
	}
	defer cleanup()
	if server == nil {
		t.Fatal("server is nil")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("sqlite db not created: %v", err)
	}
}
