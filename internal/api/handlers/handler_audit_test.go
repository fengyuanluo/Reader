package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/infra/script"
	"reader/internal/infra/sqlite/repositories"
)

func apiRequest(handler http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer secret")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func newLimitedSourceHandlerTestRouter(t *testing.T, jsonLimit, importLimit int64) http.Handler {
	t.Helper()
	db := openHandlerTestDB(t)
	handler := NewSourceHandler(sourceapp.NewManager(repositories.NewBookSourceRepository(db))).WithBodyLimits(jsonLimit, importLimit)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r))
}

// TestBookSourceUnknownFieldsRoundTrip 覆盖 R-039（不拒绝 spec 未声明的字段）与 R-020（GET/PATCH 回写保留它们）。
func TestBookSourceUnknownFieldsRoundTrip(t *testing.T) {
	router := newSourceHandlerTestRouter(t)
	createBody := `{
		"sourceId":"",
		"bookSourceUrl":"https://legado.example.test/source",
		"bookSourceName":"Legado",
		"bookSourceType":0,
		"customOrder":0,
		"enabled":true,
		"enabledExplore":true,
		"enabledCookieJar":true,
		"lastUpdateTime":10,
		"ruleSearch":{"bookList":".item"},
		"legadoFutureField":{"nested":true},
		"legadoComment":"keep me"
	}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	sourceID, _ := created["sourceId"].(string)
	if sourceID == "" {
		t.Fatalf("created = %#v", created)
	}
	if created["legadoComment"] != "keep me" {
		t.Fatalf("create response dropped unknown field: %#v", created)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/book-sources/"+sourceID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
	}
	var fetched map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched["legadoComment"] != "keep me" {
		t.Fatalf("GET dropped unknown field: %#v", fetched)
	}
	nested, ok := fetched["legadoFutureField"].(map[string]any)
	if !ok || nested["nested"] != true {
		t.Fatalf("GET dropped nested unknown field: %#v", fetched["legadoFutureField"])
	}
	if fetched["bookSourceName"] != "Legado" {
		t.Fatalf("GET lost known field: %#v", fetched)
	}

	updateBody := strings.Replace(createBody, `"bookSourceName":"Legado"`, `"bookSourceName":"Renamed"`, 1)
	updateBody = strings.Replace(updateBody, `"sourceId":""`, `"sourceId":"`+sourceID+`"`, 1)
	rec = authedRequest(router, http.MethodPut, "/api/v1/book-sources/"+sourceID, updateBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = authedRequest(router, http.MethodGet, "/api/v1/book-sources/"+sourceID, "")
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated get: %v", err)
	}
	if updated["bookSourceName"] != "Renamed" || updated["legadoComment"] != "keep me" {
		t.Fatalf("update round trip = %#v", updated)
	}
}

// TestBookSourceUpdateConflictReturns409 覆盖 R-032：sourceId 与路径不一致返回 409 而不是 400/500。
func TestBookSourceUpdateConflictReturns409(t *testing.T) {
	router, repo := newSourceHandlerTestRouterAndRepo(t)
	src := handlerTestSource(t, "https://conflict.example.test/source", "Conflict")
	if err := repo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}

	body := `{"sourceId":"src_other","bookSourceUrl":"https://conflict.example.test/source","bookSourceName":"Conflict","bookSourceType":0,"customOrder":0,"enabled":true,"enabledExplore":true,"enabledCookieJar":true,"lastUpdateTime":10}`
	rec := authedRequest(router, http.MethodPut, "/api/v1/book-sources/"+src.SourceID, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "source_conflict")
}

// TestImportBookSourcesWithoutFetcherReturns501 覆盖 R-105：装配缺失用 501 与真实内部错误区分。
func TestImportBookSourcesWithoutFetcherReturns501(t *testing.T) {
	router, _ := newImportHandlerTestRouter(t, nil)
	body := `{"payload":{"sourceUrls":["https://remote.example.test/sources.json"]}}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources/import", body)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s, want 501", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "not_configured")
}

