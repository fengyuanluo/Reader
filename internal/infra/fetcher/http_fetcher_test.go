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

func TestHTTPFetcherSniffsCharsetWithoutContentType(t *testing.T) {
	gbkHTML := func(text string) []byte {
		var raw bytes.Buffer
		writer := transform.NewWriter(&raw, simplifiedchinese.GBK.NewEncoder())
		_, _ = io.WriteString(writer, text)
		_ = writer.Close()
		return raw.Bytes()
	}
	utf16 := []byte{0xFF, 0xFE}
	for _, r := range "<p>章节</p>" {
		utf16 = append(utf16, byte(r), byte(r>>8))
	}
	binary := []byte{0x89, 'P', 'N', 'G', 0xC4, 0xE3}
	tests := []struct {
		name string
		body []byte
		want []byte
	}{
		{name: "meta charset", body: gbkHTML(`<html><head><meta charset="gbk"></head><body>民间故事</body></html>`), want: []byte("民间故事")},
		{name: "http-equiv charset", body: gbkHTML(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=GB2312"></head><body>第一章</body></html>`), want: []byte("第一章")},
		{name: "utf-16 bom", body: utf16, want: []byte("章节")},
		{name: "binary untouched", body: binary, want: binary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// nil 值阻止 net/http 自动嗅探并补 Content-Type。
				w.Header()["Content-Type"] = nil
				_, _ = w.Write(tt.body)
			}))
			defer server.Close()

			f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
			resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if resp.Headers.Get("Content-Type") != "" {
				t.Fatalf("test server sent Content-Type %q", resp.Headers.Get("Content-Type"))
			}
			if !bytes.Contains(resp.Body, tt.want) {
				t.Fatalf("body = %q, want contains %q", resp.Body, tt.want)
			}
		})
	}
}

func TestHTTPFetcherDropsBodyForGET(t *testing.T) {
	tests := []struct {
		method   string
		wantBody string
	}{
		{method: http.MethodGet, wantBody: ""},
		{method: http.MethodPost, wantBody: "a=1"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			var gotBody string
			var gotLength int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				gotLength = r.ContentLength
			}))
			defer server.Close()

			f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
			if _, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, Method: tt.method, Body: []byte("a=1")}); err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if gotBody != tt.wantBody || gotLength != int64(len(tt.wantBody)) {
				t.Fatalf("body = %q (length %d), want %q", gotBody, gotLength, tt.wantBody)
			}
		})
	}
}
