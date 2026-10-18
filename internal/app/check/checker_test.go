package checkapp

import (
	"context"
	"errors"
	"testing"
	"time"

	contentapp "reader/internal/app/content"
	diagnosticapp "reader/internal/app/diagnostic"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

func TestCheckerPassesNormalSource(t *testing.T) {
	checker := newTestChecker()
	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemPassed || item.FailureCode != "" {
		t.Fatalf("item = %#v", item)
	}
}

func TestCheckerClassifiesEmptySearch(t *testing.T) {
	checker := newTestChecker()
	checker.search.result = searchapp.MultiSearchResult{}
	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureSearchFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.FailureClass != diagnosticapp.FailureClassSourceSearchEmptyResult {
		t.Fatalf("failure class = %#v", item)
	}
	if item.Message != "empty search result" {
		t.Fatalf("message = %q", item.Message)
	}
}

func TestCheckerPreservesSearchWarning(t *testing.T) {
	checker := newTestChecker()
	checker.search.result = searchapp.MultiSearchResult{Warnings: []searchapp.SourceWarning{{Code: "search_failed", Message: "unexpected status: 403"}}}
	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err == nil {
		t.Fatal("expected search warning error")
	}
	if item.Message != "unexpected status: 403" || item.FailureClass != diagnosticapp.FailureClassSourceUpstreamHTTP403 {
		t.Fatalf("item = %#v", item)
	}
}

func TestCheckerUsesSourceCheckKeyword(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.RuleSearch.Value = &rule.SearchRule{CheckKeyWord: "源关键词"}
	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if checker.search.input.Keyword != "源关键词" || item.KeywordUsed != "源关键词" {
		t.Fatalf("input = %#v, item = %#v", checker.search.input, item)
	}
}

func TestCheckerIgnoresInvalidSourceCheckKeyword(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.RuleSearch.Value = &rule.SearchRule{CheckKeyWord: "https://example.test/query"}
	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if checker.search.input.Keyword != "斗破" || item.KeywordUsed != "斗破" {
		t.Fatalf("input = %#v, item = %#v", checker.search.input, item)
	}
}

func TestCheckerFileSourceRequiresDownloadURLsAndSkipsTocContent(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.BookSourceType = fileSourceType
	src.SearchURL = "/search"
	checker.books.infoBook = book.Book{
		BookURL:      "https://book.test/1",
		DownloadURLs: []string{"https://files.test/book.txt"},
	}

	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemPassed || checker.books.tocCalls != 0 || checker.content.calls != 0 {
		t.Fatalf("item=%#v tocCalls=%d contentCalls=%d", item, checker.books.tocCalls, checker.content.calls)
	}
	if stageStatus(item.Stages, task.CheckFlowSearch, task.CheckStageToc) != task.CheckItemSkipped || stageStatus(item.Stages, task.CheckFlowSearch, task.CheckStageContent) != task.CheckItemSkipped {
		t.Fatalf("stages = %#v", item.Stages)
	}
}

func TestCheckerFileSourceFailsWhenDownloadURLsAreEmpty(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.BookSourceType = fileSourceType
	src.SearchURL = "/search"
	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureClass != diagnosticapp.FailureClassSourceDownloadURLsEmpty {
		t.Fatalf("item = %#v", item)
	}
}

func TestCheckerFileSourceWithoutSearchIsSkipped(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.BookSourceType = fileSourceType
	src.SearchURL = ""
	src.ExploreURL = ""
	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemSkipped || checker.search.calls != 0 {
		t.Fatalf("item=%#v searchCalls=%d", item, checker.search.calls)
	}
}

func TestCheckerChecksSearchAndDiscoveryBookChainsIndependently(t *testing.T) {
	checker := newTestChecker()
	opts := defaultCheckOptions()
	opts.CheckDiscovery = true
	item, err := checker.CheckSource(context.Background(), testCheckSource(), opts)
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemPassed || checker.books.infoCalls != 2 || checker.books.tocCalls != 2 || checker.content.calls != 2 {
		t.Fatalf("item=%#v info=%d toc=%d content=%d", item, checker.books.infoCalls, checker.books.tocCalls, checker.content.calls)
	}
}

func TestCheckerClassifiesEmptyToc(t *testing.T) {
	checker := newTestChecker()
	checker.books.chapters = nil
	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureTocFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.FailureClass != diagnosticapp.FailureClassSourceTocEmpty {
		t.Fatalf("failure class = %#v", item)
	}
}

func TestCheckerClassifiesContentEmpty(t *testing.T) {
	checker := newTestChecker()
	checker.content.err = contentapp.ErrContentEmpty
	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureContentFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.FailureClass != diagnosticapp.FailureClassSourceContentEmpty {
		t.Fatalf("failure class = %#v", item)
	}
}

func TestCheckerPrefersSourceCheckKeyword(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.RuleSearch = rule.Flexible[rule.SearchRule]{Value: &rule.SearchRule{CheckKeyWord: " 校验词 "}}

	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemPassed {
		t.Fatalf("item = %#v", item)
	}
	if checker.search.input.Keyword != "校验词" {
		t.Fatalf("keyword = %q, want the source checkKeyWord", checker.search.input.Keyword)
	}
}

func TestCheckerFallsBackToRequestedKeywordAndDefault(t *testing.T) {
	checker := newTestChecker()
	if _, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions()); err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if checker.search.input.Keyword != "斗破" {
		t.Fatalf("keyword = %q, want requested keyword", checker.search.input.Keyword)
	}

	opts := defaultCheckOptions()
	opts.Keyword = ""
	if _, err := checker.CheckSource(context.Background(), testCheckSource(), opts); err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if checker.search.input.Keyword != DefaultCheckKeyword {
		t.Fatalf("keyword = %q, want %q", checker.search.input.Keyword, DefaultCheckKeyword)
	}
}