// TestRequestBodyLimitReturns413 覆盖 R-078：请求体超限返回 413。
func TestRequestBodyLimitReturns413(t *testing.T) {
	router := newLimitedSourceHandlerTestRouter(t, 64, 1024)
	body := `{"sourceId":"","bookSourceUrl":"https://limit.example.test/source","bookSourceName":"Limit","bookSourceType":0,"customOrder":0,"enabled":true,"enabledExplore":true,"enabledCookieJar":true,"lastUpdateTime":10}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s, want 413", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "payload_too_large")
}

// TestJSONContentTypeRequired 覆盖 R-120：非 application/json 的请求体直接拒绝。
func TestJSONContentTypeRequired(t *testing.T) {
	router := newSourceHandlerTestRouter(t)
	body := `{"sourceId":"","bookSourceUrl":"https://ct.example.test/source","bookSourceName":"CT","bookSourceType":0,"customOrder":0,"enabled":true,"enabledExplore":true,"enabledCookieJar":true,"lastUpdateTime":10}`

	rec := apiRequest(router, http.MethodPost, "/api/v1/book-sources", body, "text/plain")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")

	rec = apiRequest(router, http.MethodPost, "/api/v1/book-sources", body, "application/json; charset=utf-8")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status with charset = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
}

// TestPageSizeUpperBoundRejected 覆盖 R-081：pageSize 超过 spec maximum 时返回 400。
func TestPageSizeUpperBoundRejected(t *testing.T) {
	sourceRouter := newSourceHandlerTestRouter(t)
	rec := authedRequest(sourceRouter, http.MethodGet, "/api/v1/book-sources?page=1&pageSize=201", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("book-sources status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	rec = authedRequest(sourceRouter, http.MethodGet, "/api/v1/book-sources?page=1&pageSize=200", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("book-sources status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	bookRouter, _, _ := newBookHandlerTestRouter(t)
	rec = authedRequest(bookRouter, http.MethodGet, "/api/v1/books?page=1&pageSize=201", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("books status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

// TestGetOpenAPIReturnsYAML 覆盖 R-096：GET /openapi.yaml 返回 YAML 且 Content-Type 是 application/yaml。
func TestGetOpenAPIReturnsYAML(t *testing.T) {
	router := newSourceHandlerTestRouter(t)
	rec := authedRequest(router, http.MethodGet, "/api/v1/openapi.yaml", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Fatalf("Content-Type = %q, want application/yaml", ct)
	}
	body := rec.Body.Bytes()
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "{") {
		t.Fatalf("response looks like JSON: %s", trimmed[:min(40, len(trimmed))])
	}
	doc, err := openapi3.NewLoader().LoadFromData(body)
	if err != nil {
		t.Fatalf("served document is not valid OpenAPI YAML: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "Reader API" {
		t.Fatalf("served document info = %#v", doc.Info)
	}
	if doc.Paths.Find("/book-sources/{sourceId}/login") == nil {
		t.Fatal("served document is missing the login path")
	}
}

// TestSearchHasMoreAndWarningTrace 覆盖 R-019（HasMore 反映实际结果）与 R-100（逐源警告带 traceId）。
func TestSearchHasMoreAndWarningTrace(t *testing.T) {
	searcher := &fakeAPISearcher{result: searchapp.MultiSearchResult{
		Items:    []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", BookURL: "https://book.example.test/1"}},
		Warnings: []searchapp.SourceWarning{{SourceID: "src_1", BookSourceURL: "https://s.example.test", Code: "search_failed", Message: "boom"}},
		HasMore:  true,
	}}
	router, _ := newSearchHandlerTestRouter(t, searcher)

	rec := authedRequest(router, http.MethodPost, "/api/v1/search", `{"keyword":"斗破","page":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response generated.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !response.HasMore {
		t.Fatalf("hasMore = false, want true: %#v", response)
	}
	if len(response.Warnings) != 1 || response.Warnings[0].TraceId == nil {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
	if want := rec.Header().Get(middleware.TraceHeader); *response.Warnings[0].TraceId != want {
		t.Fatalf("warning traceId = %q, want %q", *response.Warnings[0].TraceId, want)
	}
	if searcher.input.Page != 2 {
		t.Fatalf("searcher input = %#v", searcher.input)
	}

	emptyRouter, _ := newSearchHandlerTestRouter(t, &fakeAPISearcher{})
	rec = authedRequest(emptyRouter, http.MethodPost, "/api/v1/search", `{"keyword":"斗破"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty search status = %d body=%s", rec.Code, rec.Body.String())
	}
	var emptyResponse generated.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyResponse); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if emptyResponse.HasMore {
		t.Fatalf("hasMore = true for empty page: %#v", emptyResponse)
	}
}

// TestSearchWithoutServiceReturns501 覆盖 R-105。
func TestSearchWithoutServiceReturns501(t *testing.T) {
	router := newHandlerTestRouter(t, nil, nil)
	rec := authedRequest(router, http.MethodPost, "/api/v1/search", `{"keyword":"斗破"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s, want 501", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "not_configured")
}

// TestBookMetadataRoundTrip 覆盖 R-017：spec 声明的 Legado 书籍字段不再被静默丢弃。
func TestBookMetadataRoundTrip(t *testing.T) {
	router, _, _ := newBookHandlerTestRouter(t)
	createBody := `{
		"bookUrl":"https://site.example.test/book/meta",
		"origin":"https://source.example.test",
		"originName":"Source",
		"name":"斗破苍穹",
		"author":"天蚕土豆",
		"type":0,
		"kind":"玄幻",
		"customTag":"我的标签",
		"coverUrl":"https://img.example.test/cover.jpg",
		"customCoverUrl":"https://img.example.test/custom.jpg",
		"intro":"原始简介",
		"customIntro":"自定义简介",
		"charset":"GBK",
		"group":7,
		"latestChapterTime":1700000000,
		"lastCheckTime":1700000001,
		"lastCheckCount":3,
		"totalChapterNum":1200,
		"canUpdate":false,
		"order":5,
		"syncTime":1700000002,
		"originOrder":2
	}`
	rec := authedRequest(router, http.MethodPost, "/api/v1/books", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	assertBookMetadata(t, created)

	rec = authedRequest(router, http.MethodGet, "/api/v1/books/"+repositoryBookID(created.BookUrl), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
	}
	var fetched generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetched: %v", err)
	}
	assertBookMetadata(t, fetched)
}

func assertBookMetadata(t *testing.T, dto generated.Book) {
	t.Helper()
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"customTag", dto.CustomTag, "我的标签"},
		{"customCoverUrl", dto.CustomCoverUrl, "https://img.example.test/custom.jpg"},
		{"customIntro", dto.CustomIntro, "自定义简介"},
		{"charset", dto.Charset, "GBK"},
		{"group", dto.Group, int64(7)},
		{"latestChapterTime", dto.LatestChapterTime, int64(1700000000)},
		{"lastCheckTime", dto.LastCheckTime, int64(1700000001)},
		{"lastCheckCount", dto.LastCheckCount, 3},
		{"totalChapterNum", dto.TotalChapterNum, 1200},
		{"canUpdate", dto.CanUpdate, false},
		{"order", dto.Order, 5},
		{"syncTime", dto.SyncTime, int64(1700000002)},
	}
	for _, tc := range cases {
		switch got := tc.got.(type) {
		case *string:
			if got == nil || *got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.name, derefString(got), tc.want)
			}
		case *int64:
			if got == nil || *got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.name, derefInt64(got), tc.want)
			}
		case *int:
			if got == nil || *got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.name, derefInt(got), tc.want)
			}
		case *bool:
			if got == nil || *got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.name, derefBool(got), tc.want)
			}
		default:
			t.Fatalf("%s has unsupported type %T", tc.name, tc.got)
		}
	}
}

