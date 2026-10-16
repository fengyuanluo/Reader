package fetcher

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reader/internal/domain/fetch"
)

func TestHTTPFetcherTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithTimeout(5*time.Millisecond))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil {
		t.Fatal("Fetch() error is nil, want timeout")
	}
}

func TestHTTPFetcherOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("too-large"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithMaxBodyBytes(3))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("Fetch() error = %v, want too large", err)
	}
}

func TestHTTPFetcherTooManyRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithMaxRedirects(1))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("Fetch() error = %v, want too many redirects", err)
	}
}

// dialRecorder 把所有拨号重定向到本地测试服务器，并记录 fetcher 实际选择的目标地址。
type dialRecorder struct {
	target string
	mu     sync.Mutex
	addrs  []string
}

func (d *dialRecorder) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	d.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, d.target)
}

func (d *dialRecorder) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addrs...)
}

func staticResolver(table map[string]string) func(context.Context, string) ([]net.IP, error) {
	return func(_ context.Context, host string) ([]net.IP, error) {
		ip, ok := table[host]
		if !ok {
			return nil, errors.New("unexpected host " + host)
		}
		return []net.IP{net.ParseIP(ip)}, nil
	}
}

func serverPort(t *testing.T, server *httptest.Server) string {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Port()
}

func TestHTTPFetcherDialsValidatedIPDespiteDNSRebinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 关闭连接，迫使第二次请求重新拨号并重新解析。
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	port := serverPort(t, server)

	var calls atomic.Int32
	resolver := func(_ context.Context, host string) ([]net.IP, error) {
		if calls.Add(1) == 1 {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	recorder := &dialRecorder{target: server.Listener.Addr().String()}
	f := NewHTTPFetcher(WithResolver(resolver))
	f.dial = recorder.dial
	target := "http://rebind.example.test:" + port + "/"

	if _, err := f.Fetch(context.Background(), fetch.Request{URL: target}); err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	if _, err := f.Fetch(context.Background(), fetch.Request{URL: target}); !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("second Fetch() error = %v, want ErrBlocked", err)
	}
	want := []string{net.JoinHostPort("93.184.216.34", port)}
	if got := recorder.dialed(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dialed = %v, want %v", got, want)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("resolver calls = %d, want one per dial", got)
	}
}

func TestHTTPFetcherBlocksPrivateTargets(t *testing.T) {
	var port string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://metadata.example.test:"+port+"/latest", http.StatusFound)
	}))
	defer server.Close()
	port = serverPort(t, server)

	tests := []struct {
		name     string
		resolver func(context.Context, string) ([]net.IP, error)
		req      fetch.Request
		dialed   []string
	}{
		{
			name: "redirect to metadata address",
			resolver: staticResolver(map[string]string{
				"public.example.test":   "93.184.216.34",
				"metadata.example.test": "169.254.169.254",
			}),
			req:    fetch.Request{URL: "http://public.example.test:" + port + "/start"},
			dialed: []string{net.JoinHostPort("93.184.216.34", port)},
		},
		{
			name: "mixed public and private resolution",
			resolver: func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1")}, nil
			},
			req: fetch.Request{URL: "http://mixed.example.test:" + port + "/"},
		},
		{
			name:     "literal loopback",
			resolver: staticResolver(nil),
			req:      fetch.Request{URL: "http://127.0.0.1:" + port + "/"},
		},
		{
			name:     "dnsIp pinned to private address",
			resolver: staticResolver(map[string]string{"pinned.example.test": "93.184.216.34"}),
			req:      fetch.Request{URL: "http://pinned.example.test:" + port + "/", DNSIP: "192.168.1.1"},
		},
		{
			name:     "proxy on loopback",
			resolver: staticResolver(map[string]string{"public.example.test": "93.184.216.34"}),
			req:      fetch.Request{URL: "http://public.example.test/", Proxy: "http://127.0.0.1:" + port},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &dialRecorder{target: server.Listener.Addr().String()}
			f := NewHTTPFetcher(WithResolver(tt.resolver))
			f.dial = recorder.dial
			_, err := f.Fetch(context.Background(), tt.req)
			if !errors.Is(err, fetch.ErrBlocked) {
				t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
			}
			if got := recorder.dialed(); strings.Join(got, ",") != strings.Join(tt.dialed, ",") {
				t.Fatalf("dialed = %v, want %v", got, tt.dialed)
			}
		})
	}
}

