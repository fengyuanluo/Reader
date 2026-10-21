package fetcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reader/internal/domain/fetch"
)

// TestHTTPFetcherDoesNotRetryPOST 覆盖 S08-03：POST 请求失败不重试，handler 至多收到一次请求。
func TestHTTPFetcherDoesNotRetryPOST(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		// 模拟连接中断：直接劫持并断开。
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			if conn != nil {
				conn.Close()
			}
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(2 * time.Second))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, Method: "POST", Retry: 3})
	if err == nil {
		t.Fatal("expected error for POST with broken connection")
	}
	if got := atomic.LoadInt64(&hits); got > 1 {
		t.Fatalf("POST retried %d times, want at most 1", got)
	}
}

// TestHTTPFetcherDoesNotRetryTooLarge 覆盖 S08-05：超限错误直接返回，仅一次尝试。
func TestHTTPFetcherDoesNotRetryTooLarge(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Write(bytes.Repeat([]byte("a"), 2048))
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(2*time.Second), WithMaxBodyBytes(1024), WithAllowPrivateNetwork(true))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, Retry: 3})
	if err == nil || err.Error() != "response body too large" {
		t.Fatalf("want too large error, got %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("too-large error retried %d times, want 1", got)
	}
}

// TestHTTPFetcherDoesNotRetryGzipTruncation 覆盖 S08-05：gzip 截断（unexpected EOF）不重试且报 too large。
func TestHTTPFetcherDoesNotRetryGzipTruncation(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write(bytes.Repeat([]byte("a"), 20*1024*1024))
		zw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(buf.Bytes())
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(5*time.Second), WithMaxBodyBytes(1024*1024), WithAllowPrivateNetwork(true))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL, Retry: 2})
	if err == nil || err.Error() != "response body too large" {
		t.Fatalf("want too large error, got %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("gzip truncation retried %d times, want 1", got)
	}
}

// TestHTTPFetcherRejectsFileRedirect 覆盖 S08-13：重定向到非 http/https scheme 被拒绝。
func TestHTTPFetcherRejectsFileRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(2 * time.Second))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil {
		t.Fatal("expected error for file:// redirect")
	}
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}

// TestHTTPFetcherAcceptsGzipMagicFallback 覆盖 S08-04：恰好以 gzip magic 开头的非 gzip body 按原文本处理。
func TestHTTPFetcherAcceptsGzipMagicFallback(t *testing.T) {
	body := []byte{0x1f, 0x8b, 0, 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(2*time.Second), WithAllowPrivateNetwork(true))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !bytes.Equal(resp.Body, body) {
		t.Fatalf("want original bytes %v, got %v", body, resp.Body)
	}
}

// TestHTTPFetcherConcurrentResolveSingleflight 覆盖 S08-11：并发解析同一 host 只调用一次 resolver。
func TestHTTPFetcherConcurrentResolveSingleflight(t *testing.T) {
	var calls int64
	start := make(chan struct{})
	f := NewHTTPFetcher(WithResolver(func(ctx context.Context, host string) ([]net.IP, error) {
		atomic.AddInt64(&calls, 1)
		<-start
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}), WithAllowPrivateNetwork(true))
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.cachedResolve(context.Background(), "example.com"); err != nil {
				t.Errorf("cachedResolve: %v", err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(start)
	wg.Wait()
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("resolver called %d times, want 1", got)
	}
}

// TestHTTPFetcherClientCacheBounded 覆盖 S08-12：clients map 有容量上限。
func TestHTTPFetcherClientCacheBounded(t *testing.T) {
	f := NewHTTPFetcher()
	for i := range 200 {
		if _, err := f.clientFor(fetch.Request{
			URL:   fmt.Sprintf("http://host%d.example.com/x", i),
			Proxy: fmt.Sprintf("http://proxy%d.example.com:8080", i),
		}); err != nil {
			t.Fatalf("clientFor: %v", err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.clients) > maxRouteClients {
		t.Fatalf("clients map size %d exceeds cap %d", len(f.clients), maxRouteClients)
	}
	if len(f.transports) > maxRouteClients {
		t.Fatalf("transports map size %d exceeds cap %d", len(f.transports), maxRouteClients)
	}
}

// TestHTTPFetcherCloseIdleConnections 覆盖 S03-15：CloseIdleConnections 可被外部调用以释放空闲连接。
func TestHTTPFetcherCloseIdleConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(2*time.Second), WithAllowPrivateNetwork(true))
	for range 3 {
		if _, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL}); err != nil {
			t.Fatalf("fetch: %v", err)
		}
	}
	f.CloseIdleConnections() // 不应 panic；空闲连接被立即回收。
}

// TestHTTPFetcherDialerFollowsTimeout 覆盖 S08-02：dialer 超时跟随 WithTimeout 配置。
func TestHTTPFetcherDialerFollowsTimeout(t *testing.T) {
	// 不可路由地址（DROP），连接阶段会挂起直至超时。
	unreachable := "http://10.255.255.1:81/"
	f := NewHTTPFetcher(WithTimeout(100 * time.Millisecond))
	start := time.Now()
	_, err := f.Fetch(context.Background(), fetch.Request{URL: unreachable})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error for unreachable host")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("dial took %v, want fast failure under 100ms timeout", elapsed)
	}
}

// TestHTTPFetcherResponseHeaderTimeout 覆盖 S08-01：慢响应头受独立超时约束。
func TestHTTPFetcherResponseHeaderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()
	f := NewHTTPFetcher(WithTimeout(100 * time.Millisecond))
	start := time.Now()
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil {
		t.Fatal("expected timeout on slow response headers")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("slow-header timeout took %v, want fast failure", elapsed)
	}
}

// TestMergeQueryDoesNotMutateBase 覆盖 S08-09：mergeQuery 不修改传入的 base，且结果稳定。
func TestMergeQueryDoesNotMutateBase(t *testing.T) {
	base, err := url.ParseQuery("b=2&a=1")
	if err != nil {
		t.Fatal(err)
	}
	original := base.Encode()
	got := mergeQuery(base, map[string]string{"c": "3"})
	if base.Encode() != original {
		t.Fatalf("mergeQuery mutated base: %s", base.Encode())
	}
	want := "a=1&b=2&c=3"
	if got != want {
		t.Fatalf("mergeQuery = %q, want %q", got, want)
	}
}

// TestRetryableClassification 覆盖 S08-03 错误分类边界。
func TestRetryableClassification(t *testing.T) {
	if retryable(fetch.Request{Method: "POST"}, &errRequestSent{underlying: errors.New("broken pipe")}) {
		t.Fatal("POST with sent request must not retry")
	}
	if retryable(fetch.Request{}, errors.New("response body too large")) {
		t.Fatal("too large must not retry")
	}
	if retryable(fetch.Request{}, errors.New("too many redirects")) {
		t.Fatal("too many redirects must not retry")
	}
	if !retryable(fetch.Request{}, errors.New("unexpected status: 404")) {
		t.Fatal("non-2xx status retries per Legado newCallResponse semantics")
	}
	if !retryable(fetch.Request{}, &errRequestSent{underlying: errors.New("connection reset by peer")}) {
		t.Fatal("GET network error should retry")
	}
	if !retryable(fetch.Request{}, errors.New("some transient error")) {
		t.Fatal("transient non-sent error should retry")
	}
}
