package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	domainfetch "reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/fetcher"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

const defaultRealCollectionURL = "https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361"

type realE2EReport struct {
	TestTime             string                     `json:"testTime"`
	CollectionURL        string                     `json:"collectionUrl"`
	HTTPStatus           int                        `json:"httpStatus"`
	ContentType          string                     `json:"contentType"`
	ResponseBytes        int                        `json:"responseBytes"`
	ParsedCount          int                        `json:"parsedCount"`
	ImportAdded          int                        `json:"importAdded"`
	ImportUpdated        int                        `json:"importUpdated"`
	ImportSkipped        int                        `json:"importSkipped"`
	ImportFailed         int                        `json:"importFailed"`
	CandidateRule        string                     `json:"candidateRule"`
	MaxCandidates        int                        `json:"maxCandidates"`
	TimeoutSeconds       int                        `json:"timeoutSeconds"`
	ContinueAfterSuccess bool                       `json:"continueAfterSuccess"`
	Candidates           []realCandidate            `json:"candidates"`
	AttemptedCandidates  int                        `json:"attemptedCandidates"`
	FullSuccessCount     int                        `json:"fullSuccessCount"`
	StageSummary         map[string]realStageCounts `json:"stageSummary"`
	Keyword              string                     `json:"keyword"`
	StageResults         []realStageResult          `json:"stageResults"`
	FailureSummary       map[string]int             `json:"failureSummary,omitempty"`
	ReleaseAllowed       bool                       `json:"releaseAllowed"`
	FailureClass         string                     `json:"failureClass,omitempty"`
}

type realCandidate struct {
	SourceID       string                `json:"sourceId"`
	BookSourceURL  string                `json:"bookSourceUrl"`
	BookSourceName string                `json:"bookSourceName"`
	KeywordUsed    string                `json:"keywordUsed,omitempty"`
	Features       realCandidateFeatures `json:"features"`
}

type realCandidateFeatures struct {
	SearchURLRelative          bool `json:"searchUrlRelative"`
	SearchURLHasAtJS           bool `json:"searchUrlHasAtJs"`
	SearchURLHasInlineExpr     bool `json:"searchUrlHasInlineExpr"`
	SearchURLHasSourceRef      bool `json:"searchUrlHasSourceRef"`
	SearchURLHasJavaHelper     bool `json:"searchUrlHasJavaHelper"`
	SearchURLHasURLOptions     bool `json:"searchUrlHasUrlOptions"`
	SearchURLRequestsWebView   bool `json:"searchUrlRequestsWebView"`
	HeaderHasJS                bool `json:"headerHasJs"`
	RuleSearchHasJS            bool `json:"ruleSearchHasJs"`
	RuleBookInfoHasJS          bool `json:"ruleBookInfoHasJs"`
	RuleTocHasJS               bool `json:"ruleTocHasJs"`
	RuleContentHasJS           bool `json:"ruleContentHasJs"`
	RuleContentHasJavaImporter bool `json:"ruleContentHasJavaImporter"`
}

type realStageResult struct {
	SourceID       string `json:"sourceId"`
	BookSourceURL  string `json:"bookSourceUrl,omitempty"`
	BookSourceName string `json:"bookSourceName,omitempty"`
	KeywordUsed    string `json:"keywordUsed,omitempty"`
	Stage          string `json:"stage"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`
	FailureClass   string `json:"failureClass,omitempty"`
}

type realStageCounts struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type captureFetcher struct {
	base     domainfetch.Fetcher
	lastReq  *domainfetch.Request
	lastResp *domainfetch.Response
}

func (f *captureFetcher) Fetch(ctx context.Context, req domainfetch.Request) (*domainfetch.Response, error) {
	reqCopy := req
	if req.Headers != nil {
		reqCopy.Headers = cloneStringMap(req.Headers)
	}
	if req.Body != nil {
		reqCopy.Body = append([]byte(nil), req.Body...)
	}
	f.lastReq = &reqCopy
	resp, err := f.base.Fetch(ctx, req)
	if resp != nil {
		respCopy := *resp
		if resp.Headers != nil {
			respCopy.Headers = resp.Headers.Clone()
		}
		if resp.Body != nil {
			respCopy.Body = append([]byte(nil), resp.Body...)
		}
		f.lastResp = &respCopy
	}
	return resp, err
}

func TestRealSourceCandidateSelection(t *testing.T) {
	valid := &domainsource.BookSource{SourceID: "s1", BookSourceURL: "https://source.example.test", BookSourceName: "valid", Enabled: true, BookSourceType: 0, SearchURL: "https://source.example.test/search"}
	valid.RuleSearch.Value = &rule.SearchRule{BookList: ".item"}
	valid.RuleBookInfo.Value = &rule.BookInfoRule{Name: "h1"}
	valid.RuleToc.Value = &rule.TocRule{ChapterList: ".chapter"}
	valid.RuleContent.Value = &rule.ContentRule{Content: ".content"}
	webview := *valid
	webview.SourceID = "s4"
	webview.SearchURL = `https://www.baidu.com/s?wd=site%3Asource.example.test+{{key}},{"webView":true}`
	disabled := *valid
	disabled.SourceID = "s2"
	disabled.Enabled = false
	noRules := &domainsource.BookSource{SourceID: "s3", BookSourceURL: "https://source3.example.test", BookSourceName: "no rules", Enabled: true, BookSourceType: 0}

	got := selectRealCandidates([]*domainsource.BookSource{&disabled, noRules, &webview, valid}, 10)

	if len(got) != 1 || got[0].SourceID != "s1" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestRealSourceReportWriters(t *testing.T) {
	dir := t.TempDir()
	report := realE2EReport{
		TestTime:       "2026-05-03T00:00:00Z",
		CollectionURL:  defaultRealCollectionURL,
		HTTPStatus:     200,
		ResponseBytes:  123,
		ParsedCount:    2,
		Keyword:        "斗破苍穹",
		ReleaseAllowed: true,
		Candidates:     []realCandidate{{SourceID: "s1", BookSourceName: "Source"}},
	}
	report.addStageResult(realStageResult{SourceID: "s1", BookSourceName: "Source", KeywordUsed: "我的", Stage: "content", Status: "passed"})
	report.addStageResult(realStageResult{SourceID: "s2", BookSourceName: "Blocked", BookSourceURL: "https://api-bc.wtzw.com＃妍希", KeywordUsed: "我的", Stage: "search", Status: "failed", Message: `Get "https://api-bc.wtzw.com%EF%BC%83%E5%A6%8D%E5%B8%8C/search": dial tcp: lookup api-bc.wtzw.com＃妍希: no such host`})

	jsonPath, mdPath, err := writeRealE2EReports(dir, report)
	if err != nil {
		t.Fatalf("write reports: %v", err)
	}
	for _, path := range []string{jsonPath, mdPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing report %s: %v", path, err)
		}
	}
	rawJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	if !strings.Contains(string(rawJSON), `"failureClass": "source_url_host_polluted"`) {
		t.Fatalf("json report missing failure class: %s", string(rawJSON))
	}
	rawMD, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown report: %v", err)
	}
	if !strings.Contains(string(rawMD), "source_url_host_polluted") {
		t.Fatalf("markdown report missing failure summary: %s", string(rawMD))
	}
}