// TestCheckerAppliesDefaultTimeout 同时校验两侧口径：默认超时必须进入上下文 deadline（我方回归），
// 也必须落在条目的 timeoutMs 上（远端阶段化新增字段）。
func TestCheckerAppliesDefaultTimeout(t *testing.T) {
	search := &deadlineCheckSearch{}
	checker := NewChecker(search, &fakeCheckDiscovery{}, &fakeCheckBooks{}, &fakeCheckContent{})
	opts := defaultCheckOptions()
	opts.CheckInfo, opts.CheckToc, opts.CheckContent = false, false, false
	if _, err := checker.CheckSource(context.Background(), testCheckSource(), opts); err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if search.deadline.IsZero() {
		t.Fatal("search stage context has no deadline; default timeout is not applied")
	}
	remaining := time.Until(search.deadline)
	if remaining < DefaultTimeout-10*time.Second || remaining > DefaultTimeout {
		t.Fatalf("deadline remaining = %v, want about %v", remaining, DefaultTimeout)
	}

	item, err := newTestChecker().CheckSource(context.Background(), testCheckSource(), opts)
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.TimeoutMS != DefaultTimeout.Milliseconds() {
		t.Fatalf("timeoutMs = %d", item.TimeoutMS)
	}
}

func TestCheckerMarksEmptySearchRule(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.SearchURL = ""

	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureSearchRuleEmpty {
		t.Fatalf("item = %#v", item)
	}
	if checker.search.input.Keyword != "" {
		t.Fatalf("search was executed for a source without searchUrl: %#v", checker.search.input)
	}
}

func TestCheckerMarksEmptyDiscoveryRule(t *testing.T) {
	checker := newTestChecker()
	checker.discovery.categories = []discoveryapp.Category{}
	opts := defaultCheckOptions()
	opts.CheckSearch = false
	opts.CheckDiscovery = true

	item, err := checker.CheckSource(context.Background(), testCheckSource(), opts)
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureDiscoveryRuleEmpty {
		t.Fatalf("item = %#v", item)
	}
}

func TestCheckerFailsEarlyOnUnsupportedContentBrowserFlow(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.RuleContent = rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: ".content", SourceRegex: ".*\\.html"}}

	item, err := checker.CheckSource(context.Background(), src, defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureContentFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.FailureClass != diagnosticapp.FailureClassSourceUnsupportedRule {
		t.Fatalf("failure class = %#v", item)
	}
	if item.Message != contentapp.ErrUnsupportedContentBrowserFlow.Error() {
		t.Fatalf("message = %q", item.Message)
	}
	if item.Path != task.CheckPathSearch {
		t.Fatalf("path = %q, want search path", item.Path)
	}
	if checker.search.input.Keyword != "" {
		t.Fatalf("search ran before the unsupported content rule was reported: %#v", checker.search.input)
	}
}