func derefString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func derefInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func derefInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func derefBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

// TestBookProgressIdentityValidation 覆盖 R-145：name/author 参与校验而不是被静默忽略。
func TestBookProgressIdentityValidation(t *testing.T) {
	router, _, _ := newBookHandlerTestRouter(t)
	createBody := `{"bookUrl":"https://site.example.test/book/progress","origin":"https://source.example.test","originName":"Source","name":"斗破苍穹","author":"天蚕土豆","type":0}`
	rec := authedRequest(router, http.MethodPost, "/api/v1/books", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	bookID := repositoryBookID(created.BookUrl)

	rec = authedRequest(router, http.MethodPut, "/api/v1/books/"+bookID+"/progress", `{"name":"另一本书","author":"天蚕土豆","durChapterIndex":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched progress status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")

	rec = authedRequest(router, http.MethodPut, "/api/v1/books/"+bookID+"/progress", `{"name":"","author":"","durChapterIndex":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing identity status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = authedRequest(router, http.MethodPut, "/api/v1/books/"+bookID+"/progress", `{"name":"斗破苍穹","author":"天蚕土豆","durChapterIndex":2,"durChapterPos":30}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching progress status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

// TestLoginEndpointsPersistAndClearState 覆盖 R-103。
func TestLoginEndpointsPersistAndClearState(t *testing.T) {
	router, repo := newLoginHandlerTestRouter(t)
	src := handlerTestSource(t, "https://login.example.test/source", "Login")
	src.LoginUI = `[{"name":"username","type":"text"}]`
	src.LoginURL = `@js:function login() { var info = source.getLoginInfoMap(); source.putLoginHeader(JSON.stringify({Cookie: "sid=" + info.username})); }`
	if err := repo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources/"+src.SourceID+"/login", `{"values":{"username":"u1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", rec.Code, rec.Body.String())
	}
	var state generated.BookSourceLoginState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if !state.LoggedIn || !state.HasLoginUrl || !state.HasLoginUi || state.LoginInfo == nil || (*state.LoginInfo)["username"] != "u1" {
		t.Fatalf("state = %#v", state)
	}
	if state.LoginHeader == nil || (*state.LoginHeader)["Cookie"] != "sid=u1" {
		t.Fatalf("loginHeader = %#v", state.LoginHeader)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/book-sources/"+src.SourceID+"/login", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("query status = %d body=%s", rec.Code, rec.Body.String())
	}
	var queried generated.BookSourceLoginState
	if err := json.Unmarshal(rec.Body.Bytes(), &queried); err != nil {
		t.Fatalf("decode queried: %v", err)
	}
	if !queried.LoggedIn || queried.LoginHeader == nil || (*queried.LoginHeader)["Cookie"] != "sid=u1" {
		t.Fatalf("queried = %#v", queried)
	}

	rec = authedRequest(router, http.MethodDelete, "/api/v1/book-sources/"+src.SourceID+"/login", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = authedRequest(router, http.MethodGet, "/api/v1/book-sources/"+src.SourceID+"/login", "")
	var cleared generated.BookSourceLoginState
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatalf("decode cleared: %v", err)
	}
	if cleared.LoggedIn || cleared.LoginInfo != nil || cleared.LoginHeader != nil {
		t.Fatalf("cleared = %#v", cleared)
	}

	webview := handlerTestSource(t, "https://login.example.test/webview", "WebView")
	webview.LoginURL = "https://login.example.test/signin"
	if err := repo.Save(context.Background(), webview); err != nil {
		t.Fatalf("save webview source: %v", err)
	}
	rec = authedRequest(router, http.MethodPost, "/api/v1/book-sources/"+webview.SourceID+"/login", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("webview login status = %d body=%s, want 501", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "unsupported_login_flow")
}

func newLoginHandlerTestRouter(t *testing.T) (http.Handler, *repositories.BookSourceRepository) {
	t.Helper()
	db := openHandlerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	runtime := script.NewRuntime(script.Policy{
		Timeout:   time.Second,
		Variables: repositories.NewSourceVariableRepository(db),
	})
	handler := NewSourceHandler(sourceapp.NewManager(repo)).WithLogin(sourceapp.NewLoginService(repo, sourceapp.NewScriptIntegration(runtime)))
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), repo
}