func TestClassifyRealE2EFailure(t *testing.T) {
	if got := classifyRealE2EFailure(domainfetch.ErrBlocked); got != "security_policy_blocked" {
		t.Fatalf("blocked classified as %q", got)
	}
	if got := classifyRealE2EFailure(context.DeadlineExceeded); got != "network_unavailable_or_timeout" {
		t.Fatalf("timeout classified as %q", got)
	}
	if got := classifyRealE2EFailure(errors.New("rule parse failed")); got != "implementation_or_rule_failed" {
		t.Fatalf("generic classified as %q", got)
	}
}

func TestClassifyRealStageFailure(t *testing.T) {
	tests := []struct {
		name   string
		result realStageResult
		want   string
	}{
		{
			name:   "source auth failed",
			result: realStageResult{Stage: "toc", Status: "failed", Message: `{"msg":"认证失败","code":4005,"data":[]}`},
			want:   "source_authorization_failed",
		},
		{
			name:   "polluted host wins",
			result: realStageResult{Stage: "search", Status: "failed", BookSourceURL: "https://api-bc.wtzw.com＃妍希", Message: `Get "https://api-bc.wtzw.com/search": dial tcp: lookup api-bc.wtzw.com＃妍希: no such host`},
			want:   "source_url_host_polluted",
		},
		{
			name:   "tls invalid",
			result: realStageResult{Stage: "search", Status: "failed", Message: `Get "https://x": tls: failed to verify certificate: x509: certificate has expired or is not yet valid`},
			want:   "network_tls_invalid",
		},
		{
			name:   "timeout",
			result: realStageResult{Stage: "search", Status: "failed", Message: `Get "https://x": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`},
			want:   "network_timeout",
		},
		{
			name:   "connection reset",
			result: realStageResult{Stage: "search", Status: "failed", Message: `Post "https://x": read tcp 1.1.1.1:1->2.2.2.2:2: read: connection reset by peer`},
			want:   "network_connection_reset",
		},
		{
			name:   "syntax error",
			result: realStageResult{Stage: "search", Status: "failed", Message: `empty search result; warnings: search_failed: script_failed: e2e: SyntaxError: Unexpected end of JSON input (EOF) at parse (native)`},
			want:   "source_upstream_empty_json_response",
		},
		{
			name:   "real syntax error",
			result: realStageResult{Stage: "search", Status: "failed", Message: `empty search result; warnings: search_failed: script_failed: e2e: SyntaxError: SyntaxError: (anonymous): Line 48:39 Unexpected identifier (and 1 more errors)`},
			want:   "source_rule_script_syntax_error",
		},
		{
			name:   "non json upstream response",
			result: realStageResult{Stage: "content", Status: "failed", Message: `script_failed: e2e: SyntaxError: invalid character 'G' looking for beginning of value at parse (native)`},
			want:   "source_upstream_non_json_response",
		},
		{
			name:   "http 400",
			result: realStageResult{Stage: "search", Status: "failed", Message: `empty search result; warnings: search_failed: unexpected status: 400`},
			want:   "source_upstream_http_400",
		},
		{
			name:   "empty result",
			result: realStageResult{Stage: "search", Status: "failed", Message: `empty search result`},
			want:   "source_search_empty_result",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyRealStageFailure(tt.result); got != tt.want {
				t.Fatalf("classifyRealStageFailure() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRealE2ETimeoutScalesWithCandidateCount(t *testing.T) {
	t.Setenv("READER_REAL_E2E_TIMEOUT_SECONDS", "")
	if got := realE2ETimeout(10); got != 2*time.Minute {
		t.Fatalf("timeout(10) = %s", got)
	}
	if got := realE2ETimeout(100); got < 8*time.Minute {
		t.Fatalf("timeout(100) = %s, want scaled timeout", got)
	}
	t.Setenv("READER_REAL_E2E_TIMEOUT_SECONDS", "7")
	if got := realE2ETimeout(100); got != 7*time.Second {
		t.Fatalf("env timeout = %s", got)
	}
}

func TestEffectiveRealKeywordPrefersSourceCheckKeyword(t *testing.T) {
	src := &domainsource.BookSource{}
	src.RuleSearch.Value = &rule.SearchRule{CheckKeyWord: "深空彼岸"}
	if got := firstRealKeyword(src, "斗破苍穹"); got != "深空彼岸" {
		t.Fatalf("firstRealKeyword() = %q", got)
	}
	src.RuleSearch.Value.CheckKeyWord = ""
	if got := firstRealKeyword(src, "斗破苍穹"); got == "" {
		t.Fatalf("fallback keyword is empty")
	}
}

func TestEffectiveRealKeywordsAddsSourceDerivedFallbacks(t *testing.T) {
	src := &domainsource.BookSource{BookSourceName: "民间故事（优+）", BookSourceURL: "https://www.6mj.com"}
	got := effectiveRealKeywords(src, "斗破苍穹")
	wantPrefix := []string{"民间故事", "怪男孩@绘本", "小熊嘟嘟上幼儿园@听书"}
	for i, want := range wantPrefix {
		if i >= len(got) || got[i] != want {
			t.Fatalf("keywords = %#v", got)
		}
	}
	if got[len(got)-1] != "斗破苍穹" {
		t.Fatalf("fallback missing: %#v", got)
	}
}

func TestEffectiveRealKeywordsAddsTraditionalClassicFallbacks(t *testing.T) {
	classic := &domainsource.BookSource{BookSourceName: "国学書库（优+）", BookSourceURL: "https://book.mywebos.cn"}
	gotClassic := effectiveRealKeywords(classic, "斗破苍穹")
	if len(gotClassic) < 3 || gotClassic[0] != "论语" || gotClassic[1] != "三国演义" {
		t.Fatalf("classic keywords = %#v", gotClassic)
	}

	novel := &domainsource.BookSource{BookSourceName: "得奇小说（优+++）", BookSourceURL: "https://www.deqixs.co"}
	gotNovel := effectiveRealKeywords(novel, "斗破苍穹")
	if len(gotNovel) != 1 || gotNovel[0] != "斗破苍穹" {
		t.Fatalf("novel keywords = %#v", gotNovel)
	}
}

func TestRealSourceSingleDebug(t *testing.T) {
	if os.Getenv("READER_REAL_E2E_ENABLED") != "true" {
		t.Skip("set READER_REAL_E2E_ENABLED=true to run real source debug")
	}
	sourceID := strings.TrimSpace(os.Getenv("READER_REAL_E2E_DEBUG_SOURCE_ID"))
	if sourceID == "" {
		t.Skip("set READER_REAL_E2E_DEBUG_SOURCE_ID=src_xxx to run single-source debug")
	}
	collectionURL := getenvDefault("READER_REAL_E2E_SOURCE_COLLECTION_URL", defaultRealCollectionURL)
	keyword := getenvDefault("READER_REAL_E2E_KEYWORD", "斗破苍穹")
	timeout := realE2ETimeout(1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	env := newE2EEnv(t)
	httpFetcher := fetcher.NewHTTPFetcher(fetcher.WithDefaultUserAgent("Reader-Real-E2E"))
	resp, err := httpFetcher.Fetch(ctx, domainfetch.Request{URL: collectionURL})
	if err != nil {
		t.Fatalf("fetch collection: %v", err)
	}
	importResult, err := env.importer.Import(ctx, sourceapp.ImportInput{Payload: resp.Body})
	if err != nil {
		t.Fatalf("import collection: %v", err)
	}
	t.Logf("imported added=%d updated=%d skipped=%d failed=%d", importResult.Added, importResult.Updated, importResult.Skipped, importResult.Failed)

	src, err := env.sources.FindByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("find source: %v", err)
	}
	if src == nil {
		t.Fatalf("source %s not found", sourceID)
	}
	t.Logf("source name=%s url=%s", src.BookSourceName, src.BookSourceURL)
	searchCapture := &captureFetcher{base: httpFetcher}
	debugSearchRuntime := script.NewRuntime(script.Policy{Timeout: 10 * time.Second, Stage: "e2e-search-capture", Fetcher: searchCapture, SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src})
	debugSearcher := searchapp.NewSingleSourceSearcher(searchCapture, debugSearchRuntime)
	if _, debugErr := debugSearcher.Search(ctx, searchapp.SearchInput{Source: src, Keyword: keyword, Page: 1}); debugErr != nil {
		t.Logf("captured search error: %v", debugErr)
	}
	if searchCapture.lastReq != nil {
		body := string(searchCapture.lastReq.Body)
		if len(body) > 240 {
			body = body[:240]
		}
		t.Logf("captured search req method=%s url=%s headers=%v body=%q", searchCapture.lastReq.Method, searchCapture.lastReq.URL, searchCapture.lastReq.Headers, body)
	}
	if searchCapture.lastResp != nil {
		body := string(searchCapture.lastResp.Body)
		if len(body) > 240 {
			body = body[:240]
		}
		t.Logf("captured search resp status=%d url=%s body=%q", searchCapture.lastResp.StatusCode, searchCapture.lastResp.URL, body)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(src.SearchURL)), "@js:") {
		scriptText := strings.TrimSpace(src.SearchURL[4:])
		debugRuntime := script.NewRuntime(script.Policy{Timeout: 10 * time.Second, Stage: "e2e-search-debug", Fetcher: httpFetcher, SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src})
		rawURLValue, evalErr := debugRuntime.EvalWithBindings(ctx, scriptText, script.Bindings{
			"key":      keyword,
			"page":     1,
			"source":   script.SourceBindings(src),
			"__source": src,
		})
		if evalErr != nil {
			t.Logf("raw searchUrl eval error: %v", evalErr)
		} else {
			rawSearchURL := fmt.Sprint(rawURLValue)
			t.Logf("raw searchUrl=%s", rawSearchURL)
			custom := urlrule.ParseCustom(rawSearchURL)
			searchReq := domainfetch.Request{URL: urlrule.Absolute(src.BookSourceURL, custom.URL), Method: "GET", Headers: map[string]string{}}
			if method, ok := custom.Attribute["method"].(string); ok && strings.TrimSpace(method) != "" {
				searchReq.Method = strings.ToUpper(strings.TrimSpace(method))
			}
			if headers, ok := custom.Attribute["headers"]; ok {
				switch typed := headers.(type) {
				case map[string]any:
					for key, value := range typed {
						searchReq.Headers[key] = fmt.Sprint(value)
					}
				}
			}
			if body, ok := custom.Attribute["body"].(string); ok {
				searchReq.Body = []byte(body)
			}
			rawSearchResp, fetchErr := httpFetcher.Fetch(ctx, searchReq)
			if fetchErr != nil {
				t.Logf("raw search fetch error: %v", fetchErr)
			} else {
				rawSearchBody := string(rawSearchResp.Body)
				if len(rawSearchBody) > 240 {
					rawSearchBody = rawSearchBody[:240]
				}
				t.Logf("raw search status=%d url=%s body=%q", rawSearchResp.StatusCode, rawSearchResp.URL, rawSearchBody)
			}
		}
	}
	searchResult, err := env.search.Search(ctx, searchInputForReal(sourceID, keyword))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	t.Logf("search items=%d warnings=%v", len(searchResult.Items), searchResult.Warnings)
	if len(searchResult.Items) == 0 {
		t.Fatal("search returned no items")
	}
	bookCandidate := searchResult.Items[0]
	t.Logf("search bookUrl=%s tocUrl=%s variable=%s", bookCandidate.BookURL, bookCandidate.TocURL, bookCandidate.Variable)

	saved, err := env.books.Save(ctx, ptr(bookCandidate.ToBook()))
	if err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(saved.BookURL)
	infoBook, err := env.books.RefreshInfo(ctx, bookID)
	if err != nil {
		infoReq := debugRequestFromCustomURL(src, bookCandidate.BookURL)
		rawInfoResp, fetchErr := httpFetcher.Fetch(ctx, infoReq)
		if fetchErr != nil {
			t.Fatalf("refresh info: %v; debug raw info fetch: %v", err, fetchErr)
		}
		rawInfoBody := string(rawInfoResp.Body)
		if len(rawInfoBody) > 240 {
			rawInfoBody = rawInfoBody[:240]
		}
		t.Logf("raw info error status=%d url=%s method=%s body=%q", rawInfoResp.StatusCode, rawInfoResp.URL, infoReq.Method, rawInfoBody)
		t.Fatalf("refresh info: %v", err)
	}
	t.Logf("info bookUrl=%s tocUrl=%s cover=%s variable=%s", infoBook.BookURL, infoBook.TocURL, infoBook.CoverURL, infoBook.Variable)
	infoReq := debugRequestFromCustomURL(src, bookCandidate.BookURL)
	rawInfoResp, err := httpFetcher.Fetch(ctx, infoReq)
	if err != nil {
		t.Fatalf("debug raw info fetch: %v", err)
	}
	rawInfoBody := string(rawInfoResp.Body)
	if len(rawInfoBody) > 240 {
		rawInfoBody = rawInfoBody[:240]
	}
	t.Logf("raw info status=%d url=%s body=%q", rawInfoResp.StatusCode, rawInfoResp.URL, rawInfoBody)
	infoAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{RedirectURL: rawInfoResp.URL, SourceURL: src.BookSourceURL})
	infoContent := any(string(rawInfoResp.Body))
	if initRule := strings.TrimSpace(src.RuleBookInfo.Value.Init); initRule != "" {
		if elements, err := infoAnalyzer.GetElements(ctx, initRule, string(rawInfoResp.Body)); err == nil && len(elements) > 0 {
			infoContent = elements[0]
		} else {
			t.Logf("raw info init error=%v elements=%d", err, len(elements))
		}
	}
	debugTocURL, err := infoAnalyzer.GetString(ctx, src.RuleBookInfo.Value.TocURL, infoContent)
	if err != nil {
		t.Logf("raw info tocUrl eval error: %v", err)
	} else {
		t.Logf("raw info tocUrl eval=%q", debugTocURL)
	}

	chapters, refreshed, err := env.books.RefreshToc(ctx, bookID)
	if err != nil {
		t.Logf("toc error: %v", err)
		t.Logf("toc source header=%q", src.Header)
		tocReq := domainfetch.Request{URL: infoBook.TocURL, Headers: map[string]string{}}
		if raw := strings.TrimSpace(src.Header); raw != "" {
			headers := map[string]string{}
			if err := json.Unmarshal([]byte(raw), &headers); err == nil {
				for key, value := range headers {
					tocReq.Headers[key] = value
				}
			} else {
				for key, value := range urlrule.ParseAttributeObject(raw) {
					tocReq.Headers[key] = fmt.Sprint(value)
				}
			}
		}
		rawTocResp, fetchErr := httpFetcher.Fetch(ctx, tocReq)
		if fetchErr != nil {
			t.Fatalf("refresh toc: %v; debug raw toc fetch: %v", err, fetchErr)
		}
		rawTocBody := string(rawTocResp.Body)
		if len(rawTocBody) > 240 {
			rawTocBody = rawTocBody[:240]
		}
		t.Logf("raw toc status=%d url=%s body=%q", rawTocResp.StatusCode, rawTocResp.URL, rawTocBody)
		t.Fatalf("refresh toc: %v", err)
	}
	t.Logf("toc chapters=%d refreshed=%v", len(chapters), refreshed)
	if len(chapters) == 0 {
		t.Fatal("toc returned no chapters")
	}
	t.Logf("first chapter title=%s url=%s base=%s variable=%s", chapters[0].Title, chapters[0].URL, chapters[0].BaseURL, chapters[0].Variable)
	t.Logf("ruleContent content=%q title=%q next=%q", src.RuleContent.Value.Content, src.RuleContent.Value.Title, src.RuleContent.Value.NextContentURL)
	if strings.Contains(src.RuleContent.Value.Content, "downloadChapter") {
		downloadURL, buildErr := extractDownloadChapterURLFromContentRule(ctx, httpFetcher, src, infoBook, chapters[0], chapters[0].BaseURL)
		if buildErr != nil {
			t.Logf("content debug build download url error: %v", buildErr)
		} else if strings.TrimSpace(downloadURL) != "" {
			t.Logf("content debug download url=%s", downloadURL)
			downloadReq := debugRequestFromCustomURL(src, downloadURL)
			downloadResp, fetchErr := httpFetcher.Fetch(ctx, downloadReq)
			if fetchErr != nil {
				t.Logf("content debug first ajax fetch error: %v", fetchErr)
			} else {
				downloadBody := string(downloadResp.Body)
				if len(downloadBody) > 240 {
					downloadBody = downloadBody[:240]
				}
				t.Logf("content debug first ajax status=%d url=%s body=%q", downloadResp.StatusCode, downloadResp.URL, downloadBody)
			}
		}
	}

	contentReq := debugRequestFromCustomURL(src, chapters[0].URL)
	rawResp, err := httpFetcher.Fetch(ctx, contentReq)
	if err != nil {
		t.Fatalf("debug raw content fetch: %v", err)
	}
	rawBody := string(rawResp.Body)
	if len(rawBody) > 240 {
		rawBody = rawBody[:240]
	}
	t.Logf("raw content status=%d url=%s body=%q", rawResp.StatusCode, rawResp.URL, rawBody)
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{RedirectURL: rawResp.URL, SourceURL: src.BookSourceURL})
	preview, err := analyzer.GetString(ctx, src.RuleContent.Value.Content, string(rawResp.Body))
	if err != nil {
		t.Logf("direct analyzer error: %v", err)
	} else {
		if len(preview) > 120 {
			preview = preview[:120]
		}
		t.Logf("direct analyzer preview=%q", preview)
	}
	rawResp2, err := httpFetcher.Fetch(ctx, contentReq)
	if err != nil {
		t.Fatalf("debug second raw content fetch: %v", err)
	}
	rawBody2 := string(rawResp2.Body)
	if len(rawBody2) > 240 {
		rawBody2 = rawBody2[:240]
	}
	t.Logf("raw content second status=%d url=%s body=%q", rawResp2.StatusCode, rawResp2.URL, rawBody2)

	content, resolvedBook, err := env.content.GetChapterContent(ctx, bookID, chapters[0].Index)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	t.Logf("content len=%d chapter=%s url=%s book=%s", len(content.Content), content.Chapter.Title, content.Chapter.URL, resolvedBook.BookURL)
}

func debugRequestFromCustomURL(src *domainsource.BookSource, rawURL string) domainfetch.Request {
	custom := urlrule.ParseCustom(rawURL)
	req := domainfetch.Request{URL: custom.URL, Method: "GET", Headers: map[string]string{}}
	if raw := strings.TrimSpace(src.Header); raw != "" {
		headers := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &headers); err == nil {
			for key, value := range headers {
				req.Headers[key] = value
			}
		} else {
			for key, value := range urlrule.ParseAttributeObject(raw) {
				req.Headers[key] = fmt.Sprint(value)
			}
		}
	}
	for key, value := range custom.Attribute {
		switch strings.ToLower(key) {
		case "method":
			if method, ok := value.(string); ok && strings.TrimSpace(method) != "" {
				req.Method = strings.ToUpper(strings.TrimSpace(method))
			}
		case "body":
			switch body := value.(type) {
			case string:
				req.Body = []byte(body)
			case map[string]any, []any:
				if raw, err := json.Marshal(body); err == nil {
					req.Body = raw
					if req.Headers["Content-Type"] == "" {
						req.Headers["Content-Type"] = "application/json"
					}
				}
			}
		case "headers":
			if headers, ok := value.(map[string]any); ok {
				for name, headerValue := range headers {
					req.Headers[name] = fmt.Sprint(headerValue)
				}
			}
		}
	}
	req.URL = urlrule.Absolute(src.BookSourceURL, req.URL)
	return req
}

