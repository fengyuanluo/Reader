package fetcher

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"reader/internal/domain/fetch"
)

func TestHTTPFetcherBuildsMethodQueryBodyAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Fatalf("page query = %q", got)
		}
		if got := r.Header.Get("X-Source"); got != "reader" {
			t.Fatalf("X-Source = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "keyword=abc" {
			t.Fatalf("body = %q", body)
		}
		w.Header().Set("X-Reply", "ok")
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	resp, err := fetcher.Fetch(context.Background(), fetch.Request{
		URL:     server.URL + "/search?existing=1",
		Method:  http.MethodPost,
		Query:   map[string]string{"page": "2"},
		Headers: map[string]string{"X-Source": "reader", "Content-Type": "application/x-www-form-urlencoded"},
		Body:    []byte("keyword=abc"),
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(resp.Body) != "accepted" || resp.Headers.Get("X-Reply") != "ok" {
		t.Fatalf("response = %#v body=%q", resp, resp.Body)
	}
}

func TestHTTPFetcherReadsGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Fatalf("Accept-Encoding = %q, want gzip", got)
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte("compressed"))
		_ = gz.Close()
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	resp, err := fetcher.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "compressed" {
		t.Fatalf("body = %q", resp.Body)
	}
}
