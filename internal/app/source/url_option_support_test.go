package sourceapp

import (
	"errors"
	"testing"

	"reader/internal/domain/fetch"
)

func TestValidateSupportedURLOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    map[string]any
		wantErr bool
	}{
		{name: "core options", opts: map[string]any{"method": "POST", "body": "a=1", "headers": map[string]any{}, "charset": "gbk", "retry": float64(2)}},
		{name: "network options", opts: map[string]any{"proxy": "http://127.0.0.1:7890", "dnsIp": "1.2.3.4"}},
		{name: "ignored legado options", opts: map[string]any{"serverID": "1", "webJs": "x", "webViewDelayTime": "500"}},
		{name: "falsy webView", opts: map[string]any{"webView": false}},
		{name: "webView", opts: map[string]any{"webView": true}, wantErr: true},
		{name: "js", opts: map[string]any{"js": "url"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSupportedURLOptions(tt.opts)
			if tt.wantErr != errors.Is(err, ErrUnsupportedURLOption) || (!tt.wantErr && err != nil) {
				t.Fatalf("ValidateSupportedURLOptions() error = %v, wantErr %v", err, tt.wantErr)
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

// LooksLikeFormBody / ApplyRetryOption 是书源、搜索、正文各抓取链路共用的唯一实现（R-121）。
func TestLooksLikeFormBody(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{body: "", want: false},
		{body: "  ", want: false},
		{body: `{"a":1}`, want: false},
		{body: `[1,2]`, want: false},
		{body: `<xml/>`, want: false},
		{body: "a=1&b=2", want: true},
		{body: "  a=1  ", want: true},
		{body: "chapterId=1", want: true},
	}
	for _, tt := range tests {
		if got := LooksLikeFormBody(tt.body); got != tt.want {
			t.Fatalf("LooksLikeFormBody(%q) = %v, want %v", tt.body, got, tt.want)
		}
	}
}

func TestApplyRetryOption(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int
	}{
		{name: "float", value: float64(3), want: 3},
		{name: "int", value: 4, want: 4},
		{name: "int64", value: int64(5), want: 5},
		{name: "string", value: "6", want: 6},
		{name: "blank string", value: " ", want: 0},
		{name: "invalid string", value: "x", want: 0},
		{name: "non positive", value: float64(0), want: 0},
		{name: "bool ignored", value: true, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fetch.Request{}
			ApplyRetryOption(&req, tt.value)
			if req.Retry != tt.want {
				t.Fatalf("Retry = %d, want %d", req.Retry, tt.want)
			}
		})
	}
}