func extractDownloadChapterURLFromContentRule(ctx context.Context, httpFetcher *fetcher.HTTPFetcher, src *domainsource.BookSource, b book.Book, chapter *book.BookChapter, baseURL string) (string, error) {
	ruleText := strings.TrimSpace(src.RuleContent.Value.Content)
	if !strings.HasPrefix(strings.ToLower(ruleText), "@js:") {
		return "", nil
	}
	scriptText := strings.TrimSpace(ruleText[4:])
	target := `java.ajax(JSON.parse(java.ajax(url)).data.contentUrl)`
	if !strings.Contains(scriptText, target) {
		return "", nil
	}
	scriptText = strings.Replace(scriptText, target, "url", 1)
	runtime := script.NewRuntime(script.Policy{Timeout: 10 * time.Second, Stage: "e2e-content-debug", Fetcher: httpFetcher})
	value, err := runtime.EvalWithBindings(ctx, scriptText, script.Bindings{
		"result":   "",
		"baseUrl":  baseURL,
		"source":   script.SourceBindings(src),
		"__source": src,
		"book":     &b,
		"chapter":  chapter,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprint(value), nil
}

func TestRealSourceCollectionE2E(t *testing.T) {
	if os.Getenv("READER_REAL_E2E_ENABLED") != "true" {
		t.Skip("set READER_REAL_E2E_ENABLED=true to run real source collection release gate")
	}
	collectionURL := getenvDefault("READER_REAL_E2E_SOURCE_COLLECTION_URL", defaultRealCollectionURL)
	keyword := getenvDefault("READER_REAL_E2E_KEYWORD", "斗破苍穹")
	maxCandidates := getenvIntDefault("READER_REAL_E2E_MAX_CANDIDATES", 10)
	continueAfterSuccess := getenvBoolDefault("READER_REAL_E2E_CONTINUE_AFTER_SUCCESS", false)
	timeout := realE2ETimeout(maxCandidates)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	env := newE2EEnv(t)
	httpFetcher := fetcher.NewHTTPFetcher(fetcher.WithDefaultUserAgent("Reader-Real-E2E"))
	resp, fetchErr := httpFetcher.Fetch(ctx, domainfetch.Request{URL: collectionURL})
	report := realE2EReport{
		TestTime:             time.Now().UTC().Format(time.RFC3339),
		CollectionURL:        collectionURL,
		Keyword:              keyword,
		MaxCandidates:        maxCandidates,
		TimeoutSeconds:       int(timeout.Seconds()),
		ContinueAfterSuccess: continueAfterSuccess,
		StageSummary:         map[string]realStageCounts{},
		CandidateRule:        "enabled=true, bookSourceType=0, http(s) bookSourceUrl, searchUrl/ruleSearch/ruleBookInfo/ruleToc/ruleContent present, explicit browser/webView search flows excluded per Requirement 3.2",
	}
	if fetchErr != nil {
		report.FailureClass = classifyRealE2EFailure(fetchErr)
		writeRealReportOrFatal(t, report)
		t.Fatalf("fetch real collection: %v", fetchErr)
	}
	report.HTTPStatus = resp.StatusCode
	report.ContentType = resp.Headers.Get("Content-Type")
	report.ResponseBytes = len(resp.Body)

	var rawSources []json.RawMessage
	if err := json.Unmarshal(resp.Body, &rawSources); err != nil {
		report.FailureClass = "collection_parse_failed"
		writeRealReportOrFatal(t, report)
		t.Fatalf("parse collection: %v", err)
	}
	report.ParsedCount = len(rawSources)
	importResult, err := env.importer.Import(ctx, sourceapp.ImportInput{Payload: resp.Body})
	if err != nil {
		report.FailureClass = classifyRealE2EFailure(err)
		writeRealReportOrFatal(t, report)
		t.Fatalf("import collection: %v", err)
	}
	report.ImportAdded = importResult.Added
	report.ImportUpdated = importResult.Updated
	report.ImportSkipped = importResult.Skipped
	report.ImportFailed = importResult.Failed

	sources := make([]*domainsource.BookSource, 0, len(rawSources))
	for _, raw := range rawSources {
		src, err := domainsource.ParseBookSource(raw)
		if err == nil {
			sources = append(sources, src)
		}
	}
	candidates := selectRealCandidates(sources, maxCandidates)
	report.Candidates = mapRealCandidates(candidates)
	for _, candidate := range candidates {
		report.AttemptedCandidates++
		var (
			sourceKeyword string
			result        searchapp.MultiSearchResult
			err           error
			searchMessage string
			searchPassed  bool
		)
		for _, candidateKeyword := range effectiveRealKeywords(candidate, keyword) {
			sourceKeyword = candidateKeyword
			result, err = env.search.Search(ctx, searchInputForReal(candidate.SourceID, sourceKeyword))
			if err != nil {
				searchMessage = err.Error()
				continue
			}
			if len(result.Items) == 0 {
				searchMessage = realSearchFailureMessage(result.Warnings)
				continue
			}
			searchPassed = true
			break
		}
		if !searchPassed {
			report.addStageResult(newRealStageResult(candidate, sourceKeyword, "search", "failed", searchMessage))
			continue
		}
		report.addStageResult(newRealStageResult(candidate, sourceKeyword, "search", "passed", ""))
		saved, err := env.books.Save(ctx, ptr(result.Items[0].ToBook()))
		if err != nil {
			report.addStageResult(newRealStageResult(candidate, sourceKeyword, "save", "failed", err.Error()))
			continue
		}
		bookID := book.IDFromURL(saved.BookURL)
		if _, err := env.books.RefreshInfo(ctx, bookID); err != nil {
			report.addStageResult(newRealStageResult(candidate, sourceKeyword, "info", "failed", err.Error()))
			continue
		}
		report.addStageResult(newRealStageResult(candidate, sourceKeyword, "info", "passed", ""))
		chapters, _, err := env.books.RefreshToc(ctx, bookID)
		if err != nil || len(chapters) == 0 {
			report.addStageResult(newRealStageResult(candidate, sourceKeyword, "toc", "failed", errString(err, "empty toc")))
			continue
		}
		report.addStageResult(newRealStageResult(candidate, sourceKeyword, "toc", "passed", ""))
		content, _, err := env.content.GetChapterContent(ctx, bookID, chapters[0].Index)
		if err != nil || strings.TrimSpace(content.Content) == "" {
			report.addStageResult(newRealStageResult(candidate, sourceKeyword, "content", "failed", errString(err, "empty content")))
			continue
		}
		report.addStageResult(newRealStageResult(candidate, sourceKeyword, "content", "passed", "length="+strconvItoa(len(content.Content))))
		report.FullSuccessCount++
		report.ReleaseAllowed = true
		if !continueAfterSuccess {
			break
		}
	}
	if !report.ReleaseAllowed {
		report.FailureClass = "implementation_or_rule_failed"
	}
	writeRealReportOrFatal(t, report)
	if !report.ReleaseAllowed {
		t.Fatalf("real source collection E2E did not find a successful candidate; report failureClass=%s", report.FailureClass)
	}
}

func (r *realE2EReport) addStageResult(result realStageResult) {
	if result.Status == "failed" && result.FailureClass == "" {
		result.FailureClass = classifyRealStageFailure(result)
	}
	r.StageResults = append(r.StageResults, result)
	if r.StageSummary == nil {
		r.StageSummary = map[string]realStageCounts{}
	}
	counts := r.StageSummary[result.Stage]
	switch result.Status {
	case "passed":
		counts.Passed++
	case "failed":
		counts.Failed++
		if result.FailureClass != "" {
			if r.FailureSummary == nil {
				r.FailureSummary = map[string]int{}
			}
			r.FailureSummary[result.FailureClass]++
		}
	}
	r.StageSummary[result.Stage] = counts
}

func newRealStageResult(candidate *domainsource.BookSource, keywordUsed string, stage string, status string, message string) realStageResult {
	result := realStageResult{
		KeywordUsed: keywordUsed,
		Stage:       stage,
		Status:      status,
		Message:     message,
	}
	if candidate != nil {
		result.SourceID = candidate.SourceID
		result.BookSourceURL = candidate.BookSourceURL
		result.BookSourceName = candidate.BookSourceName
	}
	return result
}

func selectRealCandidates(sources []*domainsource.BookSource, max int) []*domainsource.BookSource {
	out := []*domainsource.BookSource{}
	for _, src := range sources {
		if src == nil || !src.Enabled || src.BookSourceType != 0 {
			continue
		}
		parsed, err := url.Parse(src.BookSourceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		if strings.TrimSpace(src.SearchURL) == "" || src.RuleSearch.Value == nil || src.RuleBookInfo.Value == nil || src.RuleToc.Value == nil || src.RuleContent.Value == nil {
			continue
		}
		if requiresUnsupportedBrowserFlow(src) {
			continue
		}
		out = append(out, src)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func mapRealCandidates(candidates []*domainsource.BookSource) []realCandidate {
	out := make([]realCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, realCandidate{
			SourceID:       candidate.SourceID,
			BookSourceURL:  candidate.BookSourceURL,
			BookSourceName: candidate.BookSourceName,
			KeywordUsed:    firstRealKeyword(candidate, ""),
			Features:       detectRealCandidateFeatures(candidate),
		})
	}
	return out
}

func firstRealKeyword(candidate *domainsource.BookSource, fallback string) string {
	keywords := effectiveRealKeywords(candidate, fallback)
	if len(keywords) == 0 {
		return fallback
	}
	return keywords[0]
}

func effectiveRealKeywords(candidate *domainsource.BookSource, fallback string) []string {
	keywords := []string{}
	appendKeyword := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range keywords {
			if existing == value {
				return
			}
		}
		keywords = append(keywords, value)
	}
	if candidate != nil && candidate.RuleSearch.Value != nil {
		appendKeyword(candidate.RuleSearch.Value.CheckKeyWord)
	}
	lowerName := ""
	lowerURL := ""
	if candidate != nil {
		lowerName = strings.ToLower(strings.TrimSpace(candidate.BookSourceName))
		lowerURL = strings.ToLower(strings.TrimSpace(candidate.BookSourceURL))
	}
	switch {
	case strings.Contains(lowerName, "故事") || strings.Contains(lowerURL, "gushi"):
		appendKeyword("民间故事")
		appendKeyword("怪男孩@绘本")
		appendKeyword("小熊嘟嘟上幼儿园@听书")
	case strings.Contains(lowerName, "诗") || strings.Contains(lowerURL, "gushiwen") || strings.Contains(lowerURL, "shici"):
		appendKeyword("蝶恋花")
		appendKeyword("李白")
		appendKeyword("菜根谭")
	case strings.Contains(lowerName, "国学") || strings.Contains(lowerName, "古典") || strings.Contains(lowerName, "书库") || strings.Contains(lowerName, "書库") || strings.Contains(lowerName, "典藏"):
		appendKeyword("论语")
		appendKeyword("三国演义")
		appendKeyword("菜根谭")
	case strings.Contains(lowerName, "盐选"):
		appendKeyword("回答")
	case strings.Contains(lowerName, "轻小说") || strings.Contains(lowerName, "文库"):
		appendKeyword("刀剑神域")
		appendKeyword("春物")
		appendKeyword("魔法禁书目录")
		appendKeyword("关于我转生变成史莱姆这档事")
	case strings.Contains(lowerName, "百合"):
		appendKeyword("百合")
	case strings.Contains(lowerName, "武侠") || strings.Contains(lowerName, "金庸") || strings.Contains(lowerName, "古龙"):
		appendKeyword("天龙八部")
		appendKeyword("小李飞刀")
	}
	appendKeyword(fallback)
	return keywords
}

func detectRealCandidateFeatures(src *domainsource.BookSource) realCandidateFeatures {
	searchURL := strings.TrimSpace(src.SearchURL)
	lowerSearchURL := strings.ToLower(searchURL)
	header := strings.TrimSpace(src.Header)
	return realCandidateFeatures{
		SearchURLRelative:          strings.HasPrefix(searchURL, "/"),
		SearchURLHasAtJS:           strings.HasPrefix(lowerSearchURL, "@js:") || strings.Contains(lowerSearchURL, "<js>"),
		SearchURLHasInlineExpr:     hasComplexInlineExpression(searchURL),
		SearchURLHasSourceRef:      strings.Contains(searchURL, "source."),
		SearchURLHasJavaHelper:     strings.Contains(searchURL, "java."),
		SearchURLHasURLOptions:     strings.Contains(searchURL, ",{"),
		SearchURLRequestsWebView:   strings.Contains(lowerSearchURL, "webview"),
		HeaderHasJS:                strings.HasPrefix(strings.ToLower(header), "@js:") || strings.HasPrefix(strings.ToLower(header), "<js>"),
		RuleSearchHasJS:            ruleTextHasJS(src.RuleSearch.Value),
		RuleBookInfoHasJS:          ruleTextHasJS(src.RuleBookInfo.Value),
		RuleTocHasJS:               ruleTextHasJS(src.RuleToc.Value),
		RuleContentHasJS:           ruleTextHasJS(src.RuleContent.Value),
		RuleContentHasJavaImporter: strings.Contains(strings.ToLower(ruleText(src.RuleContent.Value)), "javaimporter") || strings.Contains(ruleText(src.RuleContent.Value), "Packages."),
	}
}

func requiresUnsupportedBrowserFlow(src *domainsource.BookSource) bool {
	features := detectRealCandidateFeatures(src)
	if features.SearchURLRequestsWebView {
		return true
	}
	if src.RuleContent.Value != nil && strings.TrimSpace(src.RuleContent.Value.SourceRegex) != "" {
		return true
	}
	searchChainText := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(src.LoginCheckJS),
		ruleText(src.RuleSearch.Value),
	}, "\n"))
	return strings.Contains(searchChainText, "startbrowserawait") ||
		strings.Contains(searchChainText, "java.webview(") ||
		strings.Contains(searchChainText, "webviewgetsource") ||
		strings.Contains(searchChainText, "webviewgetoverrideurl")
}

