package fetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"reader/internal/domain/fetch"
)

func TestHTTPFetcherAppliesConcurrentRatePerSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	req := fetch.Request{URL: server.URL, SourceKey: "a", ConcurrentRate: "1/60"}
	if _, err := f.Fetch(context.Background(), req); err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	started := time.Now()
	if _, err := f.Fetch(context.Background(), req); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 45*time.Millisecond {
		t.Fatalf("second request waited %s, want at least 45ms", elapsed)
	}

	started = time.Now()
	if _, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, SourceKey: "b", ConcurrentRate: "1/60"}); err != nil {
		t.Fatalf("other source Fetch() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 40*time.Millisecond {
		t.Fatalf("other source unexpectedly waited %s", elapsed)
	}
}

func TestHTTPFetcherConcurrentRateWaitHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	req := fetch.Request{URL: server.URL, SourceKey: "source", ConcurrentRate: "1/5000"}
	if _, err := f.Fetch(context.Background(), req); err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := f.Fetch(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fetch() error = %v, want deadline exceeded", err)
	}
}
