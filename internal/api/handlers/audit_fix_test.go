package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/task"
	"reader/internal/infra/sqlite/repositories"
	checkworker "reader/internal/worker/check"

	"github.com/go-chi/chi/v5"
)

// S10-03：导入接口的 default 分支不把内部错误原文回显给客户端。
func TestImportErrorHidesInternalErrorText(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)

	writeImportError(rec, req, errors.New("dial tcp 10.0.0.1:443: connect: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body generated.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "import_failed" {
		t.Fatalf("code = %q, want import_failed", body.Code)
	}
	if body.Message != "import failed" {
		t.Fatalf("message = %q, want fixed internal message", body.Message)
	}
}

// S10-03：图片代理的 default 分支不把 fetch 层错误原文（含内网 IP）回显给客户端。
func TestImageProxyErrorHidesInternalErrorText(t *testing.T) {
	router := newImageProxyTestRouter(t, &fakeImageProxyFetcher{err: errors.New("dial tcp 10.0.0.1:443: connect: connection refused")})

	rec := authedRequest(router, http.MethodGet, "/api/v1/images/proxy?url="+url.QueryEscape("https://img.example.test/a.png"), "")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body generated.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if strings.Contains(body.Message, "dial tcp") {
		t.Fatalf("message leaks internal error text: %q", body.Message)
	}
	if body.Message != "image fetch failed" {
		t.Fatalf("message = %q, want fixed internal message", body.Message)
	}
}

// S10-03：脚本解码失败不回显书源脚本片段。
func TestImageDecodeErrorHidesScriptText(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/images/proxy", nil)

	writeImageProxyError(rec, req, errors.Join(contentapp.ErrImageDecodeFailed, errors.New("ReferenceError: secretVar is not defined at <js> ...")))

	var body generated.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if strings.Contains(body.Message, "ReferenceError") {
		t.Fatalf("message leaks script error text: %q", body.Message)
	}
	if body.Message != "image decode script failed" {
		t.Fatalf("message = %q, want fixed internal message", body.Message)
	}
}

// S10-03：未知脚本失败走 default 分支，不回显内部错误原文。
func TestLoginUnknownErrorHidesInternalText(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/src/login", nil)

	writeLoginError(rec, req, errors.New("dial tcp 10.0.0.1:5432: connect: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body generated.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if strings.Contains(body.Message, "dial tcp") {
		t.Fatalf("message leaks internal error text: %q", body.Message)
	}
	if body.Message != "internal error" {
		t.Fatalf("message = %q, want fixed internal message", body.Message)
	}
}

// S10-04：查询参数绑定错误返回 Error schema 的 JSON 400（含 traceId），而非 text/plain。
func TestWriteParamErrorReturnsJSONErrorSchema(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/books?page=abc", nil)
	req = req.WithContext(middleware.ContextWithTraceID(req.Context(), "0123456789abcdef"))

	WriteParamError(rec, req, &generated.InvalidParamFormatError{ParamName: "page", Err: errors.New("invalid value")})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	var body generated.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "validation_failed" || body.TraceId != "0123456789abcdef" {
		t.Fatalf("body = %#v", body)
	}
	if rec.Header().Get(middleware.TraceHeader) != "0123456789abcdef" {
		t.Fatal("trace header missing")
	}
}

// S18-09：GET /openapi.yaml 返回内嵌的 api/openapi.yaml 原文（字段级保真，非再模型化版本）。
func TestGetOpenAPIReturnsEmbeddedSpecVerbatim(t *testing.T) {
	specOnDisk, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read embedded spec: %v", err)
	}
	if len(specOnDisk) == 0 {
		t.Fatal("embedded spec is empty")
	}
	repo := newAuditSourceRepo(t)
	handler := NewSourceHandler(sourceapp.NewManager(repo))

	rec := httptest.NewRecorder()
	handler.GetOpenAPI(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/yaml") {
		t.Fatalf("content-type = %q, want application/yaml", got)
	}
	if !strings.Contains(rec.Body.String(), "openapi: 3.") {
		t.Fatal("body is not an OpenAPI document")
	}
	// 返回体与仓库真相源逐字节一致（embed 自 api/openapi.yaml）。
	if rec.Body.String() != string(specOnDisk) {
		t.Fatal("returned body differs from embedded spec")
	}
}

// S18-02：/version 返回可注入的 version 变量。
func TestGetVersionUsesVersionVariable(t *testing.T) {
	repo := newAuditSourceRepo(t)
	handler := NewSourceHandler(sourceapp.NewManager(repo))

	rec := httptest.NewRecorder()
	handler.GetVersion(rec, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))

	var body generated.VersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Version != version {
		t.Fatalf("version = %q, want %q", body.Version, version)
	}
	if body.Version == "" {
		t.Fatal("version is empty")
	}
}

