package requestoption

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"reader/internal/domain/fetch"
)

func TestBuildAppliesLegadoRequestOptions(t *testing.T) {
	eval := func(_ context.Context, script string, input any) (any, error) {
		if script != `result + "?signed=1"` {
			t.Fatalf("script = %q", script)
		}
		if input != "https://example.test/api" {
			t.Fatalf("input = %#v", input)
		}
		return "https://example.test/api?signed=1", nil
	}
	req, err := Build(context.Background(), `/api,{"method":"POST","body":"q=中文","charset":"UTF-8","headers":{"X-Test":"ok"},"origin":"https://origin.test","retry":2,"js":"result + \"?signed=1\"","bodyJs":"result.toUpperCase()","webView":true}`, "https://example.test/base", fetch.Request{
		Headers:        map[string]string{"X-Base": "base"},
		UseCookieJar:   true,
		SourceKey:      "source-key",
		ConcurrentRate: "2/1000",
	}, eval)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if req.URL != "https://example.test/api?signed=1" || req.Method != http.MethodPost {
		t.Fatalf("request target = %s %s", req.Method, req.URL)
	}
	if string(req.Body) != "q=%E4%B8%AD%E6%96%87" {
		t.Fatalf("body = %q", req.Body)
	}
	if req.Headers["X-Test"] != "ok" || req.Headers["X-Base"] != "base" || req.Headers["Origin"] != "https://origin.test" || req.Headers["Referer"] != "https://origin.test" {
		t.Fatalf("headers = %#v", req.Headers)
	}
	if req.Retry != 2 || req.BodyJS != "result.toUpperCase()" || !req.WebViewFallback {
		t.Fatalf("request options = %#v", req)
	}
}

