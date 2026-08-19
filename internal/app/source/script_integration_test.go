package sourceapp

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/script"
)

func TestScriptIntegrationParsesHeaderJSON(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{}))
	headers, err := integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `{"User-Agent":"Reader","X-Test":"1"}`})
	if err != nil {
		t.Fatalf("HeaderMap() error = %v", err)
	}
	want := map[string]string{"User-Agent": "Reader", "X-Test": "1"}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("headers = %#v, want %#v", headers, want)
	}
}

func TestScriptIntegrationParsesHeaderJS(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
	headers, err := integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `@js:JSON.stringify({"X-Token":"abc"})`})
	if err != nil {
		t.Fatalf("HeaderMap() error = %v", err)
	}
	if headers["X-Token"] != "abc" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestScriptIntegrationHandlesUnsupportedSourceProxyExplicitly(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{}))

	headers, err := integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `@js:JSON.stringify({"User-Agent":"Reader","proxy":""})`})
	if err != nil {
		t.Fatalf("HeaderMap(empty proxy) error = %v", err)
	}
	if len(headers) != 1 || headers["User-Agent"] != "Reader" {
		t.Fatalf("headers = %#v", headers)
	}

	_, err = integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `@js:JSON.stringify({"proxy":"http://127.0.0.1:8080"})`})
	if !errors.Is(err, ErrUnsupportedSourceProxy) {
		t.Fatalf("HeaderMap(proxy) error = %v", err)
	}
}

func TestScriptIntegrationMergesLoginHeaderCompatState(t *testing.T) {
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second})
	src := &domainsource.BookSource{
		BookSourceURL: "https://source.example.test/root",
		Header:        `{"User-Agent":"Reader"}`,
	}
	if _, err := runtime.EvalWithBindings(context.Background(), `source.putLoginHeader('{"Authorization":"Bearer ok","Cookie":"sid=1"}')`, script.Bindings{"source": script.SourceBindings(src), "__source": src}); err != nil {
		t.Fatalf("seed login header error = %v", err)
	}
	integration := NewScriptIntegration(runtime)
	headers, err := integration.HeaderMap(context.Background(), src)
	if err != nil {
		t.Fatalf("HeaderMap() error = %v", err)
	}
	if headers["User-Agent"] != "Reader" || headers["Authorization"] != "Bearer ok" || headers["Cookie"] != "sid=1" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestScriptIntegrationLoginCheckModifiesBody(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second, Stage: "login_check_js"}))
	src := &domainsource.BookSource{LoginCheckJS: `result.replace("old", "new")`}
	resp := &fetch.Response{StatusCode: 200, Body: []byte("old body")}

	got, err := integration.LoginCheck(context.Background(), src, resp)
	if err != nil {
		t.Fatalf("LoginCheck() error = %v", err)
	}
	if string(got.Body) != "new body" {
		t.Fatalf("body = %q", got.Body)
	}
}

func TestScriptIntegrationLoginCheckReturnsError(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second, Stage: "login_check_js"}))
	src := &domainsource.BookSource{LoginCheckJS: `({error:"need login"})`}
	resp := &fetch.Response{StatusCode: 200, Body: []byte("body")}

	_, err := integration.LoginCheck(context.Background(), src, resp)
	if !errors.Is(err, ErrLoginCheckFailed) {
		t.Fatalf("error = %v, want ErrLoginCheckFailed", err)
	}
}

func TestScriptIntegrationLoginCheckTimeout(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: 10 * time.Millisecond, Stage: "login_check_js"}))
	src := &domainsource.BookSource{LoginCheckJS: `while (true) {}`}
	resp := &fetch.Response{StatusCode: 200, Body: []byte("body")}

	_, err := integration.LoginCheck(context.Background(), src, resp)
	if !errors.Is(err, script.ErrScriptTimeout) {
		t.Fatalf("error = %v, want ErrScriptTimeout", err)
	}
}

func TestScriptIntegrationLoginCheckExposesResponseBaseURLAndCache(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second, Stage: "login_check_js"}))
	src := &domainsource.BookSource{
		BookSourceURL: "https://source.example.test/#group",
		LoginCheckJS: `
cache.put("temporary", "value", 600);
var checked = [baseUrl, result.code(), result.url(), result.body(), java.getResponse().headers().get("x-userid"), java.getStrResponse(null, null).body()].join("|");
cache.deleteMemory("temporary");
checked + "|" + String(cache.getFromMemory("temporary"))
`,
	}
	resp := &fetch.Response{
		StatusCode: http.StatusAccepted,
		URL:        "https://source.example.test/search?q=test",
		Headers:    http.Header{"X-Userid": {"42"}},
		Body:       []byte("body"),
	}

	got, err := integration.LoginCheck(context.Background(), src, resp)
	if err != nil {
		t.Fatalf("LoginCheck() error = %v", err)
	}
	want := "https://source.example.test/|202|https://source.example.test/search?q=test|body|42|body|null"
	if string(got.Body) != want {
		t.Fatalf("body = %q, want %q", got.Body, want)
	}
}
