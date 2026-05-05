package fetcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"reader/internal/domain/fetch"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestHTTPFetcherSendsDefaultUserAgent(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	if _, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if gotUA != "Reader-Test" {
		t.Fatalf("User-Agent = %q, want Reader-Test", gotUA)
	}
}

func TestHTTPFetcherRequestWithoutUA(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	if _, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, RequestWithoutUA: true}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if gotUA != "" {
		t.Fatalf("User-Agent = %q, want empty", gotUA)
	}
}

func TestHTTPFetcherDecompressesGzipBodyWithoutEncodingHeader(t *testing.T) {
	var body bytes.Buffer
	gzipWriter := gzip.NewWriter(&body)
	_, _ = gzipWriter.Write([]byte(`{"ok":true}`))
	_ = gzipWriter.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body.Bytes())
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Fatalf("body = %q", string(resp.Body))
	}
}

func TestHTTPFetcherBlocksPrivateByDefault(t *testing.T) {
	f := NewHTTPFetcher()
	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://127.0.0.1/source.json"})
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
	}
}

func TestHTTPFetcherAllowsNonSuccessResponsesWhenRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"missing"}`))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, AllowNonSuccess: true})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if resp.StatusCode != http.StatusNotFound || string(resp.Body) != `{"code":404,"message":"missing"}` {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestHTTPFetcherRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`temporary`))
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, Retry: 2})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q", string(resp.Body))
	}
}

func TestHTTPFetcherDecodesDataURI(t *testing.T) {
	f := NewHTTPFetcher()
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: "data:;base64,L3NlYXJjaD9xPWJvb2s="})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "/search?q=book" {
		t.Fatalf("body = %q", string(resp.Body))
	}
}

func TestHTTPFetcherEncodesResponseBodyAsHexWhenRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("book"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, EncodeBodyAsHex: true})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != hex.EncodeToString([]byte("book")) {
		t.Fatalf("body = %q", string(resp.Body))
	}
}

func TestHTTPFetcherDecodesGB2312HTMLToUTF8(t *testing.T) {
	var raw bytes.Buffer
	writer := transform.NewWriter(&raw, simplifiedchinese.GBK.NewEncoder())
	_, _ = io.WriteString(writer, `<html><body><div class="g">民间故事</div></body></html>`)
	_ = writer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gb2312")
		_, _ = w.Write(raw.Bytes())
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithDefaultUserAgent("Reader-Test"))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !bytes.Contains(resp.Body, []byte("民间故事")) {
		t.Fatalf("body = %q", string(resp.Body))
	}
}