func TestCheckerFailsEarlyOnUnsupportedContentBrowserFlowForDiscoveryChain(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.RuleContent = rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: ".content", SourceRegex: ".*"}}
	opts := defaultCheckOptions()
	opts.CheckSearch = false
	opts.CheckDiscovery = true

	item, err := checker.CheckSource(context.Background(), src, opts)
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed || item.FailureCode != FailureContentFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.Path != task.CheckPathDiscovery {
		t.Fatalf("path = %q, want discovery path", item.Path)
	}
}

func TestCheckerIgnoresUnsupportedContentRuleWhenContentCheckDisabled(t *testing.T) {
	checker := newTestChecker()
	src := testCheckSource()
	src.RuleContent = rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: ".content", SourceRegex: ".*"}}
	opts := defaultCheckOptions()
	opts.CheckContent = false

	item, err := checker.CheckSource(context.Background(), src, opts)
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemPassed || item.FailureCode != "" {
		t.Fatalf("item = %#v", item)
	}
	if checker.search.input.Keyword == "" {
		t.Fatal("search stage skipped although content check was disabled")
	}
}

func TestCheckerRecordsItemPathForBookStages(t *testing.T) {
	checker := newTestChecker()
	checker.content.err = contentapp.ErrContentEmpty

	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.FailureCode != FailureContentFailed || item.Path != task.CheckPathSearch {
		t.Fatalf("item = %#v", item)
	}

	discoveryChecker := newTestChecker()
	discoveryChecker.content.err = contentapp.ErrContentEmpty
	opts := defaultCheckOptions()
	opts.CheckSearch = false
	opts.CheckDiscovery = true
	item, err = discoveryChecker.CheckSource(context.Background(), testCheckSource(), opts)
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.FailureCode != FailureContentFailed || item.Path != task.CheckPathDiscovery {
		t.Fatalf("item = %#v", item)
	}
}

type deadlineCheckSearch struct {
	deadline time.Time
}

func (s *deadlineCheckSearch) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	if deadline, ok := ctx.Deadline(); ok {
		s.deadline = deadline
	}
	return searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}, nil
}

func TestCheckerUsesStageSpecificFailureCodeForInfoErrors(t *testing.T) {
	checker := newTestChecker()
	checker.books.infoErr = errors.New("unexpected status: 404")

	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.FailureCode != "info_failed" {
		t.Fatalf("failureCode = %q, want info_failed", item.FailureCode)
	}
	if item.FailureClass != diagnosticapp.FailureClassSourceUpstreamHTTP404 {
		t.Fatalf("failureClass = %q", item.FailureClass)
	}
}

func TestCheckerUsesStageSpecificFailureCodeForUnsupportedSearchOption(t *testing.T) {
	checker := newTestChecker()
	checker.search.err = errors.New("unsupported url option: webView")

	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err == nil {
		t.Fatal("expected stage failure to propagate as error for search request error")
	}
	if item.Status != task.CheckItemFailed {
		t.Fatalf("item = %#v", item)
	}
	if item.FailureCode != FailureSearchFailed {
		t.Fatalf("failureCode = %q, want %q", item.FailureCode, FailureSearchFailed)
	}
	if item.FailureClass != diagnosticapp.FailureClassUnsupportedBrowser {
		t.Fatalf("failureClass = %q", item.FailureClass)
	}
}

func TestClassifyCheckFailureRecognizesScriptErrors(t *testing.T) {
	details := diagnosticapp.ClassifyFailure(errors.New("script_failed: e2e: SyntaxError: Unexpected token"), "")
	if details.Code != FailureJSFailed {
		t.Fatalf("classifyCheckFailure(script) = %q", details.Code)
	}
	if details.Class != diagnosticapp.FailureClassSourceRuleScriptSyntax {
		t.Fatalf("classifyCheckFailure(script) class = %q", details.Class)
	}
}