func hasComplexInlineExpression(rule string) bool {
	remainder := rule
	for {
		start := strings.Index(remainder, "{{")
		if start < 0 {
			return false
		}
		remainder = remainder[start+2:]
		end := strings.Index(remainder, "}}")
		if end < 0 {
			return true
		}
		expr := strings.TrimSpace(remainder[:end])
		switch expr {
		case "key", "page", "source.key", "source.bookSourceUrl":
		default:
			return true
		}
		remainder = remainder[end+2:]
	}
}

func ruleTextHasJS(value any) bool {
	text := strings.ToLower(ruleText(value))
	return strings.Contains(text, "@js:") || strings.Contains(text, "<js>")
}

func ruleText(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func realSearchFailureMessage(warnings []searchapp.SourceWarning) string {
	if len(warnings) == 0 {
		return "empty search result"
	}
	parts := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		parts = append(parts, strings.TrimSpace(warning.Code+": "+warning.Message))
	}
	return "empty search result; warnings: " + strings.Join(parts, " | ")
}

func writeRealE2EReports(dir string, report realE2EReport) (string, string, error) {
	report = normalizeRealE2EReport(report)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	jsonPath := filepath.Join(dir, "real_source_collection_e2e_report.json")
	mdPath := filepath.Join(dir, "real_source_collection_e2e_report.md")
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		return "", "", err
	}
	md := "# Real Source Collection E2E Report\n\n" +
		"- Test time: " + report.TestTime + "\n" +
		"- Collection URL: " + report.CollectionURL + "\n" +
		"- HTTP status: " + strconvItoa(report.HTTPStatus) + "\n" +
		"- Response bytes: " + strconvItoa(report.ResponseBytes) + "\n" +
		"- Parsed count: " + strconvItoa(report.ParsedCount) + "\n" +
		"- Max candidates: " + strconvItoa(report.MaxCandidates) + "\n" +
		"- Attempted candidates: " + strconvItoa(report.AttemptedCandidates) + "\n" +
		"- Full success count: " + strconvItoa(report.FullSuccessCount) + "\n" +
		"- Keyword: " + report.Keyword + "\n" +
		"- Release allowed: " + boolString(report.ReleaseAllowed) + "\n" +
		"- Failure class: " + report.FailureClass + "\n"
	if len(report.StageSummary) > 0 {
		md += "\n## Stage Summary\n\n"
		for _, stage := range []string{"search", "save", "info", "toc", "content"} {
			counts, ok := report.StageSummary[stage]
			if !ok {
				continue
			}
			md += "- " + stage + ": passed=" + strconvItoa(counts.Passed) + ", failed=" + strconvItoa(counts.Failed) + "\n"
		}
	}
	if len(report.FailureSummary) > 0 {
		md += "\n## Failure Summary\n\n"
		for _, item := range sortedFailureSummary(report.FailureSummary) {
			md += "- " + item.Name + ": " + strconvItoa(item.Count) + "\n"
		}
	}
	failed := failedStageResults(report.StageResults)
	if len(failed) > 0 {
		md += "\n## Failed Stages\n\n"
		for _, item := range failed {
			label := item.SourceID
			if strings.TrimSpace(item.BookSourceName) != "" {
				label = item.BookSourceName + " (" + item.SourceID + ")"
			}
			md += "- [" + item.Stage + "] " + label
			if item.FailureClass != "" {
				md += " [" + item.FailureClass + "]"
			}
			if strings.TrimSpace(item.Message) != "" {
				md += ": " + item.Message
			}
			md += "\n"
		}
	}
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, mdPath, nil
}

