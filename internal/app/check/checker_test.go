package checkapp

import (
	"context"
	"errors"
	"testing"

	contentapp "reader/internal/app/content"
	diagnosticapp "reader/internal/app/diagnostic"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	"reader/internal/domain/book"
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
	if item.FailureClass != diagnosticapp.FailureClassSourceUnsupportedRule {
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
	return &source.BookSource{SourceID: "source-id", BookSourceURL: "https://source.test", BookSourceName: "Source", ExploreURL: "https://source.test/discovery"}
}

type fakeCheckSearch struct {
	result searchapp.MultiSearchResult
	err    error
}

func (s *fakeCheckSearch) Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error) {
	if s.err != nil {
		return searchapp.MultiSearchResult{}, s.err
	}
	return s.result, nil
}

type fakeCheckDiscovery struct {
	result discoveryapp.Result
	err    error
}

func (d *fakeCheckDiscovery) Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error) {
	if d.err != nil {
		return discoveryapp.Result{}, d.err
	}
	return d.result, nil
}

type fakeCheckBooks struct {
	chapters []*book.BookChapter
	saveErr  error
	infoErr  error
	tocErr   error
}

func (b *fakeCheckBooks) Save(ctx context.Context, item *book.Book) (*book.Book, error) {
	if b.saveErr != nil {
		return nil, b.saveErr
	}
	return item, nil
}

func (b *fakeCheckBooks) RefreshInfo(ctx context.Context, id string) (book.Book, error) {
	return book.Book{BookURL: "https://book.test/1", Name: "斗破苍穹", Author: "天蚕土豆"}, b.infoErr
}

func (b *fakeCheckBooks) RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error) {
	return b.chapters, &book.Book{BookURL: "https://book.test/1", Name: "斗破苍穹", Author: "天蚕土豆"}, b.tocErr
}

type fakeCheckContent struct {
	err error
}

func (c *fakeCheckContent) GetChapterContent(ctx context.Context, id string, index int) (contentapp.Result, *book.Book, error) {
	return contentapp.Result{Content: "正文", Chapter: book.BookChapter{Index: index}}, &book.Book{}, c.err
}
