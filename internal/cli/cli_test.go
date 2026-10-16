package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reader/internal/bootstrap"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/infra/config"
)

func newTestRuntime(t *testing.T) *bootstrap.Runtime {
	t.Helper()
	cfg := config.Config{
		Addr:                "127.0.0.1:0",
		SQLiteDSN:           filepath.Join(t.TempDir(), "reader.db"),
		Token:               "cli-unused",
		Env:                 config.EnvDevelopment,
		DefaultUserAgent:    "Reader-CLI-Test",
		FetchTimeout:        30 * 1e9,
		FetchMaxBodyBytes:   10 * 1024 * 1024,
		FetchMaxRedirects:   5,
		AllowPrivateNetwork: true,
		GlobalConcurrency:   8,
		SourceConcurrency:   2,
		ScriptTimeout:       10 * 1e9,
		RegexTimeout:        500 * 1e6,
		DebugBodyLimit:      32 * 1024,
	}
	rt, cleanup, err := bootstrap.Open(cfg)
	if err != nil {
		t.Fatalf("bootstrap.Open() error = %v", err)
	}
	t.Cleanup(cleanup)
	return rt
}

type cliMockSource struct {
	*httptest.Server
	content2Status int
}

func newCLIMockSource(t *testing.T) *cliMockSource {
	t.Helper()
	mock := &cliMockSource{content2Status: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/sources.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mock.sourceJSON()))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="item"><a class="name" href="/book/1">斗破苍穹</a><span class="author">天蚕土豆</span><span class="kind">玄幻</span><span class="word">500万字</span><span class="latest">第二章</span><img class="cover" src="/cover.png"/></div>`))
	})
	mux.HandleFunc("/book/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><div class="kind">玄幻</div><span class="word">500万字</span><span class="latest">第二章</span><p class="intro">少年成长</p><img class="cover" src="/cover.png"/><a class="toc" href="/toc/1">目录</a>`))
	})
	mux.HandleFunc("/toc/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<ul>` +
			`<li class="chapter"><a href="/content/1">第一章</a><span class="isvol">no</span></li>` +
			`<li class="chapter"><a href="">第一卷</a><span class="isvol">yes</span></li>` +
			`<li class="chapter"><a href="/content/2">第二章</a><span class="isvol">no</span></li>` +
			`</ul>`))
	})
	mux.HandleFunc("/content/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">广告
真正正文一</div>`))
	})
	mux.HandleFunc("/content/2", func(w http.ResponseWriter, r *http.Request) {
		if mock.content2Status != http.StatusOK {
			w.WriteHeader(mock.content2Status)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">广告
真正正文二</div>`))
	})
	mock.Server = httptest.NewServer(mux)
	t.Cleanup(mock.Close)
	return mock
}

func (m *cliMockSource) sourceJSON() string {
	source := map[string]any{
		"bookSourceUrl":    m.URL,
		"bookSourceName":   "CLI Mock Source",
		"bookSourceType":   0,
		"enabled":          true,
		"enabledExplore":   true,
		"enabledCookieJar": true,
		"searchUrl":        m.URL + "/search?key={{key}}&page={{page}}",
		"ruleSearch": map[string]any{
			"bookList":     ".item",
			"name":         ".name@text",
			"author":       ".author@text",
			"kind":         ".kind@text",
			"wordCount":    ".word@text",
			"lastChapter":  ".latest@text",
			"coverUrl":     ".cover@src",
			"bookUrl":      ".name@href",
			"checkKeyWord": "斗破",
			"tocUrl":       ".toc@href",
		},
		"ruleBookInfo": map[string]any{
			"name":        "h1@text",
			"author":      ".author@text",
			"kind":        ".kind@text",
			"wordCount":   ".word@text",
			"lastChapter": ".latest@text",
			"intro":       ".intro@text",
			"coverUrl":    ".cover@src",
			"tocUrl":      ".toc@href",
			"canReName":   "1",
		},
		"ruleToc": map[string]any{
			"chapterList": ".chapter",
			"chapterName": "a@text",
			"chapterUrl":  "a@href",
			"isVolume":    ".isvol@text",
		},
		"ruleContent": map[string]any{
			"content": ".content@text",
		},
	}
	raw, _ := json.Marshal(source)
	return string(raw)
}

// importMockURL 通过远程 URL 导入完整规则书源，验证 import 的 URL 链路。
func importMockURL(t *testing.T, rt *bootstrap.Runtime, mockURL string) int {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runImport(rt, []string{mockURL + "/sources.json"}, stdout, stderr)
	if code != 0 || !strings.Contains(stdout.String(), "summary: added=1") {
		t.Fatalf("runImport(url) = %d, stdout: %s, stderr: %s", code, stdout.String(), stderr.String())
	}
	return code
}

