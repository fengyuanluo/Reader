package sourceapp

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
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

// 书源请求头里的 proxy 键是代理配置而非 HTTP 头（Legado AnalyzeUrl.kt:128-131）：零值丢弃，
// 非空值保留在请求头映射里，由 requestoption.Build 构建请求时移出为 fetch.Request.Proxy。
func TestScriptIntegrationKeepsSourceProxyHeaderForRequestBuild(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{}))

	headers, err := integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `@js:JSON.stringify({"User-Agent":"Reader","proxy":""})`})
	if err != nil {
		t.Fatalf("HeaderMap(empty proxy) error = %v", err)
	}
	if len(headers) != 1 || headers["User-Agent"] != "Reader" {
		t.Fatalf("headers = %#v", headers)
	}

	headers, err = integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `@js:JSON.stringify({"proxy":"http://127.0.0.1:8080"})`})
	if err != nil {
		t.Fatalf("HeaderMap(proxy) error = %v", err)
	}
	if headers["proxy"] != "http://127.0.0.1:8080" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestScriptIntegrationMergesLoginHeaderCompatState(t *testing.T) {
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second})
	src := &domainsource.BookSource{
		BookSourceURL: "https://source.example.test/root",
		Header:        `{"User-Agent":"Reader"}`,
	}
	if _, err := runtime.EvalWithBindings(context.Background(), `source.putLoginHeader('{"Authorization":"Bearer ok","Cookie":"sid=1"}')`, script.Bindings{"source": src}); err != nil {
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
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
	src := &domainsource.BookSource{LoginCheckJS: `result.body.replace("old", "new")`}
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
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
	src := &domainsource.BookSource{LoginCheckJS: `({error:"need login"})`}
	resp := &fetch.Response{StatusCode: 200, Body: []byte("body")}

	_, err := integration.LoginCheck(context.Background(), src, resp)
	if !errors.Is(err, ErrLoginCheckFailed) {
		t.Fatalf("error = %v, want ErrLoginCheckFailed", err)
	}
}

func TestScriptIntegrationLoginCheckTimeout(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: 10 * time.Millisecond}))
	src := &domainsource.BookSource{SourceID: "src-login", LoginCheckJS: `while (true) {}`}
	resp := &fetch.Response{StatusCode: 200, Body: []byte("body")}

	_, err := integration.LoginCheck(script.WithStage(context.Background(), "search"), src, resp)
	if !errors.Is(err, script.ErrScriptTimeout) {
		t.Fatalf("error = %v, want ErrScriptTimeout", err)
	}
	if !strings.Contains(err.Error(), "stage=search.login_check_js") || !strings.Contains(err.Error(), "source=src-login") {
		t.Fatalf("error = %v, want caller stage and source id", err)
	}
}

func TestScriptIntegrationLoginCheckExposesResponseBaseURLAndCache(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
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
		Headers:    fetch.Headers{"X-Userid": {"42"}},
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

// TestScriptIntegrationLoginCheckNilRuntimeSkipsScript 验证 runtime 为 nil 时 LoginCheck
// 原样返回响应而非 panic，与 HeaderMap 的 nil 守卫语义一致（S15-03）。
func TestScriptIntegrationLoginCheckNilRuntimeSkipsScript(t *testing.T) {
	integration := NewScriptIntegration(nil)
	src := &domainsource.BookSource{
		BookSourceURL: "https://source.example.test",
		LoginCheckJS:  "result.body()",
	}
	resp := &fetch.Response{StatusCode: http.StatusOK, Body: []byte("body")}
	got, err := integration.LoginCheck(context.Background(), src, resp)
	if err != nil {
		t.Fatalf("LoginCheck() error = %v", err)
	}
	if string(got.Body) != "body" {
		t.Fatalf("body = %q, want original body", got.Body)
	}
}

// TestScriptIntegrationLoginRejectsPlainURLLoginUrl 验证 loginUrl 为纯 URL 且 loginUi 非空时
// 返回 ErrUnsupportedLoginFlow 而非把 URL 当脚本文本执行（S15-04）。
func TestScriptIntegrationLoginRejectsPlainURLLoginUrl(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
	src := &domainsource.BookSource{
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "URL Login Source",
		LoginURL:       "https://login.example.test/signin",
		LoginUI:        `[{"name":"username","type":"text"}]`,
	}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	err := integration.Login(context.Background(), src, map[string]string{"username": "u"})
	if !errors.Is(err, ErrUnsupportedLoginFlow) {
		t.Fatalf("Login() error = %v, want ErrUnsupportedLoginFlow", err)
	}
}

// TestScriptIntegrationLoginAcceptsBareJSText 验证无前缀的裸 JS loginUrl 仍按脚本执行（Legado 语义）。
func TestScriptIntegrationLoginAcceptsBareJSText(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
	src := &domainsource.BookSource{
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "Bare JS Source",
		LoginURL:       `var ok = 1;`,
		LoginUI:        `[{"name":"username","type":"text"}]`,
	}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// 裸 JS 没有 login 函数，应得到脚本层面的错误而非 ErrUnsupportedLoginFlow。
	err := integration.Login(context.Background(), src, map[string]string{"username": "u"})
	if errors.Is(err, ErrUnsupportedLoginFlow) {
		t.Fatalf("Login() error = %v, bare JS should be executed as script", err)
	}
}

// TestScriptIntegrationLooseHeaderKeepsCase 验证 loose（非严格 JSON）头对象经
// urlrule.ParseHeaderObject 解析后头名保持原大小写（User-Agent 等），后键覆盖前键。
func TestScriptIntegrationLooseHeaderKeepsCase(t *testing.T) {
	integration := NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second}))
	headers, err := integration.HeaderMap(context.Background(), &domainsource.BookSource{Header: `@js:JSON.parse('{"User-Agent":"Reader","X-Token":"first","X-Token":"second"}')`})
	if err != nil {
		t.Fatalf("HeaderMap() error = %v", err)
	}
	if headers["User-Agent"] != "Reader" {
		t.Fatalf("headers = %#v, want User-Agent kept in original case", headers)
	}
	if headers["X-Token"] != "second" {
		t.Fatalf("headers = %#v, want later duplicate key to win", headers)
	}
}