func writeRealReportOrFatal(t *testing.T, report realE2EReport) {
	t.Helper()
	if _, _, err := writeRealE2EReports(filepath.Join("..", "reports"), report); err != nil {
		t.Fatalf("write real e2e report: %v", err)
	}
}

func classifyRealE2EFailure(err error) string {
	switch {
	case errors.Is(err, domainfetch.ErrBlocked):
		return "security_policy_blocked"
	case errors.Is(err, context.DeadlineExceeded):
		return "network_unavailable_or_timeout"
	default:
		return "implementation_or_rule_failed"
	}
}

func normalizeRealE2EReport(report realE2EReport) realE2EReport {
	if len(report.StageResults) == 0 {
		return report
	}
	report.StageSummary = map[string]realStageCounts{}
	report.FailureSummary = map[string]int{}
	normalized := make([]realStageResult, 0, len(report.StageResults))
	for _, result := range report.StageResults {
		if result.Status == "failed" && result.FailureClass == "" {
			result.FailureClass = classifyRealStageFailure(result)
		}
		normalized = append(normalized, result)
		counts := report.StageSummary[result.Stage]
		switch result.Status {
		case "passed":
			counts.Passed++
		case "failed":
			counts.Failed++
			if result.FailureClass != "" {
				report.FailureSummary[result.FailureClass]++
			}
		}
		report.StageSummary[result.Stage] = counts
	}
	if len(report.FailureSummary) == 0 {
		report.FailureSummary = nil
	}
	report.StageResults = normalized
	return report
}