// addMockBook 完成导入（URL 链路）+ 加书架 + 刷新，返回 bookId。
func addMockBook(t *testing.T, rt *bootstrap.Runtime, mock *cliMockSource) string {
	t.Helper()
	importMockURL(t, rt, mock.URL)
	page, err := rt.Sources.List(t.Context(), source.BookSourceFilter{Page: 1, PageSize: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list sources: %v (n=%d)", err, len(page.Items))
	}
	sourceID := page.Items[0].SourceID
	bookURL := mock.URL + "/book/1"

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runBookAdd(rt, []string{"--source", sourceID, "--url", bookURL}, stdout, stderr); code != 0 {
		t.Fatalf("runBookAdd() = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "added\t"+book.IDFromURL(bookURL)) {
		t.Fatalf("book add output = %q", stdout.String())
	}

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := runBookRefresh(rt, []string{book.IDFromURL(bookURL)}, stdout, stderr); code != 0 {
		t.Fatalf("runBookRefresh() = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "chapters=3") {
		t.Fatalf("book refresh output = %q", stdout.String())
	}
	return book.IDFromURL(bookURL)
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run([]string{"nope"}, stdout, stderr); code != 1 {
		t.Fatalf("Run(unknown) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "usage: reader") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestImportAndSourceList(t *testing.T) {
	rt := newTestRuntime(t)
	mock := newCLIMockSource(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runImport(rt, []string{"--json", mock.URL + "/sources.json"}, stdout, stderr); code != 0 {
		t.Fatalf("runImport() = %d, stderr: %s", code, stderr.String())
	}
	var result struct {
		Added int `json:"added"`
		Items []struct {
			SourceID string `json:"sourceId"`
			Status   string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("import --json invalid: %v\n%s", err, stdout.String())
	}
	if result.Added != 1 || len(result.Items) != 1 || result.Items[0].Status != "added" || result.Items[0].SourceID == "" {
		t.Fatalf("import result = %+v", result)
	}

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := runSourceList(rt, []string{"--json"}, stdout, stderr); code != 0 {
		t.Fatalf("runSourceList() = %d, stderr: %s", code, stderr.String())
	}
	var list struct {
		Total int `json:"total"`
		Items []struct {
			SourceID     string `json:"sourceId"`
			Name         string `json:"name"`
			Enabled      bool   `json:"enabled"`
			HasSearchURL bool   `json:"hasSearchUrl"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("source list --json invalid: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || !list.Items[0].Enabled || !list.Items[0].HasSearchURL {
		t.Fatalf("source list = %+v", list)
	}
}

func TestSearchJSONAndText(t *testing.T) {
	rt := newTestRuntime(t)
	mock := newCLIMockSource(t)
	importMockURL(t, rt, mock.URL)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runSearch(rt, []string{"斗破", "--json", "--limit", "5"}, stdout, stderr); code != 0 {
		t.Fatalf("runSearch() = %d, stderr: %s", code, stderr.String())
	}
	var out struct {
		Items []struct {
			Name       string `json:"name"`
			Author     string `json:"author"`
			BookURL    string `json:"bookUrl"`
			SourceID   string `json:"sourceId"`
			OriginName string `json:"originName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("search --json invalid: %v\n%s", err, stdout.String())
	}
	if len(out.Items) != 1 {
		t.Fatalf("search items = %d, want 1", len(out.Items))
	}
	item := out.Items[0]
	if item.Name != "斗破苍穹" || item.Author != "天蚕土豆" || !strings.HasSuffix(item.BookURL, "/book/1") || item.SourceID == "" {
		t.Fatalf("search item = %+v", item)
	}

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := runSearch(rt, []string{"斗破", "--limit", "5"}, stdout, stderr); code != 0 {
		t.Fatalf("runSearch(text) = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), item.SourceID) || !strings.Contains(stdout.String(), "斗破苍穹") {
		t.Fatalf("search text output = %q", stdout.String())
	}
}

func TestBookListInfoAndToc(t *testing.T) {
	rt := newTestRuntime(t)
	mock := newCLIMockSource(t)
	bookID := addMockBook(t, rt, mock)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runBookList(rt, []string{"--json"}, stdout, stderr); code != 0 {
		t.Fatalf("runBookList() = %d, stderr: %s", code, stderr.String())
	}
	var list struct {
		Items []struct {
			BookID string `json:"bookId"`
			Name   string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("book list --json invalid: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].BookID != bookID || list.Items[0].Name != "斗破苍穹" {
		t.Fatalf("book list = %+v", list)
	}

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := runToc(rt, []string{bookID, "--json"}, stdout, stderr); code != 0 {
		t.Fatalf("runToc() = %d, stderr: %s", code, stderr.String())
	}
	var toc struct {
		BookID   string `json:"bookId"`
		Count    int    `json:"count"`
		Chapters []struct {
			Index    int    `json:"index"`
			Title    string `json:"title"`
			IsVolume bool   `json:"isVolume"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &toc); err != nil {
		t.Fatalf("toc --json invalid: %v", err)
	}
	if toc.BookID != bookID || toc.Count != 3 || len(toc.Chapters) != 3 {
		t.Fatalf("toc = %+v", toc)
	}
	if !toc.Chapters[1].IsVolume || toc.Chapters[0].IsVolume || toc.Chapters[0].Title != "第一章" {
		t.Fatalf("toc chapters = %+v", toc.Chapters)
	}
}

func TestDownloadExportAndCache(t *testing.T) {
	rt := newTestRuntime(t)
	mock := newCLIMockSource(t)
	bookID := addMockBook(t, rt, mock)
	exportPath := filepath.Join(t.TempDir(), "out", "book.txt")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runDownload(rt, []string{bookID, "--from", "0", "--to", "2", "--export", exportPath, "--json"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("runDownload() = %d, stderr: %s", code, stderr.String())
	}
	var summary struct {
		BookID     string `json:"bookId"`
		From       int    `json:"from"`
		To         int    `json:"to"`
		Downloaded int    `json:"downloaded"`
		Cached     int    `json:"cached"`
		Skipped    int    `json:"skipped"`
		Failed     []struct {
			Index int `json:"index"`
		} `json:"failed"`
		Exported string `json:"exported"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("download --json invalid: %v\n%s", err, stdout.String())
	}
	if summary.Downloaded != 2 || summary.Cached != 0 || summary.Skipped != 1 || len(summary.Failed) != 0 {
		t.Fatalf("download summary = %+v", summary)
	}
	if summary.Exported != exportPath {
		t.Fatalf("exported = %q, want %q", summary.Exported, exportPath)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	text := string(data)
	for _, want := range []string{"=== 0 第一章", "真正正文一", "=== 2 第二章", "真正正文二"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export missing %q; got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "第一卷") {
		t.Fatalf("export must not contain volume chapter:\n%s", text)
	}
	if !strings.Contains(stderr.String(), "[1/3] 0 第一章") {
		t.Fatalf("progress stderr = %q", stderr.String())
	}

	// 重跑：全部命中缓存，不再抓取。
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := runDownload(rt, []string{bookID, "--from", "0", "--to", "2", "--export", exportPath, "--json"}, stdout, stderr); code != 0 {
		t.Fatalf("runDownload(rerun) = %d, stderr: %s", code, stderr.String())
	}
	summary = struct {
		BookID     string `json:"bookId"`
		From       int    `json:"from"`
		To         int    `json:"to"`
		Downloaded int    `json:"downloaded"`
		Cached     int    `json:"cached"`
		Skipped    int    `json:"skipped"`
		Failed     []struct {
			Index int `json:"index"`
		} `json:"failed"`
		Exported string `json:"exported"`
	}{}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("download rerun --json invalid: %v", err)
	}
	if summary.Downloaded != 0 || summary.Cached != 2 || summary.Skipped != 1 {
		t.Fatalf("rerun summary = %+v", summary)
	}
}

func TestDownloadChapterFailureExitsTwo(t *testing.T) {
	rt := newTestRuntime(t)
	mock := newCLIMockSource(t)
	mock.content2Status = http.StatusInternalServerError
	bookID := addMockBook(t, rt, mock)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runDownload(rt, []string{bookID, "--from", "0", "--to", "2", "--no-export", "--json"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("runDownload() = %d, want 2; stderr: %s", code, stderr.String())
	}
	var summary struct {
		Downloaded int `json:"downloaded"`
		Skipped    int `json:"skipped"`
		Failed     []struct {
			Index int    `json:"index"`
			Error string `json:"error"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("download --json invalid: %v\n%s", err, stdout.String())
	}
	if summary.Downloaded != 1 || summary.Skipped != 1 || len(summary.Failed) != 1 || summary.Failed[0].Index != 2 {
		t.Fatalf("failure summary = %+v", summary)
	}
}