func TestClassifyCheckFailureRecognizesDNSTlsAndStatusAsWebsiteFailures(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{`Get "https://x": dial tcp: lookup source.test: no such host`, diagnosticapp.FailureClassNetworkDNSFailure},
		{`Get "https://x": tls: failed to verify certificate: x509: certificate has expired or is not yet valid`, diagnosticapp.FailureClassNetworkTLSInvalid},
		{`unexpected status: 404`, diagnosticapp.FailureClassSourceUpstreamHTTP404},
	}
	for _, tt := range tests {
		details := diagnosticapp.ClassifyFailure(errors.New(tt.message), "")
		if details.Code != FailureWebsiteFailed {
			t.Fatalf("classifyCheckFailure(%q) = %q", tt.message, details.Code)
		}
		if details.Class != tt.want {
			t.Fatalf("classifyCheckFailure(%q) class = %q, want %q", tt.message, details.Class, tt.want)
		}
	}
}

func newTestChecker() *testChecker {
	search := &fakeCheckSearch{result: searchapp.MultiSearchResult{Items: []book.SearchBook{{BookURL: "https://book.test/1", Origin: "https://source.test", OriginName: "Source", Name: "斗破苍穹", Author: "天蚕土豆"}}}}
	discovery := &fakeCheckDiscovery{result: discoveryapp.Result{Items: []book.SearchBook{{BookURL: "https://book.test/d", Origin: "https://source.test", OriginName: "Source", Name: "发现书", Author: "作者"}}}}
	books := &fakeCheckBooks{chapters: []*book.BookChapter{{BookURL: "https://book.test/1", URL: "https://book.test/c/1", Title: "第一章", Index: 0}}}
	content := &fakeCheckContent{}
	return &testChecker{Checker: NewChecker(search, discovery, books, content), search: search, discovery: discovery, books: books, content: content}
}

type testChecker struct {
	*Checker
	search    *fakeCheckSearch
	discovery *fakeCheckDiscovery
	books     *fakeCheckBooks
	content   *fakeCheckContent
}

func defaultCheckOptions() Options {
	return Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}
}

func testCheckSource() *source.BookSource {
	return &source.BookSource{SourceID: "source-id", BookSourceURL: "https://source.test", BookSourceName: "Source", SearchURL: "https://source.test/search", ExploreURL: "https://source.test/discovery"}
}

type fakeCheckSearch struct {
	result searchapp.MultiSearchResult
	err    error
	input  searchapp.MultiSearchInput
	calls  int
}

func (s *fakeCheckSearch) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	s.input = input
	s.calls++
	if s.err != nil {
		return searchapp.MultiSearchResult{}, s.err
	}
	return s.result, nil
}

type fakeCheckDiscovery struct {
	result     discoveryapp.Result
	err        error
	categories []discoveryapp.Category
}

func (d *fakeCheckDiscovery) Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error) {
	if d.err != nil {
		return discoveryapp.Result{}, d.err
	}
	return d.result, nil
}

func (d *fakeCheckDiscovery) Categories(ctx context.Context, sourceID string) ([]discoveryapp.Category, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.categories != nil {
		return d.categories, nil
	}
	return []discoveryapp.Category{{Title: "玄幻", URL: "https://source.test/discovery"}}, nil
}

type fakeCheckBooks struct {
	chapters  []*book.BookChapter
	infoBook  book.Book
	infoErr   error
	tocErr    error
	infoCalls int
	tocCalls  int
	// 记录最后一次收到的 source/book，用于断言内存链路不再走书架。
	gotSource *source.BookSource
	gotBook   book.Book
}

func (b *fakeCheckBooks) FetchInfo(ctx context.Context, src *source.BookSource, item book.Book) (book.Book, error) {
	b.infoCalls++
	b.gotSource, b.gotBook = src, item
	if b.infoBook.BookURL == "" {
		b.infoBook = book.Book{BookURL: "https://book.test/1", Name: "斗破苍穹", Author: "天蚕土豆"}
	}
	return b.infoBook, b.infoErr
}

func (b *fakeCheckBooks) FetchToc(ctx context.Context, src *source.BookSource, item book.Book) ([]*book.BookChapter, error) {
	b.tocCalls++
	b.gotSource, b.gotBook = src, item
	return b.chapters, b.tocErr
}