// webView 生效（远端新增 WebViewFallback），webJs 只接受不生效：两者都不再按不支持报错。
func TestBuildAcceptsWebViewAndIgnoresWebJS(t *testing.T) {
	req, err := Build(context.Background(), `https://example.test,{"webView":true,"webJs":"document.body.innerText"}`, "", fetch.Request{}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !req.WebViewFallback || req.BodyJS != "" {
		t.Fatalf("request = %#v", req)
	}
}

// 未知选项在非零值时按不支持报错，零值（null / 空串 / false / 0 / 空容器）放行。
func TestBuildRejectsNonZeroUnknownOptions(t *testing.T) {
	if _, err := Build(context.Background(), `https://example.test,{"Content-Type":"text/plain","referer":"https://ref.test","zdy":"legacy"}`, "", fetch.Request{}, nil); !errors.Is(err, ErrUnsupportedURLOption) {
		t.Fatalf("Build() error = %v", err)
	}
	req, err := Build(context.Background(), `https://example.test,{"zdy":"","useWebView":false,"legacy":null}`, "", fetch.Request{}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if req.URL != "https://example.test" || len(req.Headers) != 0 {
		t.Fatalf("request = %#v", req)
	}
}

// proxy / dnsIp 生效并写入请求；serverID / webViewDelayTime / webJs 接受但不改变请求。
func TestBuildAppliesNetworkOptionsAndIgnoresBrowserOnlyOptions(t *testing.T) {
	proxyReq, err := Build(context.Background(), `https://example.test,{"proxy":"http://127.0.0.1:8080"}`, "", fetch.Request{}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if proxyReq.Proxy != "http://127.0.0.1:8080" {
		t.Fatalf("proxy = %q", proxyReq.Proxy)
	}
	dnsReq, err := Build(context.Background(), `https://example.test,{"dnsIp":"1.1.1.1"}`, "", fetch.Request{}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if dnsReq.DNSIP != "1.1.1.1" {
		t.Fatalf("dnsip = %q", dnsReq.DNSIP)
	}
	ignored, err := Build(context.Background(), `https://example.test,{"webViewDelayTime":1000,"serverID":"1","webJs":"document.body.innerText"}`, "", fetch.Request{}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if ignored.WebViewFallback || ignored.DNSIP != "" || ignored.BodyJS != "" || len(ignored.Headers) != 0 {
		t.Fatalf("request = %#v", ignored)
	}
}

// 书源请求头里的 proxy 键是代理配置而非 HTTP 头（Legado AnalyzeUrl.kt:128-131）：移出请求头，
// URL 选项 proxy 优先于请求头里的配置。
func TestBuildMovesSourceHeaderProxyIntoRequest(t *testing.T) {
	req, err := Build(context.Background(), `https://example.test/api,{"dnsIp":"93.184.216.34"}`, "", fetch.Request{
		Headers: map[string]string{"proxy": "socks5://proxy.test:1080@u@p", "X-Test": "1"},
	}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if req.Proxy != "socks5://proxy.test:1080@u@p" || req.DNSIP != "93.184.216.34" {
		t.Fatalf("Proxy/DNSIP = %q/%q", req.Proxy, req.DNSIP)
	}
	if _, ok := req.Headers["proxy"]; ok {
		t.Fatalf("proxy leaked into headers: %#v", req.Headers)
	}

	overridden, err := Build(context.Background(), `https://example.test/api,{"proxy":"http://127.0.0.1:8888"}`, "", fetch.Request{
		Headers: map[string]string{"proxy": "socks5://proxy.test:1080"},
	}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if overridden.Proxy != "http://127.0.0.1:8888" {
		t.Fatalf("proxy = %q", overridden.Proxy)
	}
}

func TestBuildKeepsURLWhenJSReturnsNil(t *testing.T) {
	req, err := Build(context.Background(), `/api,{"js":"java.toast('loading')"}`, "https://example.test/base", fetch.Request{}, func(context.Context, string, any) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if req.URL != "https://example.test/api" {
		t.Fatalf("URL = %q", req.URL)
	}
}

func TestBuildAppliesJSBeforeCharsetEncodingAndOptionHeadersBeforeBody(t *testing.T) {
	req, err := Build(context.Background(), `/search,{"method":"POST","body":"q=中文","charset":"GBK","headers":{"content-type":"text/plain"},"js":"result + '?key=中文'"}`, "https://example.test", fetch.Request{
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	}, func(_ context.Context, _ string, input any) (any, error) {
		if input != "https://example.test/search" {
			t.Fatalf("input = %#v", input)
		}
		return "https://example.test/search?key=中文", nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if req.URL != "https://example.test/search?key=%D6%D0%CE%C4" || string(req.Body) != "q=中文" {
		t.Fatalf("request = %#v", req)
	}
	if len(req.Headers) != 1 || headerValue(req.Headers, "Content-Type") != "text/plain" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

// type 键存在且值非 null 时按 hex 二进制处理响应（对齐 Legado AnalyzeUrl），空串同样生效。
func TestBuildMarksHexEncodingForTypeKeyPresence(t *testing.T) {
	tests := []struct {
		option string
		want   bool
	}{
		{`{"type":""}`, true},
		{`{"type":null}`, false},
		{`{"type":123}`, true},
		{`{"type":"x"}`, true},
	}
	for _, tt := range tests {
		req, err := Build(context.Background(), "https://example.test,"+tt.option, "", fetch.Request{}, nil)
		if err != nil {
			t.Fatalf("Build(%s) error = %v", tt.option, err)
		}
		if req.EncodeBodyAsHex != tt.want {
			t.Fatalf("option %s: EncodeBodyAsHex = %v, want %v", tt.option, req.EncodeBodyAsHex, tt.want)
		}
	}
}

func TestValidateURLOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    map[string]any
		wantErr bool
	}{
		{name: "core options", opts: map[string]any{"method": "POST", "body": "a=1", "headers": map[string]any{}, "charset": "gbk", "retry": float64(2)}},
		{name: "network options", opts: map[string]any{"proxy": "http://127.0.0.1:7890", "dnsIp": "1.2.3.4"}},
		{name: "ignored legado options", opts: map[string]any{"serverID": "1", "webJs": "x", "webViewDelayTime": "500"}},
		{name: "webView falls back to plain HTTP", opts: map[string]any{"webView": true}},
		{name: "url script", opts: map[string]any{"js": "result", "bodyJs": "result"}},
		{name: "zero-like unknown options", opts: map[string]any{"zdy": "", "useWebView": false, "legacy": nil}},
		{name: "non-zero unknown option", opts: map[string]any{"zdy": "legacy"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.opts)
			if tt.wantErr != errors.Is(err, ErrUnsupportedURLOption) || (!tt.wantErr && err != nil) {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestApplyNetworkOptions(t *testing.T) {
	tests := []struct {
		name      string
		headers   map[string]string
		opts      map[string]any
		wantProxy string
		wantDNSIP string
	}{
		{name: "header proxy moved out", headers: map[string]string{"proxy": "http://h:1@u@p", "UA": "x"}, wantProxy: "http://h:1@u@p"},
		{name: "option overrides header", headers: map[string]string{"proxy": "http://h:1"}, opts: map[string]any{"PROXY": "socks5://s:2"}, wantProxy: "socks5://s:2"},
		{name: "dnsIp", opts: map[string]any{"dnsIp": " 1.2.3.4 "}, wantDNSIP: "1.2.3.4"},
		{name: "blank values ignored", opts: map[string]any{"proxy": " ", "dnsIp": float64(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fetch.Request{Headers: tt.headers}
			ApplyNetworkOptions(&req, tt.opts)
			if req.Proxy != tt.wantProxy || req.DNSIP != tt.wantDNSIP {
				t.Fatalf("Proxy/DNSIP = %q/%q, want %q/%q", req.Proxy, req.DNSIP, tt.wantProxy, tt.wantDNSIP)
			}
			if _, ok := req.Headers["proxy"]; ok {
				t.Fatalf("proxy still in headers: %#v", req.Headers)
			}
		})
	}
}

func TestFetchFormatsNilBodyJSResultLikeLegado(t *testing.T) {
	resp, err := Fetch(context.Background(), fetcherFunc(func(context.Context, fetch.Request) (*fetch.Response, error) {
		return &fetch.Response{StatusCode: 200, Body: []byte("before")}, nil
	}), fetch.Request{BodyJS: "null"}, func(context.Context, string, any) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "null" {
		t.Fatalf("body = %q", resp.Body)
	}
}

func TestFetchAppliesBodyJS(t *testing.T) {
	fetcher := fetcherFunc(func(context.Context, fetch.Request) (*fetch.Response, error) {
		return &fetch.Response{StatusCode: 200, Body: []byte("before"), URL: "https://example.test/final"}, nil
	})
	eval := func(_ context.Context, script string, input any) (any, error) {
		if script != "transform" || input != "before" {
			t.Fatalf("eval = %q %#v", script, input)
		}
		return "after", nil
	}
	resp, err := Fetch(context.Background(), fetcher, fetch.Request{URL: "https://example.test", BodyJS: "transform"}, eval)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(resp.Body) != "after" || resp.URL != "https://example.test/final" {
		t.Fatalf("response = %#v", resp)
	}
}

type fetcherFunc func(context.Context, fetch.Request) (*fetch.Response, error)

func (f fetcherFunc) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	return f(ctx, req)
}
