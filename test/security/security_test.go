package security_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	replaceapp "reader/internal/app/replace"
	"reader/internal/domain/fetch"
	"reader/internal/domain/replace"
	"reader/internal/infra/fetcher"
	"reader/internal/infra/logging"
	"reader/internal/infra/script"
)

func TestFetcherBlocksPrivateAddressSSRF(t *testing.T) {
	tests := []struct {
		name string
		ips  []string
	}{
		{name: "loopback", ips: []string{"127.0.0.1"}},
		{name: "metadata", ips: []string{"169.254.169.254"}},
		{name: "nat64 mapped loopback", ips: []string{"64:ff9b::7f00:1"}},
		{name: "public mixed with private", ips: []string{"93.184.216.34", "10.0.0.1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := func(context.Context, string) ([]net.IP, error) {
				out := make([]net.IP, 0, len(tt.ips))
				for _, ip := range tt.ips {
					out = append(out, net.ParseIP(ip))
				}
				return out, nil
			}
			f := fetcher.NewHTTPFetcher(fetcher.WithResolver(resolver))

			_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://private.example.test/secret"})
			if !errors.Is(err, fetch.ErrBlocked) {
				t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
			}
		})
	}
}

func TestFetcherBlocksPrivateProxyAndDNSIPSSRF(t *testing.T) {
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	f := fetcher.NewHTTPFetcher(fetcher.WithResolver(resolver))
	for _, req := range []fetch.Request{
		{URL: "http://public.example.test/", Proxy: "http://127.0.0.1:8080"},
		{URL: "http://public.example.test/", DNSIP: "169.254.169.254"},
	} {
		if _, err := f.Fetch(context.Background(), req); !errors.Is(err, fetch.ErrBlocked) {
			t.Fatalf("Fetch(%+v) error = %v, want ErrBlocked", req, err)
		}
	}
}

func TestFetcherIgnoresEnvironmentProxy(t *testing.T) {
	var proxied atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Store(true)
		_, _ = w.Write([]byte("proxy"))
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct"))
	}))
	defer target.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)

	_, port, err := net.SplitHostPort(target.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// 使用非 localhost 域名：Go 对 localhost 本就不走环境代理，无法体现差异。
	resolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	f := fetcher.NewHTTPFetcher(fetcher.WithAllowPrivateNetwork(true), fetcher.WithResolver(resolver))
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: "http://target.example.test:" + port + "/"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "direct" || proxied.Load() {
		t.Fatalf("body = %q, proxied = %v; want direct connection ignoring HTTP_PROXY", resp.Body, proxied.Load())
	}
}

func TestScriptInfiniteLoopTimesOut(t *testing.T) {
	runtime := script.NewRuntime(script.Policy{Timeout: 10 * time.Millisecond, Stage: "security"})

	_, err := runtime.Eval(context.Background(), `while (true) {}`, nil)
	if !errors.Is(err, script.ErrScriptTimeout) {
		t.Fatalf("Eval() error = %v, want ErrScriptTimeout", err)
	}
}

func TestScriptCannotAccessHostEscapePrimitives(t *testing.T) {
	runtime := script.NewRuntime(script.Policy{})

	got, err := runtime.Eval(context.Background(), `typeof require + "," + typeof process + "," + typeof fetch + "," + typeof Deno`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "undefined,undefined,undefined,undefined" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRegexReplacementRejectsOversizedInput(t *testing.T) {
	text := strings.Repeat("a", 2*1024*1024+1)

	_, err := (&replaceapp.Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "a", Replacement: "b", IsRegex: true}, text)
	if !errors.Is(err, replaceapp.ErrRegexLimitExceeded) {
		t.Fatalf("Test() error = %v, want ErrRegexLimitExceeded", err)
	}
}

func TestLogRedactionMasksSensitiveValues(t *testing.T) {
	message := logging.RedactString("Authorization: Bearer abc\nProxy-Authorization: Basic cHc=\nX-Api-Key: k-1\nCookie: sid=secret\nSet-Cookie: refresh=secret; Path=/\nGET https://u:pw1@api.example.test/s?apikey=k-2&password=pw2&token=secret")
	for _, secret := range []string{"abc", "cHc=", "k-1", "sid=secret", "refresh=secret", "pw1", "k-2", "pw2", "token=secret"} {
		if strings.Contains(message, secret) {
			t.Fatalf("redacted message still contains %q: %s", secret, message)
		}
	}
}