type fakeCheckContent struct {
	err         error
	calls       int
	chapter     book.BookChapter
	nextChapter string
}

func (c *fakeCheckContent) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (contentapp.Result, error) {
	c.calls++
	c.chapter, c.nextChapter = chapter, nextChapterURL
	return contentapp.Result{Content: "正文", Chapter: chapter}, c.err
}

func stageStatus(stages []task.CheckStageResult, flow task.CheckStageFlow, stage task.CheckStageName) task.CheckTaskItemStatus {
	for _, value := range stages {
		if value.Flow == flow && value.Stage == stage {
			return value.Status
		}
	}
	return ""
}

// 搜索结果已带 tocUrl 时跳过详情抓取（对齐 Legado CheckSourceService.kt:207），
// 目录与正文仍按内存链路执行。
func TestCheckerSkipsInfoWhenSearchResultHasTocURL(t *testing.T) {
	checker := newTestChecker()
	checker.search.result = searchapp.MultiSearchResult{Items: []book.SearchBook{{
		BookURL: "https://book.test/1",
		TocURL:  "https://book.test/toc/1",
		Origin:  "https://source.test",
		Name:    "斗破苍穹",
		Author:  "天蚕土豆",
	}}}

	item, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions())
	if err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if item.Status != task.CheckItemPassed {
		t.Fatalf("item = %#v", item)
	}
	if status := stageStatus(item.Stages, task.CheckFlowSearch, task.CheckStageInfo); status != task.CheckItemSkipped {
		t.Fatalf("info stage = %q, want skipped", status)
	}
	if checker.books.infoCalls != 0 {
		t.Fatalf("infoFetches = %d, want 0", checker.books.infoCalls)
	}
	if checker.books.tocCalls != 1 || checker.content.calls != 1 {
		t.Fatalf("toc=%d content=%d", checker.books.tocCalls, checker.content.calls)
	}
}

// 文件源的 tocUrl 不是目录入口，仍必须抓详情校验下载链接。
func TestCheckerFetchesInfoForFileSourceEvenWithTocURL(t *testing.T) {
	checker := newTestChecker()
	checker.search.result = searchapp.MultiSearchResult{Items: []book.SearchBook{{
		BookURL: "https://book.test/1",
		TocURL:  "https://book.test/toc/1",
		Origin:  "https://source.test",
		Name:    "斗破苍穹",
		Author:  "天蚕土豆",
	}}}
	checker.books.infoBook = book.Book{BookURL: "https://book.test/1", DownloadURLs: []string{"https://files.test/book.txt"}}
	src := testCheckSource()
	src.BookSourceType = fileSourceType

	if _, err := checker.CheckSource(context.Background(), src, defaultCheckOptions()); err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if checker.books.infoCalls != 1 {
		t.Fatalf("infoFetches = %d, want 1", checker.books.infoCalls)
	}
}

// 正文校验用内存章节：第一个非卷章节 + 下一章 URL，全程不触碰章节表与正文缓存。
func TestCheckerContentUsesFirstNonVolumeChapterInMemory(t *testing.T) {
	checker := newTestChecker()
	checker.books.chapters = []*book.BookChapter{
		{BookURL: "https://book.test/1", Title: "第一卷", URL: "https://book.test/volume", Index: 0, IsVolume: true},
		{BookURL: "https://book.test/1", Title: "第一章", URL: "https://book.test/c/1", Index: 1},
		{BookURL: "https://book.test/1", Title: "第二章", URL: "https://book.test/c/2", Index: 2},
	}

	if _, err := checker.CheckSource(context.Background(), testCheckSource(), defaultCheckOptions()); err != nil {
		t.Fatalf("CheckSource() error = %v", err)
	}
	if checker.content.calls != 1 {
		t.Fatalf("contentCalls = %d", checker.content.calls)
	}
	if checker.content.chapter.Index != 1 || checker.content.chapter.URL != "https://book.test/c/1" {
		t.Fatalf("chapter = %#v", checker.content.chapter)
	}
	if checker.content.nextChapter != "https://book.test/c/2" {
		t.Fatalf("nextChapterURL = %q", checker.content.nextChapter)
	}
}