func classifyRealStageFailure(result realStageResult) string {
	if result.Status != "failed" {
		return ""
	}
	message := strings.TrimSpace(result.Message)
	lower := strings.ToLower(message)
	switch {
	case hasPollutedSourceHost(result.BookSourceURL) && (strings.Contains(lower, "no such host") || strings.Contains(lower, "lookup ")):
		return "source_url_host_polluted"
	case strings.Contains(message, "认证失败") || strings.Contains(message, `"code":4005`) || strings.Contains(lower, "unexpected status: 401"):
		return "source_authorization_failed"
	case strings.Contains(lower, "fetch blocked"):
		return "security_policy_blocked"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "server misbehaving"):
		return "network_dns_failure"
	case strings.Contains(lower, "tls: failed to verify certificate") || strings.Contains(lower, "x509:"):
		return "network_tls_invalid"
	case strings.Contains(lower, "context deadline exceeded") || strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "client.timeout exceeded while awaiting headers"):
		return "network_timeout"
	case strings.Contains(lower, "connection reset by peer"):
		return "network_connection_reset"
	case strings.Contains(lower, "unexpected status: 400"):
		return "source_upstream_http_400"
	case strings.Contains(lower, "unexpected status: 403"):
		return "source_upstream_http_403"
	case strings.Contains(lower, "unexpected status: 404"):
		return "source_upstream_http_404"
	case strings.Contains(lower, "unexpected status: 500"):
		return "source_upstream_http_500"
	case strings.Contains(lower, "unexpected end of json input"):
		return "source_upstream_empty_json_response"
	case strings.Contains(lower, "invalid character") && strings.Contains(lower, "looking for beginning of value at parse"):
		return "source_upstream_non_json_response"
	case strings.Contains(lower, "script_failed:") && strings.Contains(lower, "syntaxerror"):
		return "source_rule_script_syntax_error"
	case strings.Contains(lower, "script_failed:"):
		return "source_rule_script_failed"
	case message == "toc_empty" || strings.Contains(lower, "empty toc"):
		return "source_toc_empty"
	case message == "content_empty" || strings.Contains(lower, "empty content"):
		return "source_content_empty"
	case strings.HasPrefix(lower, "empty search result"):
		if strings.Contains(lower, "unexpected end of json input") {
			return "source_upstream_empty_json_response"
		}
		return "source_search_empty_result"
	default:
		return "implementation_or_rule_failed"
	}
}

