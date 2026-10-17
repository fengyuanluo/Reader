package fetcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reader/internal/domain/fetch"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestDecodeTextResponseBodySniffsMetaCharsetWithoutHeaderParam(t *testing.T) {
	gbk := func(text string) []byte {
		var raw bytes.Buffer
		writer := transform.NewWriter(&raw, simplifiedchinese.GBK.NewEncoder())
		_, _ = io.WriteString(writer, text)
		_ = writer.Close()
		return raw.Bytes()
	}
	utf8BOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`<html><body>序章</body></html>`)...)

	tests := []struct {
		name        string
		body        []byte
		contentType string
		want        []byte
	}{
		{
			name:        "gbk html with meta charset and no header charset",
			body:        gbk(`<html><head><meta charset="gbk"></head><body>民间故事</body></html>`),
			contentType: "text/html",
			want:        []byte(`<html><head><meta charset="gbk"></head><body>民间故事</body></html>`),
		},
		{
			name:        "gb2312 http-equiv without header charset",
			body:        gbk(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=GB2312"></head><body>第一章</body></html>`),
			contentType: "text/html",
			want:        []byte(`<html><head><meta http-equiv="Content-Type" content="text/html; charset=GB2312"></head><body>第一章</body></html>`),
		},
		{
			name:        "utf-8 bom without header charset",
			body:        utf8BOM,
			contentType: "text/html",
			want:        []byte("\ufeff" + `<html><body>序章</body></html>`),
		},
		{
			name:        "explicit header charset still wins over meta",
			body:        gbk(`<html><head><meta charset="gbk"></head><body>第二章</body></html>`),
			contentType: "text/html; charset=gbk",
			want:        []byte(`<html><head><meta charset="gbk"></head><body>第二章</body></html>`),
		},
		{
			name:        "text without any declaration untouched",
			body:        []byte("plain"),
			contentType: "text/html",
			want:        []byte("plain"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeTextResponseBody(tt.body, tt.contentType, 1<<20)
			if err != nil {
				t.Fatalf("decodeTextResponseBody() error = %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("decoded = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPFetcherSniffsCharsetWithContentTypeHeaderMissingCharset(t *testing.T) {
	var raw bytes.Buffer
	writer := transform.NewWriter(&raw, simplifiedchinese.GBK.NewEncoder())
	_, _ = io.WriteString(writer, `<html><head><meta charset="gbk"></head><body>民间故事</body></html>`)
	_ = writer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 显式设置不带 charset 参数的 Content-Type，阻止 net/http 嗅探补全。
		w.Header()["Content-Type"] = []string{"text/html"}
		_, _ = w.Write(raw.Bytes())
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !bytes.Contains(resp.Body, []byte("民间故事")) {
		t.Fatalf("body = %q, want decoded GBK text", string(resp.Body))
	}
}

func TestCachedResolvePropagatesResolverErrorWithoutCaching(t *testing.T) {
	boom := errors.New("dns: temporary failure")
	var calls atomic.Int32
	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithResolver(func(ctx context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		return nil, boom
	}))
	defer f.clearDNSCache()

	for range 2 {
		_, err := f.allowedIPs(context.Background(), "missing.example.test")
		if err == nil {
			t.Fatal("allowedIPs() error = nil, want resolver error")
		}
		if !errors.Is(err, boom) {
			t.Fatalf("allowedIPs() error = %v, want wrapped dns: temporary failure", err)
		}
		if !strings.Contains(err.Error(), "dns: temporary failure") {
			t.Fatalf("error %q missing original cause", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("resolver calls = %d, want 2 (errors must not be cached)", calls.Load())
	}

	// context.Canceled 也不被吞掉。
	canceled := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithResolver(func(ctx context.Context, host string) ([]net.IP, error) {
		return nil, context.Canceled
	}))
	defer canceled.clearDNSCache()
	_, err := canceled.allowedIPs(context.Background(), "missing.example.test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

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

// TestHTTPFetcherConvertsResponseHeadersToDomainType 覆盖 R-098：传输层响应头在适配层转成
// domain 的 fetch.Headers，且 Get 的键匹配语义与 net/http.Header 一致（大小写不敏感）。
func TestHTTPFetcherConvertsResponseHeadersToDomainType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace-Id", "trace-1")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := resp.Headers.Get("X-Trace-Id"); got != "trace-1" {
		t.Fatalf("Get(X-Trace-Id) = %q, want trace-1", got)
	}
	if got := resp.Headers.Get("x-trace-id"); got != "trace-1" {
		t.Fatalf("Get(x-trace-id) = %q, want trace-1 (canonical key lookup)", got)
	}
	if got := resp.Headers.Get("Missing"); got != "" {
		t.Fatalf("Get(Missing) = %q, want empty", got)
	}
}

func TestHTTPFetcherRetriesWithBackoff(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	start := time.Now()
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, Retry: 1, AllowNonSuccess: false})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q", string(resp.Body))
	}
	// 第一次失败后应至少退避 100ms 再重试。
	if elapsed < 100*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 100ms backoff", elapsed)
	}
}

func TestHTTPFetcherResolverCachedPerHost(t *testing.T) {
	var calls atomic.Int32
	resolver := func(_ context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 关闭连接，迫使每次请求重新拨号。
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	port := serverPort(t, server)

	recorder := &dialRecorder{target: server.Listener.Addr().String()}
	f := NewHTTPFetcher(WithResolver(resolver))
	f.dial = recorder.dial
	target := "http://cache.example.test:" + port + "/"
	for i := 0; i < 3; i++ {
		if _, err := f.Fetch(context.Background(), fetch.Request{URL: target}); err != nil {
			t.Fatalf("Fetch() #%d error = %v", i, err)
		}
	}
	// 同一 host 连续拨号只解析一次 DNS（缓存 TTL 60s 内）。
	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
}

func TestHTTPFetcherCachedDNSStillBlocksPrivateIP(t *testing.T) {
	var calls atomic.Int32
	resolver := func(_ context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	f := NewHTTPFetcher(WithResolver(resolver))
	for i := 0; i < 2; i++ {
		if _, err := f.Fetch(context.Background(), fetch.Request{URL: "http://blocked.example.test/x"}); !errors.Is(err, fetch.ErrBlocked) {
			t.Fatalf("Fetch() #%d error = %v, want ErrBlocked", i, err)
		}
	}
	// 缓存命中后 SSRF 校验仍对当次使用的 IP 执行：两次都被拦截，且第二次未重新解析。
	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1 (cached)", got)
	}
}

func TestHTTPFetcherDropsInvalidHeaderNameAndSanitizesValue(t *testing.T) {
	var gotUA, gotXTag, gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotXTag = r.Header.Get("X-Tag")
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	// U+2011 NON-BREAKING HYPHEN 的 key 不是合法 RFC 7230 token，应被跳过而不是让整个请求失败。
	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	resp, err := f.Fetch(context.Background(), fetch.Request{
		URL: server.URL,
		Headers: map[string]string{
			"User\u2011Agent": "evil",
			"X-Tag":           "good\r\nInjected: 1",
			"Cookie":          "sid=abc",
		},
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q", string(resp.Body))
	}
	// 非法 header 未被发出（传输层未报错且值不是请求方的）。
	if gotUA == "evil" {
		t.Fatalf("invalid U+2011 header was sent")
	}
	// 合法 header 正常到达，value 中的 \r\n 被去除。
	if gotXTag != "goodInjected: 1" {
		t.Fatalf("X-Tag = %q, want CRLF stripped", gotXTag)
	}
	if gotCookie != "sid=abc" {
		t.Fatalf("Cookie = %q, want sid=abc", gotCookie)
	}
}
