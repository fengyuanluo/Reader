package handlers

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	bookapp "reader/internal/app/book"
	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	debugapp "reader/internal/app/debug"
	discoveryapp "reader/internal/app/discovery"
	replaceapp "reader/internal/app/replace"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/replace"
	"reader/internal/domain/rule"
	domainsource "reader/internal/domain/source"
	"reader/internal/domain/task"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
	checkworker "reader/internal/worker/check"

	_ "modernc.org/sqlite"
)

func TestSourceHandlersRequireBearerToken(t *testing.T) {
	router := newSourceHandlerTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/book-sources", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSourceHandlersCreateListGetAndUpdate(t *testing.T) {
	router := newSourceHandlerTestRouter(t)
	createBody := `{
		"sourceId":"",
		"bookSourceUrl":"https://example.test/source",
		"bookSourceName":"Example",
		"bookSourceGroup":"g1",
		"bookSourceType":0,
		"customOrder":7,
		"enabled":true,
		"enabledExplore":true,
		"enabledCookieJar":true,
		"lastUpdateTime":10,
		"ruleSearch":{"bookList":".item"}
	}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.BookSource
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.SourceId == "" || created.BookSourceName != "Example" {
		t.Fatalf("created = %#v", created)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/book-sources?keyword=Example&enabled=true", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list generated.BookSourceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Meta.Total != 1 || len(list.Items) != 1 || list.Items[0].SourceId != created.SourceId {
		t.Fatalf("list = %#v", list)
	}

	rec = authedRequest(router, http.MethodPatch, "/api/v1/book-sources/"+created.SourceId+"/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enabled status = %d body=%s", rec.Code, rec.Body.String())
	}
	var summary generated.BookSourceSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.Enabled {
		t.Fatalf("enabled summary = %#v", summary)
	}

	updateBody := strings.Replace(createBody, `"bookSourceName":"Example"`, `"bookSourceName":"Updated"`, 1)
	updateBody = strings.Replace(updateBody, `"sourceId":""`, `"sourceId":"`+created.SourceId+`"`, 1)
	rec = authedRequest(router, http.MethodPut, "/api/v1/book-sources/"+created.SourceId, updateBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	var updated generated.BookSource
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.BookSourceName != "Updated" {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestSourceHandlersReturnValidationError(t *testing.T) {
	router := newSourceHandlerTestRouter(t)
	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources", `{"sourceId":"","bookSourceUrl":"","bookSourceName":"","bookSourceType":0,"customOrder":0,"enabled":true,"enabledExplore":true,"enabledCookieJar":true,"lastUpdateTime":0}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

func TestImportBookSourcesHandlerImportsPayload(t *testing.T) {
	router, repo := newImportHandlerTestRouter(t, nil)
	body := `{"payload":{"bookSourceUrl":"https://import.example.test","bookSourceName":"Imported","enabled":true,"enabledExplore":false,"bookSourceType":0,"customOrder":0,"enabledCookieJar":true,"lastUpdateTime":10}}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources/import", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result generated.ImportBookSourcesResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Added != 1 || len(result.Items) != 1 || result.Items[0].Status != generated.ImportBookSourceItemResultStatusAdded {
		t.Fatalf("result = %#v", result)
	}
	if _, err := repo.FindByURL(context.Background(), "https://import.example.test"); err != nil {
		t.Fatalf("imported source not saved: %v", err)
	}
}

func TestImportBookSourcesHandlerReturnsRemoteErrors(t *testing.T) {
	router, _ := newImportHandlerTestRouter(t, &fakeImportFetcher{err: fetch.ErrBlocked})

	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources/import", `{"remoteUrl":"http://127.0.0.1/sources.json"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result generated.ImportBookSourcesResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.RemoteErrors == nil || len(*result.RemoteErrors) != 1 || (*result.RemoteErrors)[0].Error.Code != "fetch_failed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSourceHandlersReturnNotFoundAndDelete(t *testing.T) {
	router, repo := newSourceHandlerTestRouterAndRepo(t)
	src := handlerTestSource(t, "https://delete.example.test", "Delete")
	if err := repo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}

	rec := authedRequest(router, http.MethodGet, "/api/v1/book-sources/missing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "source_not_found")

	rec = authedRequest(router, http.MethodDelete, "/api/v1/book-sources/"+src.SourceID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = authedRequest(router, http.MethodGet, "/api/v1/book-sources/"+src.SourceID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSourceHandlersReorder(t *testing.T) {
	router, repo := newSourceHandlerTestRouterAndRepo(t)
	first := handlerTestSource(t, "https://first.example.test", "First")
	second := handlerTestSource(t, "https://second.example.test", "Second")
	if err := repo.Save(context.Background(), first); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := repo.Save(context.Background(), second); err != nil {
		t.Fatalf("save second: %v", err)
	}

	body := `{"orders":[{"sourceId":"` + first.SourceID + `","customOrder":2},{"sourceId":"` + second.SourceID + `","customOrder":1}]}`
	rec := authedRequest(router, http.MethodPatch, "/api/v1/book-sources/reorder", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list generated.BookSourceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 2 || list.Items[0].SourceId != second.SourceID {
		t.Fatalf("reordered list = %#v", list)
	}
}

func TestSearchBooksReturnsValidationErrorForMissingKeyword(t *testing.T) {
	router, _ := newSearchHandlerTestRouter(t, &fakeAPISearcher{})

	rec := authedRequest(router, http.MethodPost, "/api/v1/search", `{"keyword":"   "}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

func TestSearchBooksReturnsResultsAndWarnings(t *testing.T) {
	searcher := &fakeAPISearcher{
		result: searchapp.MultiSearchResult{
			Items: []book.SearchBook{{
				BookURL:            "https://example.test/book/1",
				Origin:             "https://source-a.example.test",
				OriginName:         "Source A",
				Type:               0,
				Name:               "斗破苍穹",
				Author:             "天蚕土豆",
				Kind:               "玄幻",
				CoverURL:           "https://example.test/cover.jpg",
				Intro:              "少年故事",
				WordCount:          "500万字",
				LatestChapterTitle: "第一千章",
				TocURL:             "https://example.test/toc",
				Variable:           "v1",
				OriginOrder:        7,
				Origins:            []string{"https://source-a.example.test", "https://source-b.example.test"},
			}},
			Warnings: []searchapp.SourceWarning{{
				SourceID:       "source-b",
				BookSourceURL:  "https://source-b.example.test",
				BookSourceName: "Source B",
				Code:           "search_failed",
				Message:        "boom",
			}},
		},
	}
	router, _ := newSearchHandlerTestRouter(t, searcher)

	rec := authedRequest(router, http.MethodPost, "/api/v1/search", `{"keyword":"  斗破  ","page":2,"sourceIds":["source-a"],"group":"g1","precision":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if searcher.input.Keyword != "斗破" || searcher.input.Page != 2 || !searcher.input.EnabledOnly || !searcher.input.Precision {
		t.Fatalf("input = %#v", searcher.input)
	}
	if len(searcher.input.SourceIDs) != 1 || searcher.input.SourceIDs[0] != "source-a" || searcher.input.Group != "g1" {
		t.Fatalf("input scope = %#v", searcher.input)
	}
	var response generated.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].Name != "斗破苍穹" || response.Items[0].CoverUrl == nil || *response.Items[0].CoverUrl == "" {
		t.Fatalf("items = %#v", response.Items)
	}
	if len(response.Items[0].Origins) != 2 {
		t.Fatalf("origins = %#v", response.Items[0].Origins)
	}
	if len(response.Warnings) != 1 || response.Warnings[0].SourceId != "source-b" || response.Warnings[0].BookSourceName == nil {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
}

func TestSearchBooksMapsMissingSourceToNotFound(t *testing.T) {
	router, _ := newSearchHandlerTestRouter(t, &fakeAPISearcher{err: domainsource.ErrNotFound})

	rec := authedRequest(router, http.MethodPost, "/api/v1/search", `{"keyword":"斗破","sourceIds":["missing"]}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "source_not_found")
}

func TestDiscoverBooksReturnsResultsAndWarnings(t *testing.T) {
	discovery := &fakeAPIDiscovery{
		result: discoveryapp.Result{
			Items: []book.SearchBook{{
				BookURL:     "https://example.test/book/1",
				Origin:      "https://source-a.example.test",
				OriginName:  "Source A",
				Type:        0,
				Name:        "发现书",
				Author:      "作者",
				OriginOrder: 1,
				Origins:     []string{"https://source-a.example.test"},
			}},
			Warnings: []discoveryapp.Warning{{
				SourceID:       "source-b",
				BookSourceURL:  "https://source-b.example.test",
				BookSourceName: "Source B",
				Code:           "discovery_failed",
				Message:        "exploreUrl is required",
			}},
		},
	}
	router := newHandlerTestRouter(t, &fakeAPISearcher{}, discovery)

	rec := authedRequest(router, http.MethodPost, "/api/v1/discovery", `{"sourceIds":["source-a"],"group":"g1","discoveryUrl":"https://example.test/list?page={page}","page":3}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if discovery.input.Page != 3 || discovery.input.Group != "g1" || discovery.input.DiscoveryURL == "" || len(discovery.input.SourceIDs) != 1 {
		t.Fatalf("input = %#v", discovery.input)
	}
	var response generated.SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].Name != "发现书" {
		t.Fatalf("items = %#v", response.Items)
	}
	if len(response.Warnings) != 1 || response.Warnings[0].Code != "discovery_failed" {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
}

func TestDiscoverBooksReturnsValidationErrorForInvalidPage(t *testing.T) {
	router := newHandlerTestRouter(t, &fakeAPISearcher{}, &fakeAPIDiscovery{})

	rec := authedRequest(router, http.MethodPost, "/api/v1/discovery", `{"page":0}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

func TestBookHandlersLifecycleProgressChaptersContentAndDelete(t *testing.T) {
	router, bookRepo, chapterRepo := newBookHandlerTestRouter(t)
	createBody := `{"bookUrl":"https://site.example.test/book/1","tocUrl":"https://site.example.test/toc/1","origin":"https://source.example.test","originName":"Source","name":"斗破苍穹","author":"天蚕土豆","type":0,"originOrder":3}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/books", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	bookID := repositoryBookID(created.BookUrl)

	rec = authedRequest(router, http.MethodGet, "/api/v1/books?page=1&pageSize=10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list generated.BookListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Meta.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("list = %#v", list)
	}

	rec = authedRequest(router, http.MethodPut, "/api/v1/books/"+bookID+"/progress", `{"name":"斗破苍穹","author":"天蚕土豆","durChapterIndex":1,"durChapterPos":20,"durChapterTitle":"第二章","durChapterTime":99}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("progress status = %d body=%s", rec.Code, rec.Body.String())
	}
	var progressed generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &progressed); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if progressed.DurChapterIndex == nil || *progressed.DurChapterIndex != 1 {
		t.Fatalf("progressed = %#v", progressed)
	}

	chapter := &book.BookChapter{BookURL: created.BookUrl, URL: "https://site.example.test/c/1", Title: "第一章", Index: 0, BaseURL: "https://site.example.test/toc/1"}
	if err := chapterRepo.ReplaceByBook(context.Background(), bookID, []*book.BookChapter{chapter}); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}
	if err := chapterRepo.SaveContent(context.Background(), bookID, 0, "cached content"); err != nil {
		t.Fatalf("save content: %v", err)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/books/"+bookID+"/chapters", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chapters status = %d body=%s", rec.Code, rec.Body.String())
	}
	var chapters generated.ChapterListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &chapters); err != nil {
		t.Fatalf("decode chapters: %v", err)
	}
	if len(chapters.Items) != 1 || chapters.Items[0].Title != "第一章" {
		t.Fatalf("chapters = %#v", chapters)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/books/"+bookID+"/chapters/0/content", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("content status = %d body=%s", rec.Code, rec.Body.String())
	}
	var content generated.ChapterContentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &content); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	// 缓存保存净化前正文，读取时应用用户替换规则（Legado ContentProcessor 语义），因此正文按段落加缩进。
	if content.Content != "　　cached content" {
		t.Fatalf("content = %#v", content)
	}

	rec = authedRequest(router, http.MethodDelete, "/api/v1/books/"+bookID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := bookRepo.FindByURL(context.Background(), created.BookUrl); err == nil {
		t.Fatalf("book still exists after delete")
	}
}

func TestBookHandlersRefreshInfoExposesDownloadURLs(t *testing.T) {
	router, bookRepo, _ := newBookHandlerTestRouter(t)
	db := openHandlerTestDB(t)
	sourceRepo := repositories.NewBookSourceRepository(db)
	src := handlerTestSource(t, "https://file.example.test", "FileSource")
	src.BookSourceType = 3
	src.RuleBookInfo.Value = &rule.BookInfoRule{
		Name:         "$.name",
		Author:       "$.author",
		DownloadURLs: "$.downloads[*]",
	}
	if err := sourceRepo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}
	if err := bookRepo.Save(context.Background(), &book.Book{
		BookURL:    "https://file.example.test/book/1",
		Origin:     src.BookSourceURL,
		OriginName: src.BookSourceName,
		Name:       "文件书",
		Author:     "作者",
		Type:       3,
		InfoHTML:   `{"name":"文件书","author":"作者","downloads":["/a.epub","https://cdn.example.test/b.epub"]}`,
	}); err != nil {
		t.Fatalf("save book: %v", err)
	}

	manager := sourceapp.NewManager(sourceRepo)
	infoService := bookapp.NewInfoService(bookRepo, emptyAPIFetcher{}, nil)
	bookService := bookapp.NewService(bookRepo, repositories.NewChapterRepository(db), sourceRepo, infoService, nil)
	contentApp := contentapp.NewAppService(bookRepo, repositories.NewChapterRepository(db), sourceRepo, nil)
	handler := NewSourceHandler(manager).WithBooks(bookService, contentApp)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	router = middleware.Trace(middleware.Auth("secret")(r))

	rec := authedRequest(router, http.MethodPost, "/api/v1/books/"+repositoryBookID("https://file.example.test/book/1")+"/refresh-info", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode book: %v", err)
	}
	if got.DownloadUrls == nil || len(*got.DownloadUrls) != 2 {
		t.Fatalf("book = %#v", got)
	}
}

func TestBookContentHandlerAppliesReplaceProcessor(t *testing.T) {
	router, _, chapterRepo, replaceRepo := newBookHandlerTestRouterWithReplace(t)
	createBody := `{"bookUrl":"https://site.example.test/book/replace","origin":"https://source.example.test","originName":"Source","name":"斗破苍穹","author":"天蚕土豆","type":0,"originOrder":3}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/books", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	bookID := repositoryBookID(created.BookUrl)
	chapter := &book.BookChapter{BookURL: created.BookUrl, URL: "https://site.example.test/c/replace", Title: "第一章", Index: 0, BaseURL: "https://site.example.test/toc/replace"}
	if err := chapterRepo.ReplaceByBook(context.Background(), bookID, []*book.BookChapter{chapter}); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}
	if err := replaceRepo.Save(context.Background(), &replace.ReplaceRule{ID: 100, Name: "ad", Pattern: "广告", Replacement: "", ScopeContent: true, IsEnabled: true, TimeoutMillisecond: 3000}); err != nil {
		t.Fatalf("save replace rule: %v", err)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/books/"+bookID+"/chapters/0/content", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("content status = %d body=%s", rec.Code, rec.Body.String())
	}
	var content generated.ChapterContentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &content); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if content.Content != "　　正文" {
		t.Fatalf("content = %#v", content)
	}
}

func TestProxyImageReturnsImageBytes(t *testing.T) {
	router := newImageProxyTestRouter(t, &fakeImageProxyFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.png",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{0x89, 'P', 'N', 'G'},
		StatusCode: http.StatusOK,
	}})

	rec := authedRequest(router, http.MethodGet, "/api/v1/images/proxy?url="+url.QueryEscape("https://img.example.test/cover.png"), "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q", got)
	}
	if string(rec.Body.Bytes()) != string([]byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("body = %v", rec.Body.Bytes())
	}
}

func TestProxyImageRequiresSourceIDWhenDecodeModeIsSet(t *testing.T) {
	router := newImageProxyTestRouter(t, &fakeImageProxyFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.png",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{0x89, 'P', 'N', 'G'},
		StatusCode: http.StatusOK,
	}})

	rec := authedRequest(router, http.MethodGet, "/api/v1/images/proxy?url="+url.QueryEscape("https://img.example.test/cover.png")+"&decodeMode=cover", "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

func TestProxyImageMapsFetcherSecurityErrors(t *testing.T) {
	router := newImageProxyTestRouter(t, &fakeImageProxyFetcher{err: fetch.ErrBlocked})

	rec := authedRequest(router, http.MethodGet, "/api/v1/images/proxy?url="+url.QueryEscape("http://127.0.0.1/private.png"), "")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "fetch_blocked")
}

func TestProxyImageRejectsOversizedImagesViaFetcher(t *testing.T) {
	router := newImageProxyTestRouter(t, &fakeImageProxyFetcher{err: errors.New("response body too large")})

	rec := authedRequest(router, http.MethodGet, "/api/v1/images/proxy?url="+url.QueryEscape("https://img.example.test/large.png"), "")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "fetch_failed")
}

func TestGetChapterContentMapsUnsupportedBrowserFlow(t *testing.T) {
	router, bookRepo, chapterRepo := newBookHandlerTestRouter(t)
	bookValue := &book.Book{
		BookURL:    "https://book.test/1",
		Origin:     "https://source.example.test",
		OriginName: "Source",
		Name:       "Book",
		Author:     "Author",
		Type:       0,
	}
	if err := bookRepo.Save(context.Background(), bookValue); err != nil {
		t.Fatalf("save book: %v", err)
	}
	if err := chapterRepo.ReplaceByBook(context.Background(), repositoryBookID(bookValue.BookURL), []*book.BookChapter{{BookURL: bookValue.BookURL, URL: "https://book.test/c/1", Title: "第一章", Index: 0}}); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	sourceBody := `{
		"sourceId":"",
		"bookSourceUrl":"https://source.example.test",
		"bookSourceName":"Source",
		"bookSourceType":0,
		"customOrder":0,
		"enabled":true,
		"enabledExplore":false,
		"enabledCookieJar":false,
		"lastUpdateTime":0,
		"ruleContent":{"content":".content@text","sourceRegex":"https://cdn.example.test/content/.*"}
	}`
	rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources", sourceBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create source status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/books/"+repositoryBookID(bookValue.BookURL)+"/chapters/0/content", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "unsupported_rule_feature")
}

func TestReplaceRuleHandlersCRUDAndInvalidRegex(t *testing.T) {
	router := newReplaceHandlerTestRouter(t)
	body := `{"id":0,"name":"去广告","pattern":"广告","replacement":"","scopeTitle":false,"scopeContent":true,"isEnabled":true,"isRegex":false,"timeoutMillisecond":0,"order":2}`

	rec := authedRequest(router, http.MethodPost, "/api/v1/replace-rules", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.ReplaceRule
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Id == 0 || created.TimeoutMillisecond != 3000 {
		t.Fatalf("created = %#v", created)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/replace-rules", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list generated.ReplaceRuleListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "去广告" {
		t.Fatalf("list = %#v", list)
	}

	updateBody := strings.Replace(body, `"name":"去广告"`, `"name":"去广告2"`, 1)
	rec = authedRequest(router, http.MethodPut, "/api/v1/replace-rules/"+int64Text(created.Id), updateBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	var updated generated.ReplaceRule
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Id != created.Id || updated.Name != "去广告2" {
		t.Fatalf("updated = %#v", updated)
	}

	rec = authedRequest(router, http.MethodPost, "/api/v1/replace-rules", `{"id":0,"name":"bad","pattern":"(","replacement":"","scopeTitle":false,"scopeContent":true,"isEnabled":true,"isRegex":true,"timeoutMillisecond":3000,"order":3}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad regex status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")

	rec = authedRequest(router, http.MethodDelete, "/api/v1/replace-rules/"+int64Text(created.Id), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReplaceRuleTestHandlerPlainRegexAndInvalidRegex(t *testing.T) {
	router := newReplaceHandlerTestRouter(t)

	rec := authedRequest(router, http.MethodPost, "/api/v1/replace-rules/test", `{"rule":{"id":0,"name":"plain","pattern":"foo","replacement":"bar","scopeTitle":false,"scopeContent":true,"isEnabled":true,"isRegex":false,"timeoutMillisecond":3000,"order":0},"text":"foo foo"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("plain status = %d body=%s", rec.Code, rec.Body.String())
	}
	var plain generated.ReplaceRuleTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil {
		t.Fatalf("decode plain: %v", err)
	}
	if plain.Text != "bar bar" {
		t.Fatalf("plain = %#v", plain)
	}

	rec = authedRequest(router, http.MethodPost, "/api/v1/replace-rules/test", `{"rule":{"id":0,"name":"regex","pattern":"foo(\\d+)","replacement":"bar$1","scopeTitle":false,"scopeContent":true,"isEnabled":true,"isRegex":true,"timeoutMillisecond":3000,"order":0},"text":"foo12"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("regex status = %d body=%s", rec.Code, rec.Body.String())
	}
	var regex generated.ReplaceRuleTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &regex); err != nil {
		t.Fatalf("decode regex: %v", err)
	}
	if regex.Text != "bar12" {
		t.Fatalf("regex = %#v", regex)
	}

	rec = authedRequest(router, http.MethodPost, "/api/v1/replace-rules/test", `{"rule":{"id":0,"name":"bad","pattern":"(","replacement":"","scopeTitle":false,"scopeContent":true,"isEnabled":true,"isRegex":true,"timeoutMillisecond":3000,"order":0},"text":"foo"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

func TestReplaceRuleTestHandlerMapsRegexLimit(t *testing.T) {
	router := newReplaceHandlerTestRouter(t)
	text := strings.Repeat("a", 2*1024*1024+1)
	body, err := json.Marshal(map[string]any{
		"rule": map[string]any{
			"id":                 0,
			"name":               "limit",
			"pattern":            "a",
			"replacement":        "b",
			"scopeTitle":         false,
			"scopeContent":       true,
			"isEnabled":          true,
			"isRegex":            true,
			"timeoutMillisecond": 3000,
			"order":              0,
		},
		"text": text,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	rec := authedRequest(router, http.MethodPost, "/api/v1/replace-rules/test", string(body))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "replace_failed")
}

func TestDebugSessionEventsStreamReplaysHistory(t *testing.T) {
	router, debugRepo := newDebugHandlerTestRouter(t)
	ctx := context.Background()
	session := &task.DebugSession{SessionID: "debug-1", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionRunning, CreatedAt: time.Now().UTC()}
	if err := debugRepo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := debugRepo.AppendEvent(ctx, &task.DebugEvent{
		SessionID: session.SessionID,
		Type:      task.DebugEventStarted,
		Level:     task.DebugLevelInfo,
		Message:   "started",
		TraceID:   "trace-debug",
		CreatedAt: session.CreatedAt,
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/debug/sessions/debug-1/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: debug.started") || !strings.Contains(body, `"message":"started"`) {
		t.Fatalf("sse body = %s", body)
	}
}

func TestDebugSessionHandlersCreateGetAndCancel(t *testing.T) {
	router, _ := newDebugHandlerTestRouter(t)

	rec := authedRequest(router, http.MethodPost, "/api/v1/debug/book-source", `{"sourceId":"source-1","mode":"keyword","keyword":"斗破"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.DebugSession
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.SessionId == "" || created.SourceId != "source-1" || created.Mode != generated.Keyword || created.Status != generated.DebugSessionStatusRunning {
		t.Fatalf("created = %#v", created)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/debug/sessions/"+created.SessionId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got generated.DebugSession
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode got: %v", err)
	}
	if got.SessionId != created.SessionId || got.Keyword == nil || *got.Keyword != "斗破" {
		t.Fatalf("got = %#v", got)
	}

	rec = authedRequest(router, http.MethodDelete, "/api/v1/debug/sessions/"+created.SessionId, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d body=%s", rec.Code, rec.Body.String())
	}
	var cancelled generated.DebugSession
	if err := json.Unmarshal(rec.Body.Bytes(), &cancelled); err != nil {
		t.Fatalf("decode cancelled: %v", err)
	}
	if cancelled.Status != generated.DebugSessionStatusCancelled || cancelled.CompletedAt == nil {
		t.Fatalf("cancelled = %#v", cancelled)
	}
}

func TestDebugSessionCreateRejectsUnknownMode(t *testing.T) {
	router, _ := newDebugHandlerTestRouter(t)
	for _, mode := range []string{"keywordd", "url", "", "resolve"} {
		rec := authedRequest(router, http.MethodPost, "/api/v1/debug/book-source", `{"sourceId":"source-1","mode":"`+mode+`","url":"https://book.test/1"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mode %q status = %d body=%s", mode, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "validation_failed")
	}
}

func TestDebugSessionEventsStreamReturnsNotFoundForUnknownSession(t *testing.T) {
	router, _ := newDebugHandlerTestRouter(t)
	rec := authedRequest(router, http.MethodGet, "/api/v1/debug/sessions/missing/events", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "debug_session_not_found")
}

func TestDebugSessionEventsStreamClosesForTerminalSession(t *testing.T) {
	router, debugRepo := newDebugHandlerTestRouter(t)
	ctx := context.Background()
	session := &task.DebugSession{SessionID: "debug-terminal", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionFailed, CreatedAt: time.Now().UTC()}
	if err := debugRepo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := debugRepo.AppendEvent(ctx, &task.DebugEvent{SessionID: session.SessionID, Type: task.DebugEventStageFailed, Stage: "content", Level: task.DebugLevelError, Message: "content empty", TraceID: "trace-debug", CreatedAt: session.CreatedAt}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	rec := authedRequest(router, http.MethodGet, "/api/v1/debug/sessions/"+session.SessionID+"/events", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: debug.stage_failed") || !strings.Contains(body, `"message":"content empty"`) {
		t.Fatalf("sse body = %s", body)
	}
}

func TestDebugSessionEventsStreamRejectsMalformedLastEventID(t *testing.T) {
	router, debugRepo := newDebugHandlerTestRouter(t)
	ctx := context.Background()
	session := &task.DebugSession{SessionID: "debug-last-event", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionFailed, CreatedAt: time.Now().UTC()}
	if err := debugRepo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	for i := 1; i <= 2; i++ {
		if err := debugRepo.AppendEvent(ctx, &task.DebugEvent{SessionID: session.SessionID, Type: task.DebugEventLog, Level: task.DebugLevelInfo, Message: "event-" + strconv.Itoa(i), CreatedAt: session.CreatedAt}); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/debug/sessions/"+session.SessionID+"/events", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Last-Event-ID", "1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "event-1") || !strings.Contains(body, "event-2") {
		t.Fatalf("sse body = %s", body)
	}
}

func TestDebugSessionCreateValidatesModeInputs(t *testing.T) {
	router, _ := newDebugHandlerTestRouter(t)
	rec := authedRequest(router, http.MethodPost, "/api/v1/debug/book-source", `{"sourceId":"source-1","mode":"keyword"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("keyword status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")

	rec = authedRequest(router, http.MethodPost, "/api/v1/debug/book-source", `{"sourceId":"source-1","mode":"content_url"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("url status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "validation_failed")
}

func TestDebugSessionHandlersRequireBearerToken(t *testing.T) {
	router, _ := newDebugHandlerTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/debug/book-source", strings.NewReader(`{"sourceId":"source-1","mode":"keyword","keyword":"斗破"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCheckTaskHandlersCreateGetCancelAndEvents(t *testing.T) {
	router, sourceRepo, _ := newCheckHandlerTestRouter(t)
	src := handlerTestSource(t, "https://check.example.test", "Check")
	src.SearchURL = src.BookSourceURL + "/search"
	if err := sourceRepo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}

	rec := authedRequest(router, http.MethodPost, "/api/v1/check/book-sources", `{"sourceIds":["`+src.SourceID+`"],"options":{"keyword":"斗破","checkSearch":true,"checkInfo":true,"checkToc":true,"checkContent":true}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created generated.CheckTask
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.TaskId == "" || created.Total != 1 {
		t.Fatalf("created = %#v", created)
	}

	var got generated.CheckTask
	waitForCondition(t, "check task finished", func() bool {
		rec = authedRequest(router, http.MethodGet, "/api/v1/check/tasks/"+created.TaskId, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode got: %v", err)
		}
		return got.Status == generated.TaskStatusSucceeded || got.Status == generated.TaskStatusFailed
	})
	if got.Finished != 1 || len(got.Items) != 1 {
		t.Fatalf("got = %#v", got)
	}

	rec = authedRequest(router, http.MethodGet, "/api/v1/check/tasks/"+created.TaskId+"/events", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"event: check.started", "event: check.item_started", "event: check.item_passed", "event: check.completed"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("events body missing %q: %s", want, rec.Body.String())
		}
	}

	rec = authedRequest(router, http.MethodDelete, "/api/v1/check/tasks/"+created.TaskId, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCheckTaskCreateReturnsNotFoundForUnknownSource(t *testing.T) {
	router, _, _ := newCheckHandlerTestRouter(t)
	rec := authedRequest(router, http.MethodPost, "/api/v1/check/book-sources", `{"sourceIds":["missing"],"options":{"keyword":"斗破","checkSearch":true}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "source_not_found")
}

// TestCheckTaskEventsStreamPushesLiveEvents 用真实 HTTP 服务验证 SSE：先回放已产生的事件，
// 连接保持打开（收到心跳注释），任务结束后推送终态事件并关闭连接。
func TestCheckTaskEventsStreamPushesLiveEvents(t *testing.T) {
	db := openHandlerTestDB(t)
	sourceRepo := repositories.NewBookSourceRepository(db)
	checkRepo := repositories.NewCheckRepository(db)
	bookRepo := repositories.NewBookRepository(db)
	chapterRepo := repositories.NewChapterRepository(db)
	content := &blockingHandlerContent{started: make(chan struct{}), released: make(chan struct{})}
	checker := checkapp.NewChecker(
		&fakeAPISearcher{result: searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://check.example.test", OriginName: "Check", Name: "斗破苍穹", Author: "天蚕土豆"}}}},
		&fakeAPIDiscovery{},
		&fakeCheckAPIBooks{bookRepo: bookRepo, chapterRepo: chapterRepo},
		content,
	)
	events := checkapp.NewPublisher()
	runner := checkworker.NewRunner(checkRepo, checker).WithEvents(events)
	checkManager := checkapp.NewManager(checkRepo, sourceRepo, runner).WithEvents(events)
	handler := NewSourceHandler(sourceapp.NewManager(sourceRepo)).WithCheck(checkManager)
	handler.heartbeat = 20 * time.Millisecond
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	server := httptest.NewServer(middleware.Trace(middleware.Auth("secret")(r)))
	defer server.Close()

	src := handlerTestSource(t, "https://live.example.test", "Live")
	src.SearchURL = src.BookSourceURL + "/search"
	if err := sourceRepo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}

	body := `{"sourceIds":["` + src.SourceID + `"],"options":{"keyword":"斗破","checkSearch":true,"checkInfo":true,"checkToc":true,"checkContent":true}}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/check/book-sources", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST check task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	var created generated.CheckTask
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	select {
	case <-content.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("check content stage did not start")
	}

	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/check/tasks/"+created.TaskId+"/events", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamReq.Header.Set("Authorization", "Bearer secret")
	stream, err := server.Client().Do(streamReq)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", stream.StatusCode)
	}
	if got := stream.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q", got)
	}
	sse := newSSEStream(stream.Body)
	sse.until(t, "event: check.item_started", 5*time.Second)
	// 任务仍卡在正文阶段，连接必须保持打开并持续发送心跳。
	sse.until(t, ": heartbeat", 5*time.Second)

	close(content.released)
	sse.until(t, "event: check.completed", 5*time.Second)
	sse.untilEOF(t, 5*time.Second)
}

// sseStream 用单个读协程把 SSE 响应体按行送入 channel，避免并发读同一个 bufio.Reader。
type sseStream struct {
	lines chan string
	errs  chan error
}

func newSSEStream(body io.Reader) *sseStream {
	stream := &sseStream{lines: make(chan string, 128), errs: make(chan error, 1)}
	go func() {
		defer close(stream.lines)
		reader := bufio.NewReader(body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				stream.errs <- err
				return
			}
			stream.lines <- line
		}
	}()
	return stream
}

func (s *sseStream) until(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	var seen strings.Builder
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				t.Fatalf("sse stream ended before %q: %v; seen=%s", want, <-s.errs, seen.String())
			}
			seen.WriteString(line)
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q; seen=%s", want, seen.String())
		}
	}
}

func (s *sseStream) untilEOF(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-s.errs:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("stream read error = %v, want EOF", err)
		}
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for the sse stream to close")
	}
}

// TestDebugSessionEventsStreamPushesLiveEvents 验证调试会话的 SSE 是真实事件流：
// 订阅后推送新事件会实时到达，终态事件到达后连接关闭；长时间无事件时靠心跳保活。
func TestDebugSessionEventsStreamPushesLiveEvents(t *testing.T) {
	router, debugRepo, publisher := newDebugHandlerTestRouterWithPublisher(t)
	server := httptest.NewServer(router)
	defer server.Close()

	session := &task.DebugSession{SessionID: "debug-live", SourceID: "source-1", Mode: task.DebugModeKeyword, Status: task.DebugSessionRunning, CreatedAt: time.Now().UTC()}
	if err := debugRepo.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/debug/sessions/"+session.SessionID+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", stream.StatusCode)
	}

	sse := newSSEStream(stream.Body)
	// 订阅后先看到心跳，说明在无事件时连接保持打开。
	sse.until(t, ": heartbeat", 5*time.Second)

	publisher.Publish(context.Background(), &task.DebugEvent{
		SessionID: session.SessionID,
		Type:      task.DebugEventStageFailed,
		Stage:     "content",
		Level:     task.DebugLevelError,
		Message:   "content empty",
		TraceID:   "trace-live",
		CreatedAt: time.Now().UTC(),
	})
	sse.until(t, "event: debug.stage_failed", 5*time.Second)
	sse.until(t, `"message":"content empty"`, 5*time.Second)
	// stage 失败不是会话终态，连接必须继续打开并发送心跳。
	sse.until(t, ": heartbeat", 5*time.Second)
	completedAt := time.Now().UTC()
	if _, err := debugRepo.UpdateSessionStatus(context.Background(), session.SessionID, task.DebugSessionFailed, &completedAt); err != nil {
		t.Fatalf("UpdateSessionStatus() error = %v", err)
	}
	// 会话进入终态后，SSE 通过心跳周期内的状态检查收束连接，不再永久挂起。
	sse.untilEOF(t, 5*time.Second)
}

func waitForCondition(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// blockingHandlerContent 阻塞正文抓取，让任务在“运行中”状态停留以观察 SSE 实时推送。
type blockingHandlerContent struct {
	once     sync.Once
	started  chan struct{}
	released chan struct{}
}

func (c *blockingHandlerContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	c.once.Do(func() { close(c.started) })
	select {
	case <-c.released:
	case <-ctx.Done():
		return contentapp.Result{}, nil, ctx.Err()
	}
	return contentapp.Result{Content: "正文", Chapter: book.BookChapter{Index: index}}, &book.Book{}, nil
}

func TestCheckTaskHandlersValidateAndRequireAuth(t *testing.T) {
	router, _, _ := newCheckHandlerTestRouter(t)
	rec := authedRequest(router, http.MethodPost, "/api/v1/check/book-sources", `{"sourceIds":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("validation status = %d body=%s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/check/book-sources", strings.NewReader(`{"sourceIds":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("auth status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func newSourceHandlerTestRouter(t *testing.T) http.Handler {
	t.Helper()
	router, _ := newSourceHandlerTestRouterAndRepo(t)
	return router
}

func newSourceHandlerTestRouterAndRepo(t *testing.T) (http.Handler, *repositories.BookSourceRepository) {
	t.Helper()
	db := openHandlerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	manager := sourceapp.NewManager(repo)
	handler := NewSourceHandler(manager)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), repo
}

func newImportHandlerTestRouter(t *testing.T, fetcher fetch.Fetcher) (http.Handler, *repositories.BookSourceRepository) {
	t.Helper()
	db := openHandlerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	manager := sourceapp.NewManager(repo)
	importer := sourceapp.NewImporter(repo)
	if fetcher != nil {
		importer = sourceapp.NewImporterWithFetcher(repo, fetcher)
	}
	handler := NewSourceHandler(manager).WithImporter(importer)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), repo
}

func newSearchHandlerTestRouter(t *testing.T, searcher *fakeAPISearcher) (http.Handler, *repositories.BookSourceRepository) {
	t.Helper()
	return newHandlerTestRouterWithRepo(t, searcher, nil)
}

func newHandlerTestRouter(t *testing.T, searcher *fakeAPISearcher, discovery *fakeAPIDiscovery) http.Handler {
	t.Helper()
	router, _ := newHandlerTestRouterWithRepo(t, searcher, discovery)
	return router
}

func newHandlerTestRouterWithRepo(t *testing.T, searcher *fakeAPISearcher, discovery *fakeAPIDiscovery) (http.Handler, *repositories.BookSourceRepository) {
	t.Helper()
	db := openHandlerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	manager := sourceapp.NewManager(repo)
	handler := NewSourceHandler(manager)
	if searcher != nil {
		handler.WithSearcher(searcher)
	}
	if discovery != nil {
		handler.WithDiscovery(discovery)
	}
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), repo
}

func newBookHandlerTestRouter(t *testing.T) (http.Handler, *repositories.BookRepository, *repositories.ChapterRepository) {
	t.Helper()
	router, bookRepo, chapterRepo, _ := newBookHandlerTestRouterWithReplace(t)
	return router, bookRepo, chapterRepo
}

func newBookHandlerTestRouterWithReplace(t *testing.T) (http.Handler, *repositories.BookRepository, *repositories.ChapterRepository, *repositories.ReplaceRuleRepository) {
	t.Helper()
	db := openHandlerTestDB(t)
	sourceRepo := repositories.NewBookSourceRepository(db)
	bookRepo := repositories.NewBookRepository(db)
	chapterRepo := repositories.NewChapterRepository(db)
	replaceRepo := repositories.NewReplaceRuleRepository(db)
	src := handlerTestSource(t, "https://source.example.test", "Source")
	src.RuleContent = rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: ".content@text"}}
	if err := sourceRepo.Save(context.Background(), src); err != nil {
		t.Fatalf("save source: %v", err)
	}
	manager := sourceapp.NewManager(sourceRepo)
	infoService := bookapp.NewInfoService(bookRepo, emptyAPIFetcher{}, nil)
	tocService := bookapp.NewTocService(chapterRepo, emptyAPIFetcher{}, nil)
	bookService := bookapp.NewService(bookRepo, chapterRepo, sourceRepo, infoService, tocService)
	contentService := contentapp.NewService(chapterRepo, emptyAPIFetcher{}, nil).WithProcessor(replaceapp.NewProcessor(replaceRepo))
	contentApp := contentapp.NewAppService(bookRepo, chapterRepo, sourceRepo, contentService)
	handler := NewSourceHandler(manager).WithBooks(bookService, contentApp)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), bookRepo, chapterRepo, replaceRepo
}

func newImageProxyTestRouter(t *testing.T, fetcher fetch.Fetcher) http.Handler {
	t.Helper()
	db := openHandlerTestDB(t)
	manager := sourceapp.NewManager(repositories.NewBookSourceRepository(db))
	handler := NewSourceHandler(manager).WithImages(contentapp.NewImageProxy(fetcher, nil, nil, nil))
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r))
}

func newReplaceHandlerTestRouter(t *testing.T) http.Handler {
	t.Helper()
	db := openHandlerTestDB(t)
	manager := sourceapp.NewManager(repositories.NewBookSourceRepository(db))
	replaceManager := replaceapp.NewManager(repositories.NewReplaceRuleRepository(db))
	handler := NewSourceHandler(manager).WithReplace(replaceManager)
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r))
}

func newDebugHandlerTestRouter(t *testing.T) (http.Handler, *repositories.DebugRepository) {
	t.Helper()
	router, repo, _ := newDebugHandlerTestRouterWithPublisher(t)
	return router, repo
}

func newDebugHandlerTestRouterWithPublisher(t *testing.T) (http.Handler, *repositories.DebugRepository, *debugapp.Publisher) {
	t.Helper()
	db := openHandlerTestDB(t)
	manager := sourceapp.NewManager(repositories.NewBookSourceRepository(db))
	debugRepo := repositories.NewDebugRepository(db)
	publisher := debugapp.NewPublisher(debugRepo)
	handler := NewSourceHandler(manager).WithDebug(debugapp.NewService(debugRepo).WithPublisher(publisher), publisher)
	handler.heartbeat = 10 * time.Millisecond
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), debugRepo, publisher
}

func newCheckHandlerTestRouter(t *testing.T) (http.Handler, *repositories.BookSourceRepository, *repositories.CheckRepository) {
	t.Helper()
	db := openHandlerTestDB(t)
	sourceRepo := repositories.NewBookSourceRepository(db)
	bookRepo := repositories.NewBookRepository(db)
	chapterRepo := repositories.NewChapterRepository(db)
	checkRepo := repositories.NewCheckRepository(db)
	manager := sourceapp.NewManager(sourceRepo)
	checker := checkapp.NewChecker(&fakeAPISearcher{result: searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://check.example.test", OriginName: "Check", Name: "斗破苍穹", Author: "天蚕土豆"}}}}, &fakeAPIDiscovery{}, &fakeCheckAPIBooks{bookRepo: bookRepo, chapterRepo: chapterRepo}, &fakeCheckAPIContent{})
	events := checkapp.NewPublisher()
	runner := checkworker.NewRunner(checkRepo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(sourceRepo)).WithEvents(events)
	checkManager := checkapp.NewManager(checkRepo, sourceRepo, runner).WithEvents(events)
	handler := NewSourceHandler(manager).WithCheck(checkManager)
	handler.heartbeat = 10 * time.Millisecond
	r := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(handler, r, "/api/v1")
	return middleware.Trace(middleware.Auth("secret")(r)), sourceRepo, checkRepo
}

type emptyAPIFetcher struct{}

func (emptyAPIFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	return &fetch.Response{URL: req.URL, Body: []byte(`<div class="content">第一章
广告
正文</div>`)}, nil
}

func repositoryBookID(bookURL string) string {
	return book.IDFromURL(bookURL)
}

func int64Text(value int64) string {
	return strconv.FormatInt(value, 10)
}

type fakeAPISearcher struct {
	input  searchapp.MultiSearchInput
	result searchapp.MultiSearchResult
	err    error
}

func (s *fakeAPISearcher) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	s.input = input
	if s.err != nil {
		return searchapp.MultiSearchResult{}, s.err
	}
	return s.result, nil
}

type fakeAPIDiscovery struct {
	input  discoveryapp.Input
	result discoveryapp.Result
	err    error
}

func (s *fakeAPIDiscovery) Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error) {
	s.input = input
	if s.err != nil {
		return discoveryapp.Result{}, s.err
	}
	return s.result, nil
}

func (s *fakeAPIDiscovery) Categories(ctx context.Context, sourceID string) ([]discoveryapp.Category, error) {
	return []discoveryapp.Category{{Title: "玄幻", URL: "https://source.test/discovery"}}, nil
}

type fakeCheckAPIBooks struct {
	bookRepo    *repositories.BookRepository
	chapterRepo *repositories.ChapterRepository
}

func (b *fakeCheckAPIBooks) Save(ctx context.Context, item *book.Book) (*book.Book, error) {
	if err := b.bookRepo.Save(ctx, item); err != nil {
		return nil, err
	}
	return b.bookRepo.FindByURL(ctx, item.BookURL)
}

func (b *fakeCheckAPIBooks) RefreshInfo(ctx context.Context, id string) (book.Book, error) {
	got, err := b.bookRepo.FindByID(ctx, id)
	if err != nil {
		return book.Book{}, err
	}
	return *got, nil
}

func (b *fakeCheckAPIBooks) RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error) {
	got, err := b.bookRepo.FindByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	chapters := []*book.BookChapter{{BookURL: got.BookURL, URL: "https://book.test/c/1", Title: "第一章", Index: 0}}
	if err := b.chapterRepo.ReplaceByBook(ctx, id, chapters); err != nil {
		return nil, nil, err
	}
	return chapters, got, nil
}

type fakeCheckAPIContent struct{}

func (fakeCheckAPIContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	return contentapp.Result{Content: "正文", Chapter: book.BookChapter{Index: index}}, &book.Book{}, nil
}

type fakeImageProxyFetcher struct {
	response *fetch.Response
	err      error
}

func (f *fakeImageProxyFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

type fakeImportFetcher struct {
	response *fetch.Response
	err      error
}

func (f *fakeImportFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func openHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func authedRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer secret")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func handlerTestSource(t *testing.T, url string, name string) *domainsource.BookSource {
	t.Helper()
	src := &domainsource.BookSource{
		BookSourceURL:    url,
		BookSourceName:   name,
		BookSourceType:   0,
		Enabled:          true,
		EnabledExplore:   true,
		EnabledCookieJar: true,
	}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize source: %v", err)
	}
	return src
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != want {
		t.Fatalf("error code = %q, want %q; body=%s", body.Code, want, rec.Body.String())
	}
}