func hasPollutedSourceHost(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if i := strings.Index(raw, "##"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.Index(raw, "#"); i >= 0 {
		raw = raw[:i]
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		host = parsed.Host
	}
	if host == "" {
		return false
	}
	for _, r := range host {
		if r > unicode.MaxASCII {
			return true
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			continue
		}
		return true
	}
	return false
}

type namedCount struct {
	Name  string
	Count int
}

func sortedFailureSummary(summary map[string]int) []namedCount {
	items := make([]namedCount, 0, len(summary))
	for name, count := range summary {
		items = append(items, namedCount{Name: name, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})
	return items
}

func failedStageResults(results []realStageResult) []realStageResult {
	out := make([]realStageResult, 0, len(results))
	for _, result := range results {
		if result.Status == "failed" {
			out = append(out, result)
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func getenvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getenvIntDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func realE2ETimeout(maxCandidates int) time.Duration {
	if seconds := getenvIntDefault("READER_REAL_E2E_TIMEOUT_SECONDS", 0); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	timeout := 2 * time.Minute
	scaled := time.Duration(maxCandidates*5) * time.Second
	if scaled > timeout {
		timeout = scaled
	}
	return timeout
}

func getenvBoolDefault(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func searchInputForReal(sourceID string, keyword string) searchapp.MultiSearchInput {
	return searchapp.MultiSearchInput{Keyword: keyword, Page: 1, SourceIDs: []string{sourceID}, EnabledOnly: false, Concurrency: 1}
}

func errString(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func strconvItoa(value int) string {
	return strconv.FormatInt(int64(value), 10)
}