func TestHTTPFetcherDNSIPPinsAddressAndKeepsHost(t *testing.T) {
	var gotHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	port := serverPort(t, server)

	recorder := &dialRecorder{target: server.Listener.Addr().String()}
	f := NewHTTPFetcher(WithResolver(staticResolver(map[string]string{"book.example.test": "93.184.216.34"})))
	f.dial = recorder.dial
	if _, err := f.Fetch(context.Background(), fetch.Request{URL: "http://book.example.test:" + port + "/", DNSIP: "93.184.216.99"}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if want := net.JoinHostPort("93.184.216.99", port); strings.Join(recorder.dialed(), ",") != want {
		t.Fatalf("dialed = %v, want %s", recorder.dialed(), want)
	}
	if want := "book.example.test:" + port; gotHost != want {
		t.Fatalf("Host = %q, want %q", gotHost, want)
	}
	// 同一 fetcher 的非固定请求不能复用固定路由的连接。
	if _, err := f.Fetch(context.Background(), fetch.Request{URL: "http://book.example.test:" + port + "/"}); err != nil {
		t.Fatalf("unpinned Fetch() error = %v", err)
	}
	if got := recorder.dialed(); len(got) != 2 || got[1] != net.JoinHostPort("93.184.216.34", port) {
		t.Fatalf("dialed = %v, want unpinned request to use resolved address", got)
	}
}

func TestHTTPFetcherUsesRequestProxyWithLegadoAuth(t *testing.T) {
	var gotAuth, gotURI string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		gotURI = r.RequestURI
		_, _ = w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()
	proxyAddr := proxy.Listener.Addr().String()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: "http://book.example.test/chapter?id=1", Proxy: "http://" + proxyAddr + "@reader@p@ss"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "via proxy" {
		t.Fatalf("body = %q", resp.Body)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("reader:p@ss")); gotAuth != want {
		t.Fatalf("Proxy-Authorization = %q, want %q", gotAuth, want)
	}
	if gotURI != "http://book.example.test/chapter?id=1" {
		t.Fatalf("proxy request URI = %q", gotURI)
	}
}

func TestHTTPFetcherBlocksProxiedRedirectToPrivateHost(t *testing.T) {
	var hops atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, "http://metadata.example.test/latest", http.StatusFound)
	}))
	defer proxy.Close()
	port := serverPort(t, proxy)

	recorder := &dialRecorder{target: proxy.Listener.Addr().String()}
	f := NewHTTPFetcher(WithResolver(staticResolver(map[string]string{
		"proxy.example.test":    "93.184.216.10",
		"public.example.test":   "93.184.216.34",
		"metadata.example.test": "169.254.169.254",
	})))
	f.dial = recorder.dial
	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://public.example.test/start", Proxy: "http://proxy.example.test:" + port})
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
	}
	if got := hops.Load(); got != 1 {
		t.Fatalf("proxy hops = %d, want redirect to be blocked before sending", got)
	}
}

func TestParseProxy(t *testing.T) {
	tests := []struct {
		raw      string
		want     string
		user     string
		password string
		wantErr  string
	}{
		{raw: "http://127.0.0.1:7890", want: "http://127.0.0.1:7890"},
		{raw: "HTTP://proxy.test:8080@u@p", want: "http://proxy.test:8080", user: "u", password: "p"},
		{raw: "socks5://proxy.test:1080@user@pa:ss", want: "socks5://proxy.test:1080", user: "user", password: "pa:ss"},
		{raw: "socks5://user:pw@proxy.test:1080", want: "socks5://proxy.test:1080", user: "user", password: "pw"},
		{raw: "http://[::1]:8080@u@p", want: "http://[::1]:8080", user: "u", password: "p"},
		{raw: "http://proxy.test:8080@@", want: "http://proxy.test:8080"},
		{raw: "socks4://proxy.test:1080", wantErr: "socks4"},
		{raw: "ftp://proxy.test:21", wantErr: "unsupported scheme"},
		{raw: "http://proxy.test", wantErr: "host and port"},
		{raw: "proxy.test:8080", wantErr: "unsupported scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseProxy(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseProxy() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProxy() error = %v", err)
			}
			user := got.User
			got.User = nil
			if got.String() != tt.want {
				t.Fatalf("parseProxy() = %s, want %s", got, tt.want)
			}
			password, _ := user.Password()
			if user.Username() != tt.user || password != tt.password {
				t.Fatalf("credentials = %q/%q, want %q/%q", user.Username(), password, tt.user, tt.password)
			}
		})
	}
}

func TestHTTPFetcherRejectsInvalidRoute(t *testing.T) {
	f := NewHTTPFetcher()
	tests := []fetch.Request{
		{URL: "http://a.example.test/", Proxy: "http://p.test:1", DNSIP: "93.184.216.34"},
		{URL: "http://a.example.test/", DNSIP: "not-an-ip"},
		{URL: "http://a.example.test/", Proxy: "socks4://p.test:1080"},
	}
	for _, req := range tests {
		if _, err := f.Fetch(context.Background(), req); err == nil {
			t.Fatalf("Fetch(%+v) error is nil", req)
		}
	}
}

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"10.0.0.1", true},
		{"100.64.0.1", true},
		{"169.254.169.254", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"198.18.0.1", true},
		{"224.0.0.1", true},
		{"255.255.255.255", true},
		{"::1", true},
		{"::", true},
		{"::ffff:127.0.0.1", true},
		{"fe80::1", true},
		{"fd00::1", true},
		{"fec0::1", true},
		{"ff02::1", true},
		{"64:ff9b::7f00:1", true},
		{"64:ff9b::a9fe:a9fe", true},
		{"64:ff9b:1::1", true},
		{"2001:db8::1", true},
		{"2002:7f00:1::1", true},
		{"8.8.8.8", false},
		{"93.184.216.34", false},
		{"::ffff:8.8.8.8", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tt := range tests {
		if got := isBlockedIP(net.ParseIP(tt.ip)); got != tt.blocked {
			t.Errorf("isBlockedIP(%s) = %v, want %v", tt.ip, got, tt.blocked)
		}
	}
}