// S10-06：check 事件流输出 id 字段且支持 Last-Event-ID 断线续传，不重复重放。
func TestCheckTaskEventsStreamWritesEventIDAndHonorsLastEventID(t *testing.T) {
	db := openHandlerTestDB(t)
	sourceRepo := newAuditSourceRepo(t)
	checkRepo := repositories.NewCheckRepository(db)
	events := checkapp.NewPublisher()
	checker := checkapp.NewChecker(&fakeAPISearcher{}, &fakeAPIDiscovery{}, &fakeCheckAPIBooks{}, &fakeCheckAPIContent{})
	runner := checkworker.NewRunner(checkRepo, checker).WithSourceLoader(sourceRepo).WithEvents(events)
	checkManager := checkapp.NewManager(checkRepo, sourceRepo, runner).WithEvents(events)
	handler := NewSourceHandler(sourceapp.NewManager(sourceRepo)).WithCheck(checkManager)
	handler.heartbeat = 10 * time.Millisecond
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	server := httptest.NewServer(middleware.Trace(middleware.Auth("secret")(r)))
	defer server.Close()

	// Get 需要持久化任务行：先建一条已收敛任务，再直接发布事件到事件流（两条历史 + 一条终态）。
	seededTask := &task.CheckTask{
		TaskID:    "sse-task",
		Status:    task.CheckTaskSucceeded,
		CreatedAt: time.Now().UTC(),
		Total:     2,
		Finished:  2,
	}
	if err := checkRepo.CreateTask(context.Background(), seededTask); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	events.Open("sse-task")
	events.Publish(&task.CheckEvent{TaskID: "sse-task", Type: task.CheckEventItemPassed, SourceID: "src_sse", EventID: 1})
	events.Publish(&task.CheckEvent{TaskID: "sse-task", Type: task.CheckEventItemPassed, SourceID: "src_sse", EventID: 2})
	events.Publish(&task.CheckEvent{TaskID: "sse-task", Type: task.CheckEventCompleted, SourceID: "src_sse", EventID: 3})

	// 首次订阅：三条事件都带 id:。
	streamReq, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/check/tasks/sse-task/events", nil)
	streamReq.Header.Set("Authorization", "Bearer secret")
	stream, err := server.Client().Do(streamReq)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer stream.Body.Close()
	first := newSSEStream(stream.Body)
	first.until(t, "id: 1", 5*time.Second)
	first.until(t, "id: 3", 5*time.Second)
	first.untilEOF(t, 5*time.Second)

	// 断线续传：Last-Event-ID: 1 只应收到 id: 2 与 id: 3，不重复重放 id: 1。
	resumeReq, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/check/tasks/sse-task/events", nil)
	resumeReq.Header.Set("Authorization", "Bearer secret")
	resumeReq.Header.Set("Last-Event-ID", "1")
	resume, err := server.Client().Do(resumeReq)
	if err != nil {
		t.Fatalf("GET events with Last-Event-ID: %v", err)
	}
	defer resume.Body.Close()
	second := newSSEStream(resume.Body)
	second.until(t, "id: 2", 5*time.Second)
	second.until(t, "id: 3", 5*time.Second)
	second.untilEOF(t, 5*time.Second)
}

// S10-10 / S17-07：导出文件名清洗与 Content-Disposition 构造行为不变（回归保护）。
func TestExportFilenameAndDisposition(t *testing.T) {
	if got := exportFilename(`书名:卷一/章?`); got != "书名_卷一_章_.txt" {
		t.Fatalf("exportFilename = %q", got)
	}
	if got := exportFilename("   "); got != "book.txt" {
		t.Fatalf("exportFilename empty = %q", got)
	}
	d := exportContentDisposition("书名.txt")
	if !strings.HasPrefix(d, `attachment; filename="__.txt"`) || !strings.Contains(d, "filename*=UTF-8''%E4%B9%A6%E5%90%8D.txt") {
		t.Fatalf("exportContentDisposition = %q", d)
	}
}

// S10-11：check 请求 sourceIds 超过上限时快速返回 400，不进入逐条查询。
func TestCheckTaskCreateRejectsTooManySourceIds(t *testing.T) {
	repo := newAuditSourceRepo(t)
	checkRepo := repositories.NewCheckRepository(openHandlerTestDB(t))
	checker := checkapp.NewChecker(&fakeAPISearcher{}, &fakeAPIDiscovery{}, &fakeCheckAPIBooks{}, &fakeCheckAPIContent{})
	checkManager := checkapp.NewManager(checkRepo, repo, checkworker.NewRunner(checkRepo, checker).WithSourceLoader(repo))
	handler := NewSourceHandler(sourceapp.NewManager(repo)).WithCheck(checkManager)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	router := middleware.Trace(middleware.Auth("secret")(r))

	ids := make([]string, 0, maxSourceIds+1)
	for i := 0; i <= maxSourceIds; i++ {
		ids = append(ids, fmt.Sprintf("src_%08d", i))
	}
	payload, err := json.Marshal(map[string]any{"sourceIds": ids})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rec := authedRequest(router, http.MethodPost, "/api/v1/check/book-sources", string(payload))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

// S10-11：search 请求 sourceIds 超过上限时返回 400。
func TestSearchBooksRejectsTooManySourceIds(t *testing.T) {
	repo := newAuditSourceRepo(t)
	searcher := &fakeAPISearcher{}
	handler := NewSourceHandler(sourceapp.NewManager(repo)).WithSearcher(searcher)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	router := middleware.Trace(middleware.Auth("secret")(r))

	ids := make([]string, 0, maxSourceIds+1)
	for i := 0; i <= maxSourceIds; i++ {
		ids = append(ids, fmt.Sprintf("src_%08d", i))
	}
	payload, err := json.Marshal(map[string]any{"keyword": "斗破", "sourceIds": ids})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rec := authedRequest(router, http.MethodPost, "/api/v1/search", string(payload))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
	if searcher.input.Keyword != "" {
		t.Fatal("searcher must not be called when sourceIds exceed the limit")
	}
}

func newAuditSourceRepo(t *testing.T) *repositories.BookSourceRepository {
	t.Helper()
	return repositories.NewBookSourceRepository(openHandlerTestDB(t))
}
