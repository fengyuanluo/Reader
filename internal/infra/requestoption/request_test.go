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

func TestBuildRejectsBrowserOnlyOptions(t *testing.T) {
	_, err := Build(context.Background(), `https://example.test,{"webView":true,"webJs":"document.body.innerText"}`, "", fetch.Request{}, nil)
	if !errors.Is(err, ErrUnsupportedURLOption) {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestBuildIgnoresUnknownOptionsLikeLegadoGson(t *testing.T) {
	req, err := Build(context.Background(), `https://example.test,{"Content-Type":"text/plain","referer":"https://ref.test","zdy":"legacy"}`, "", fetch.Request{}, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if req.URL != "https://example.test" || len(req.Headers) != 0 {
		t.Fatalf("request = %#v", req)
	}
}

func TestBuildRejectsExplicitUnsupportedNetworkAndBrowserOptions(t *testing.T) {
	for _, option := range []string{
		`{"dnsIp":"1.1.1.1"}`,
		`{"proxy":"http://127.0.0.1:8080"}`,
		`{"webViewDelayTime":1000}`,
	} {
		t.Run(option, func(t *testing.T) {
			_, err := Build(context.Background(), "https://example.test,"+option, "", fetch.Request{}, nil)
			if !errors.Is(err, ErrUnsupportedURLOption) {
				t.Fatalf("Build() error = %v", err)
			}
		})
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
