package searchapp

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/ratelimit"
	"reader/internal/infra/script"
)

func TestSingleSourceSearchParsesMockHTML(t *testing.T) {
	src := searchTestSource()
	var lastURL string
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=斗破&page=1",
		Body: []byte(`<div class="book">
			<a class="name" href="/book/1">斗破苍穹</a>
			<span class="author">天蚕土豆</span>
			<span class="kind">玄幻</span>
			<span class="last">第一章</span>
			<p class="intro">少年故事</p>
		</div>`),
	}, lastURL: &lastURL}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	got := books[0]
	if got.Name != "斗破苍穹" || got.Author != "天蚕土豆" || got.BookURL != "https://example.test/book/1" || got.Origin != src.BookSourceURL {
		t.Fatalf("book = %#v", got)
	}
	if !strings.Contains(lastURL, "q="+url.QueryEscape("斗破")) || !strings.Contains(lastURL, "page=1") {
		t.Fatalf("lastURL = %q", lastURL)
	}
}

func TestSingleSourceSearchEmptyListWithoutBookInfoRuleDoesNotPanic(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = ".missing"
	src.RuleBookInfo.Value = nil
	searcher := NewSingleSourceSearcher(fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=test&page=1",
		Body:       []byte(`<html><body>no results</body></html>`),
	}}, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "test", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchStoresPutVariables(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = "$.data"
	src.RuleSearch.Value.Name = "$.name@put:{id:id,name:name,author:author}"
	src.RuleSearch.Value.Author = "$.author"
	src.RuleSearch.Value.BookURL = "https://example.test/book/@get:{id}"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=斗破&page=1",
		Body:       []byte(`{"data":{"id":7,"name":"斗破苍穹","author":"天蚕土豆"}}`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	if books[0].BookURL != "https://example.test/book/7" || !strings.Contains(books[0].Variable, `"id":"7"`) {
		t.Fatalf("book = %#v", books[0])
	}
}

func TestSingleSourceSearchJSCanWriteSearchBookVariables(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = "$.data[*]"
	src.RuleSearch.Value.Name = `@js:book.putVariable("bid", result.id); result.name`
	src.RuleSearch.Value.Author = "$.author"
	src.RuleSearch.Value.BookURL = `https://example.test/book/{{java.get('bid')}}`
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=斗破&page=1",
		Body:       []byte(`{"data":[{"id":"7","name":"斗破苍穹","author":"天蚕土豆"}]}`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	if books[0].BookURL != "https://example.test/book/7" || !strings.Contains(books[0].Variable, `"bid":"7"`) {
		t.Fatalf("book = %#v", books[0])
	}
}

func TestSingleSourceSearchSupportsLeadingAtAttributeThenJSStep(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "http://m.suixkan.com"
	src.RuleSearch.Value.BookURL = "@onclick@js:result.match(/\\('(.*?)', '', ''\\)/)[1]"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "http://m.suixkan.com/s/1.html?keyword=斗破苍穹&page=1",
		Body: []byte(`<div class="book" onclick="openBook('/book/42.html', '', '')">
			<a class="name">斗破苍穹</a>
			<span class="author">天蚕土豆</span>
		</div>`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破苍穹", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	if books[0].BookURL != "http://m.suixkan.com/book/42.html" {
		t.Fatalf("bookURL = %q", books[0].BookURL)
	}
}

func TestSingleSourceSearchWrapsJSoupElementsInBookListScript(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://m.suixkan.com"
	src.SearchURL = "/s/1.html?keyword={{key}}"
	src.RuleSearch.Value.BookList = `class.v-list-item
@js:
list=result.toArray();
list1=[];
for(i in list){
  if(list[i].text().indexOf("斗罗")>-1){ list1.push(list[i]); }
}
list1.map(x=>x)`
	src.RuleSearch.Value.Name = ".v-title@text"
	src.RuleSearch.Value.Author = ".v-author@text"
	src.RuleSearch.Value.BookURL = `##="newWebView\('([^']+)'##$1###`
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://m.suixkan.com/s/1.html?keyword=斗罗大陆",
		Body:       []byte(`<div class="v-list-item" onclick="newWebView('/b/1.html', '', '')"><div class="v-title">斗罗大陆</div><div class="v-author">唐家三少</div></div>`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗罗大陆", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 || books[0].Name != "斗罗大陆" || books[0].Author != "唐家三少" {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchPreservesCustomBookURLAttributesWithTrailingSuffix(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://api.midureader.com/#@遇知"
	src.RuleSearch.Value.BookList = "$.data[*]"
	src.RuleSearch.Value.Name = "$.title"
	src.RuleSearch.Value.BookURL = "https://api.midureader.com/fiction/book/getDetail,{\n  \"method\": \"POST\",\n  \"body\": \"app=midu&book_id={{$..book_id}}\"\n}##(getBrief||getDetail)选用其中之一"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://api.midureader.com/fiction/search/searchV2",
		Body:       []byte(`{"data":[{"title":"斗破苍穹","book_id":"229221"}]}`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破苍穹", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	if !strings.HasPrefix(books[0].BookURL, `https://api.midureader.com/fiction/book/getDetail,{"body":"app=midu`) {
		t.Fatalf("bookURL = %q", books[0].BookURL)
	}
	if !strings.Contains(books[0].BookURL, `book_id=229221`) || !strings.Contains(books[0].BookURL, `"method":"POST"`) {
		t.Fatalf("bookURL = %q", books[0].BookURL)
	}
}

func TestSingleSourceSearchPreservesMultilineCustomBookURLAttributesWithoutTrailingSuffix(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://vreader.vivo.com.cn/"
	src.RuleSearch.Value.BookList = "$.data[*]"
	src.RuleSearch.Value.Name = "$.title"
	src.RuleSearch.Value.BookURL = "https://vreader.vivo.com.cn/book/detail.do,{\n  \"method\": \"POST\",\n  \"body\": \"{\\\"bookId\\\":\\\"{{$.bookId}}\\\",\\\"order\\\":1}\"\n}"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://vreader.vivo.com.cn/book/search.do",
		Body:       []byte(`{"data":[{"title":"斗破苍穹","bookId":"N1122344671000280502"}]}`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破苍穹", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	if !strings.HasPrefix(books[0].BookURL, `https://vreader.vivo.com.cn/book/detail.do,{"body":"{\"bookId\":\"N1122344671000280502\",\"order\":1}"`) {
		t.Fatalf("bookURL = %q", books[0].BookURL)
	}
	if !strings.Contains(books[0].BookURL, `"method":"POST"`) {
		t.Fatalf("bookURL = %q", books[0].BookURL)
	}
}

func TestSingleSourceSearchUsesFirstNonEmptyBookURLValue(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = "$.data"
	src.RuleSearch.Value.Name = "$.name"
	src.RuleSearch.Value.BookURL = "$.urls[*]"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=斗破&page=1",
		Body:       []byte(`{"data":{"name":"斗破苍穹","urls":["https://example.test/book/1","https://example.test/author/2"]}}`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 || books[0].BookURL != "https://example.test/book/1" {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchNormalizesTemplatedRelativeBookURL(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = "$.data[*]"
	src.RuleSearch.Value.Name = "$.novelName"
	src.RuleSearch.Value.BookURL = "/novel/{{$.novelId}}?isSearch=1"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "http://api.jmlldsc.com/search?keyword=斗破&page=1",
		Body:       []byte(`{"data":[{"novelId":42,"novelName":"斗破苍穹"}]}`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 || books[0].BookURL != "http://api.jmlldsc.com/novel/42?isSearch=1" {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchBookURLPatternDirectInfo(t *testing.T) {
	src := searchTestSource()
	src.BookURLPattern = `https://example\.test/book/\d+`
	fetcher := fakeFetcher{resp: &fetch.Response{StatusCode: 200, URL: "https://example.test/book/1", Body: []byte(searchDetailHTML())}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 || books[0].BookURL != "https://example.test/book/1" || books[0].OriginName != src.BookSourceName {
		t.Fatalf("books = %#v", books)
	}
	if books[0].Name != "斗破苍穹" || books[0].Author != "天蚕土豆" {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchBookURLPatternRequiresWholeMatch(t *testing.T) {
	src := searchTestSource()
	// Legado 用 baseUrl.matches(pattern) 做整串匹配：`book/\d+` 不应命中以 book/1 开头的列表页。
	src.BookURLPattern = `book/\d+`
	fetcher := fakeFetcher{resp: &fetch.Response{StatusCode: 200, URL: "https://example.test/book/1/other", Body: []byte(`<div class="book"><a class="name" href="/book/2">列表书</a><span class="author">作者</span></div>`)}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 || books[0].Name != "列表书" || books[0].BookURL != "https://example.test/book/2" {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchRejectsInvalidBookURLPattern(t *testing.T) {
	src := searchTestSource()
	src.BookURLPattern = `(`
	searcher := NewSingleSourceSearcher(fakeFetcher{resp: &fetch.Response{StatusCode: 200, URL: "https://example.test/list"}}, nil)

	_, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书", Page: 1})
	if err == nil || !strings.Contains(err.Error(), "bookUrlPattern") {
		t.Fatalf("error = %v, want bookUrlPattern error", err)
	}
}

func TestSingleSourceSearchSkipsInfoFallbackWhenBookURLPatternConfigured(t *testing.T) {
	src := searchTestSource()
	src.BookURLPattern = `https://example\.test/detail/\d+`
	fetcher := fakeFetcher{resp: &fetch.Response{StatusCode: 200, URL: "https://example.test/list?q=书", Body: []byte(searchDetailHTML())}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("books = %#v, want empty without info fallback", books)
	}
}

func TestSingleSourceSearchFallsBackToInfoWhenBookListEmpty(t *testing.T) {
	src := searchTestSource()
	fetcher := fakeFetcher{resp: &fetch.Response{StatusCode: 200, URL: "https://example.test/detail/1", Body: []byte(searchDetailHTML())}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 || books[0].Name != "斗破苍穹" || books[0].Author != "天蚕土豆" {
		t.Fatalf("books = %#v", books)
	}
}

func TestSingleSourceSearchEmptySearchURL(t *testing.T) {
	searcher := NewSingleSourceSearcher(fakeFetcher{}, nil)
	_, err := searcher.Search(context.Background(), SearchInput{Source: &source.BookSource{}})
	if !errors.Is(err, ErrEmptySearchURL) {
		t.Fatalf("error = %v, want ErrEmptySearchURL", err)
	}
}

func TestBuildSearchRequestResolvesRelativeURLAndSourceKey(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://api-x.shrtxs.cn/qd/"
	src.SearchURL = "{{source.key}}/search.php?keyword={{key}}&page={{page}}"

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破苍穹", 2)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://api-x.shrtxs.cn/qd/search.php?keyword="+url.QueryEscape("斗破苍穹")+"&page=2" {
		t.Fatalf("url = %q", req.URL)
	}
}

func TestBuildSearchRequestStripsSourceSuffixForRelativeURL(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://cn.ttkan.co/##@遇知"
	src.SearchURL = "/novel/search?q={{key}}"

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://cn.ttkan.co/novel/search?q="+url.QueryEscape("斗破苍穹") {
		t.Fatalf("url = %q", req.URL)
	}
}

func TestBuildSearchRequestStripsFullWidthSourceSuffixForRelativeURL(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://api-bc.wtzw.com＃妍希"
	src.SearchURL = "/search/v1/words?wd={{key}}"

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://api-bc.wtzw.com/search/v1/words?wd="+url.QueryEscape("斗破苍穹") {
		t.Fatalf("url = %q", req.URL)
	}
}

func TestBuildSearchRequestAppliesURLOptionsAndHeaders(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://www.deqixs.co"
	src.Header = `{"User-Agent":"Reader-Test"}`
	src.SearchURL = `/modules/article/search.php,{"method":"POST","body":"searchkey={{key}}&action=login","headers":{"X-Test":"1"}}`

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://www.deqixs.co/modules/article/search.php" || req.Method != "POST" || string(req.Body) != "searchkey=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&action=login" {
		t.Fatalf("req = %#v", req)
	}
	if req.Headers["User-Agent"] != "Reader-Test" || req.Headers["X-Test"] != "1" || req.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestAppliesCharsetToQueryAndBody(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://www.6mj.com"
	src.SearchURL = `https://www.6mj.com/Search.asp?Keyword={{key}}&Page={{page}},{"charset":"gb2312"}`

	req, err := buildSearchRequest(context.Background(), nil, src, "民间故事", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://www.6mj.com/Search.asp?Keyword=%C3%F1%BC%E4%B9%CA%CA%C2&Page=1" {
		t.Fatalf("url = %q", req.URL)
	}

	src.SearchURL = `/plus/search.php,{"charset":"gb2312","method":"POST","body":"kwtype=0&q={{key}}&searchtype=title"}`
	req, err = buildSearchRequest(context.Background(), nil, src, "民间故事", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest(body) error = %v", err)
	}
	if req.Method != "POST" {
		t.Fatalf("method = %q", req.Method)
	}
	if string(req.Body) != "kwtype=0&q=%C3%F1%BC%E4%B9%CA%CA%C2&searchtype=title" {
		t.Fatalf("body = %q", string(req.Body))
	}
}

func TestBuildSearchRequestSupportsJavaEncodeURICharsetArg(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://www.6mj.com"
	src.SearchURL = `@js:"https://www.6mj.com/Search.asp?Keyword="+java.encodeURI(key,"gb2312")+"&Page="+page`
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "民间故事", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://www.6mj.com/Search.asp?Keyword=%C3%F1%BC%E4%B9%CA%CA%C2&Page=1" {
		t.Fatalf("url = %q", req.URL)
	}
}

func TestBuildSearchRequestParsesLooseHeaderJSON(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "http://api.jmlldsc.com##@曦灵"
	src.SearchURL = `/search?keyword={{key}}`
	src.Header = `{'User-Agent':'okhttp/4.9.2','client-name':'app.maoyankanshu.novel'}`

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "http://api.jmlldsc.com/search?keyword="+url.QueryEscape("斗破苍穹") {
		t.Fatalf("url = %q", req.URL)
	}
	if req.Headers["User-Agent"] != "okhttp/4.9.2" || req.Headers["client-name"] != "app.maoyankanshu.novel" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestRejectsUnsupportedScript(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = `@js:"https://example.test/search?q=" + key`

	_, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if !errors.Is(err, ErrUnsupportedSearchURL) {
		t.Fatalf("error = %v, want ErrUnsupportedSearchURL", err)
	}
}

func TestBuildSearchRequestMarksWebViewHTTPFallback(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = `https://example.test/search,{"webView":true}`
	req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if !req.WebViewFallback {
		t.Fatalf("request = %#v", req)
	}
}

func TestBuildSearchRequestAcceptsWebJSURLOption(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = `https://example.test/search,{"webView":true,"webJs":"document.body.innerText"}`
	req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	// 对齐 Legado AnalyzeUrl.kt：webJs 只在 useWebView 分支执行——webView:true 时 webJs 生效（S07-09）。
	if !req.WebViewFallback || req.BodyJS != "document.body.innerText" {
		t.Fatalf("request = %#v", req)
	}
	// 未声明 webView 时 webJs 完全不生效，丢弃 BodyJS。
	noWebView := searchTestSource()
	noWebView.SearchURL = `https://example.test/search,{"webJs":"document.body.innerText"}`
	reqNoWebView, err := buildSearchRequest(context.Background(), nil, noWebView, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if reqNoWebView.WebViewFallback || reqNoWebView.BodyJS != "" {
		t.Fatalf("request without webView = %#v", reqNoWebView)
	}
	// webView:false 时 webJs 同样不生效。
	falseWebView := searchTestSource()
	falseWebView.SearchURL = `https://example.test/search,{"webView":false,"webJs":"document.body.innerText"}`
	reqFalse, err := buildSearchRequest(context.Background(), nil, falseWebView, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if reqFalse.WebViewFallback || reqFalse.BodyJS != "" {
		t.Fatalf("request with webView false = %#v", reqFalse)
	}
}

func TestBuildSearchRequestAppliesRetryOption(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = `https://example.test/search,{retry:2}`
	req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.Retry != 2 {
		t.Fatalf("req = %#v", req)
	}
}

func TestBuildSearchRequestEvaluatesURLScriptAndInlineExpressions(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://example.test/base"
	src.SearchURL = `@js:source.getKey() + "/search?q=" + java.encodeURI(key) + "&p=" + (page + 1)`
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破 苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://example.test/base/search?q=%E6%96%97%E7%A0%B4+%E8%8B%8D%E7%A9%B9&p=2" {
		t.Fatalf("url = %q", req.URL)
	}

	src.SearchURL = `/search?pn={{(page - 1) * 10}}&q={{java.encodeURI(key)}}`
	req, err = buildSearchRequest(context.Background(), runtime, src, "斗破 苍穹", 2)
	if err != nil {
		t.Fatalf("inline buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://example.test/search?pn=10&q=%E6%96%97%E7%A0%B4+%E8%8B%8D%E7%A9%B9" {
		t.Fatalf("inline url = %q", req.URL)
	}

	src.SearchURL = `{{cookie.removeCookie(source.key)}}https://example.test/search?q={{key}}`
	req, err = buildSearchRequest(context.Background(), runtime, src, "斗破", 1)
	if err != nil {
		t.Fatalf("nil inline buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://example.test/search?q="+url.QueryEscape("斗破") {
		t.Fatalf("nil inline url = %q", req.URL)
	}

	src.SearchURL = `https://api.example.test/search,{"method":"POST","body":{"keyword":{{key}},"page":{{page}},"page_size":20}}`
	req, err = buildSearchRequest(context.Background(), runtime, src, "斗破", 3)
	if err != nil {
		t.Fatalf("json body buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://api.example.test/search" || req.Method != "POST" {
		t.Fatalf("json body req = %#v", req)
	}
	if string(req.Body) != `{"keyword":"斗破","page":3,"page_size":20}` {
		t.Fatalf("json body = %q", string(req.Body))
	}
	if req.Headers["Content-Type"] != "application/json; charset=UTF-8" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestDefinesNullResultForInlineExpression(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = `https://example.test/search?q={{java.put("keyword", key);result}}`
	runtime := script.NewRuntime(script.Policy{SourceKey: src.BookSourceURL, Source: src})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗罗大陆", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://example.test/search?q=" {
		t.Fatalf("URL = %q", req.URL)
	}
}

func TestBuildSearchRequestEvaluatesEmbeddedTrailingURLScript(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://fanqie.example.test/＃妍希"
	src.SearchURL = "/search?query={{key}}&page={{(page-1)*10}}\n@js:if (key.match(/^\\d+$/) && !Number.isNaN(parseInt(key))) {\n  result = source.getKey() + \"detail?bookid=\" + key;\n}\nresult"
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破苍穹", 2)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://fanqie.example.test/search?query="+url.QueryEscape("斗破苍穹")+"&page=10" {
		t.Fatalf("url = %q", req.URL)
	}

	req, err = buildSearchRequest(context.Background(), runtime, src, "7268620849496722495", 1)
	if err != nil {
		t.Fatalf("numeric buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://fanqie.example.test/detail?bookid=7268620849496722495" {
		t.Fatalf("numeric url = %q", req.URL)
	}
}

func TestBuildSearchRequestEvaluatesHeaderScript(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://fanqie.example.test/＃妍希"
	src.SearchURL = `/search?query={{key}}`
	src.Header = `@js:JSON.stringify({"X-Sec-Token":"abc","X-Keyword":key,"X-Source":source.getKey()})`
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.Headers["X-Sec-Token"] != "abc" || req.Headers["X-Keyword"] != "斗破苍穹" || req.Headers["X-Source"] != "https://fanqie.example.test/" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestHandlesFanqieEmbeddedScriptAndHeader(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://fanqie.hnxianxin.cn/＃妍希"
	src.SearchURL = "/search?query={{key}}&page={{(page-1)*10}}&tab_type=3\n@js:if (key.match(/^\\d+$/) && !Number.isNaN(parseInt(key))) {\n  result = source.key + \"detail?bookid=\" + key;\n}\nresult"
	src.Header = "@js:\nJSON.stringify({\n\t\"x-sec-token\": \"8f79170487338570cdbb178e4653ed25943a6af05f3c29043782de49fc7a8fbd2446589412f746e396a05744cf546027bc63d7391b0e86ff3551c4d8e52ac1f97a2492bcc15dba6a9e2181084ed6e3f2a4d8a7e5aa419a705c5cffc8a93f5827fce376e3c3df14b8498405ba5f2630fa948894f4f7e5e26840f431f50396e9\"\n\t})"
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://fanqie.hnxianxin.cn/search?query="+url.QueryEscape("斗破苍穹")+"&page=0&tab_type=3" {
		t.Fatalf("url = %q", req.URL)
	}
	if req.Headers["x-sec-token"] == "" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestEncodesFormBodyFromURLScript(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://m.nauqmf.com"
	src.Header = `{"User-Agent":"Reader-Test"}`
	src.SearchURL = `@js: "/api/search," + JSON.stringify({
  "body": "q=" + key + "&ts=1",
  "headers": {
    "Accept": "application/json"
  },
  "method": "POST"
})`
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "https://m.nauqmf.com/api/search" || req.Method != "POST" {
		t.Fatalf("req = %#v", req)
	}
	if string(req.Body) != "q=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&ts=1" {
		t.Fatalf("body = %q", string(req.Body))
	}
	if req.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestDoesNotBreakJSTemplateStrings(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://n.novelia.cc"
	src.SearchURL = "<js>\n" +
		"var url;\n" +
		"if (!url) {\n" +
		"  url = `${baseUrl}/api/novel?page=${page-1}&pageSize=20&query=${key}`;\n" +
		"}\n" +
		"url;\n" +
		"</js>"
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	want := "https://n.novelia.cc/api/novel?page=0&pageSize=20&query=" + url.QueryEscape("斗破苍穹")
	if req.URL != want {
		t.Fatalf("url = %q, want %q", req.URL, want)
	}
}

func TestBuildSearchRequestMarksTypeRequestsForHexEncoding(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "http://download.yichnmedia.com"
	src.SearchURL = `data:;base64,L3NlYXJjaD9rZXl3b3JkPWJvb2s=,{"type":"maoyankanshu"}`

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.URL != "data:;base64,L3NlYXJjaD9rZXl3b3JkPWJvb2s=" || !req.EncodeBodyAsHex {
		t.Fatalf("req = %#v", req)
	}
}

func TestBuildSearchRequestDoesNotJSONQuoteInlineDataURIExpression(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "http://download.yichnmedia.com"
	src.SearchURL = `data:;base64,{{java.base64Encode("/search?keyword="+key+"&page="+page)}},{"type":"maoyankanshu"}`
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})

	req, err := buildSearchRequest(context.Background(), runtime, src, "斗破苍穹", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if strings.Contains(req.URL, `"`) {
		t.Fatalf("data uri unexpectedly quoted: %q", req.URL)
	}
	if !strings.Contains(req.URL, "data:;base64,") || !req.EncodeBodyAsHex {
		t.Fatalf("req = %#v", req)
	}
}

func TestSingleSourceSearchSupportsLowercaseAtCSSFieldRules(t *testing.T) {
	html := `<!DOCTYPE html><html><body><div class="col-lg-3 col-md-4 col-sm-3 col-xs-6">
<div class="card mb-30" title="斗破苍穹之足诀">
  <div class="card-body">
    <h5 class="card-title"><a href="/detail/1771755194.html">斗破苍穹之足诀</a></h5>
    <div class="card-ep mt-2"><a href="/forum/1771755194/493331.html">第十八章 厄难毒体</a></div>
    <div class="card-author"><a href="/tags/蒼藍/">蒼藍</a></div>
    <div class="column"><i class="icon-star-s"></i> 5 (2)</div>
    <div class="column"><i class="icon-file-text"></i>73,614</div>
    <div class="book-update">最新更新</div>
  </div>
</div>
</div></body></html>`
	src := searchTestSource()
	src.BookSourceURL = "https://www.esjzone.cc"
	src.SearchURL = "https://www.esjzone.one/tags/{{key}}"
	src.RuleSearch.Value.BookList = "div.col-lg-3.col-md-4.col-sm-3.col-xs-6"
	src.RuleSearch.Value.Name = "@css: .card-title a, .product-title@text\n@js:\njava.t2s(result)"
	src.RuleSearch.Value.Author = "class.card-author@tag.a@text\n@js:\njava.t2s(result)"
	src.RuleSearch.Value.BookURL = "@css:div> div > div > h5 > a, .product-title > a@href"
	src.RuleSearch.Value.Intro = "class.book-ep@div.-1@text"
	src.RuleSearch.Value.LastChapter = "@css:div>div>div[class=card-ep mt-2], .book-ep a@text"
	searcher := NewSingleSourceSearcher(fakeFetcher{resp: &fetch.Response{URL: src.SearchURL, StatusCode: 200, Body: []byte(html)}}, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	items, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破苍穹", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Name != "斗破苍穹之足诀" || items[0].Author != "苍蓝" || items[0].BookURL != "https://www.esjzone.one/detail/1771755194.html" {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestSingleSourceSearchLoginCheckJSSupportsResultBodyMethod(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = "https://example.test/search"
	src.LoginCheckJS = `if (result.body().match(/old/)) { result = result.body().replace("old", "new"); } result`
	searcher := NewSingleSourceSearcher(fakeFetcher{resp: &fetch.Response{URL: src.SearchURL, StatusCode: 200, Body: []byte(`<div class="book"><a class="name" href="/b/1">new</a></div>old`)}}, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	items, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "old", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != "new" {
		t.Fatalf("items = %#v", items)
	}
}

func TestSingleSourceSearchReversesBookListWhenRuleStartsWithMinus(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = "-.book"
	searcher := NewSingleSourceSearcher(fakeFetcher{
		resp: &fetch.Response{URL: src.SearchURL, StatusCode: 200, Body: []byte(`
<div class="book"><a class="name" href="/b/1">第一本</a><span class="author">甲</span></div>
<div class="book"><a class="name" href="/b/2">第二本</a><span class="author">乙</span></div>`)},
	}, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	items, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(items) != 2 || items[0].Name != "第二本" || items[1].Name != "第一本" {
		t.Fatalf("items = %#v", items)
	}
}

func TestSingleSourceSearchDoesNotLeakPutVariablesAcrossItems(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.BookList = ".book"
	src.RuleSearch.Value.Name = ".name@text@put:{bid:.bid@text}"
	src.RuleSearch.Value.Intro = "@get:{bid}"
	searcher := NewSingleSourceSearcher(fakeFetcher{
		resp: &fetch.Response{URL: src.SearchURL, StatusCode: 200, Body: []byte(`
<div class="book"><a class="name" href="/b/1">第一本</a><span class="author">甲</span><span class="bid">A1</span></div>
<div class="book"><a class="name" href="/b/2">第二本</a><span class="author">乙</span></div>`)},
	}, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	items, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Intro != "A1" {
		t.Fatalf("first item = %#v", items[0])
	}
	if items[1].Intro != "" {
		t.Fatalf("second item leaked vars = %#v", items[1])
	}
}

func TestBuildSearchRequestAcceptsLooseHeaderJSONFromJS(t *testing.T) {
	src := searchTestSource()
	src.Header = `@js:"{'X-Token':'abc'}"`
	req, err := buildSearchRequest(context.Background(), script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}), src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.Headers["X-Token"] != "abc" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestSingleSourceSearchPassesUseCookieJarToFetcher(t *testing.T) {
	src := searchTestSource()
	var lastReq fetch.Request
	searcher := NewSingleSourceSearcher(fakeFetcher{
		resp:    &fetch.Response{URL: src.SearchURL, StatusCode: 200, Body: []byte(`<div class="book"><a class="name" href="/b/1">书名</a></div>`)},
		lastReq: &lastReq,
	}, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	if _, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书名", Page: 1}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !lastReq.UseCookieJar {
		t.Fatalf("request = %#v", lastReq)
	}
}

type fakeFetcher struct {
	resp    *fetch.Response
	err     error
	lastURL *string
	lastReq *fetch.Request
}

func (f fakeFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if f.lastURL != nil {
		*f.lastURL = req.URL
	}
	if f.lastReq != nil {
		*f.lastReq = req
	}
	return f.resp, f.err
}

func searchTestSource() *source.BookSource {
	return &source.BookSource{
		BookSourceURL:    "https://example.test/source",
		BookSourceName:   "Example",
		BookSourceType:   0,
		SearchURL:        "https://example.test/search?q={key}&page={page}",
		Enabled:          true,
		EnabledExplore:   true,
		EnabledCookieJar: true,
		RuleSearch: rule.Flexible[rule.SearchRule]{Value: &rule.SearchRule{
			BookList:    ".book",
			Name:        ".name@text",
			Author:      ".author@text",
			BookURL:     ".name@href",
			Kind:        ".kind@text",
			LastChapter: ".last@text",
			Intro:       ".intro@text",
		}},
		RuleBookInfo: rule.Flexible[rule.BookInfoRule]{Value: &rule.BookInfoRule{
			Init:      ".detail",
			Name:      "h1@text",
			Author:    ".author@text",
			Intro:     ".intro@text",
			CoverURL:  ".cover@src",
			TocURL:    ".toc@href",
			CanReName: "1",
		}},
	}
}

func searchDetailHTML() string {
	return `<html><body><main class="detail"><h1>斗破苍穹</h1><span class="author">天蚕土豆</span><p class="intro">少年故事</p><img class="cover" src="/images/cover.jpg"/><a class="toc" href="/toc/1">目录</a></main></body></html>`
}

func TestSingleSourceSearchFormatsFieldsLikeLegado(t *testing.T) {
	src := searchTestSource()
	src.RuleSearch.Value.WordCount = ".wc@text"
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=斗破&page=1",
		Body: []byte(`<div class="book">
			<a class="name" href="/b/1">斗破苍穹 作者:天蚕土豆</a>
			<span class="author">作者:天蚕土豆</span>
			<span class="kind">玄幻</span>
			<span class="kind">奇幻</span>
			<span class="wc">12345</span>
			<div class="intro">少年<br/>故事</div>
		</div>`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %#v", books)
	}
	got := books[0]
	if got.Name != "斗破苍穹" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.Author != "天蚕土豆" {
		t.Fatalf("author = %q", got.Author)
	}
	if got.Kind != "玄幻,奇幻" {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.WordCount != "1.2万字" {
		t.Fatalf("wordCount = %q", got.WordCount)
	}
	if !strings.Contains(got.Intro, "少年") || strings.Contains(got.Intro, "<br") {
		t.Fatalf("intro = %q", got.Intro)
	}
}

func TestSingleSourceSearchRespectsLimiterGlobalLimit(t *testing.T) {
	src := searchTestSource()
	fetcher := &blockingFetcher{
		resp: &fetch.Response{
			StatusCode: 200,
			URL:        "https://example.test/search?q=斗破&page=1",
			Body:       []byte(`<div class="book"><a class="name" href="/b/1">斗破苍穹</a><span class="author">天蚕土豆</span></div>`),
		},
		barrier: make(chan struct{}),
		once:    &sync.Once{},
	}
	searcher := NewSingleSourceSearcher(fetcher, nil).WithLimiter(ratelimit.New(ratelimit.Options{Global: 1}))

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1}); err != nil {
				t.Errorf("Search() error = %v", err)
			}
		}()
	}
	// 两个请求都到达 barrier（第二个会因限流被阻塞在 Acquire，单次放开即可观察串行化）。
	deadline := time.After(5 * time.Second)
	for fetcher.maxConcurrent() < 1 {
		select {
		case <-deadline:
			t.Fatal("no fetch reached barrier")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(fetcher.barrier)
	wg.Wait()
	if max := fetcher.maxConcurrent(); max > 1 {
		t.Fatalf("max concurrent fetches = %d, want <= 1", max)
	}
}

// blockingFetcher 记录同时进行的抓取数，用于验证限流器确实生效。
// Fetch 用 barrier 让并发爬升与调度无关：先到 barrier 再返回（S17-06，避免固定 sleep flaky）。
type blockingFetcher struct {
	resp *fetch.Response

	barrier chan struct{}
	once    *sync.Once

	mu      sync.Mutex
	running int
	max     int
}

func (f *blockingFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.mu.Lock()
	f.running++
	if f.running > f.max {
		f.max = f.running
	}
	f.mu.Unlock()
	<-f.barrier
	f.mu.Lock()
	f.running--
	f.mu.Unlock()
	return f.resp, nil
}

func (f *blockingFetcher) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.max
}

func TestBuildSearchRequestReplacesPageListPlaceholder(t *testing.T) {
	tests := []struct {
		page int
		want string
	}{
		{1, "https://example.test/list/1/index.html?p=4"},
		{2, "https://example.test/list/2/index.html?p=5"},
		{9, "https://example.test/list/3/index.html?p=5"},
	}
	for _, tt := range tests {
		src := searchTestSource()
		src.SearchURL = "https://example.test/list/<1,2,3>/index.html?p=<4,5>"

		req, err := buildSearchRequest(context.Background(), nil, src, "斗破", tt.page)
		if err != nil {
			t.Fatalf("buildSearchRequest(page=%d) error = %v", tt.page, err)
		}
		if req.URL != tt.want {
			t.Fatalf("page=%d url = %q, want %q", tt.page, req.URL, tt.want)
		}
	}
}

func TestBuildSearchRequestAppliesOriginOption(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://www.example.test"
	src.SearchURL = `/search,{"method":"POST","origin":"https://origin.example.test"}`

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if req.Headers["Origin"] != "https://origin.example.test" || req.Headers["Referer"] != "https://origin.example.test" {
		t.Fatalf("headers = %#v", req.Headers)
	}

	src.SearchURL = `/search,{"method":"POST","origin":"https://origin.example.test","headers":{"Referer":"https://ref.example.test"}}`
	req, err = buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest(referer) error = %v", err)
	}
	if req.Headers["Referer"] != "https://ref.example.test" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestKeepsBodyWithExplicitContentType(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://api.example.test"
	src.SearchURL = `/search,{"method":"POST","body":"a=1&b=斗破","headers":{"Content-Type":"text/plain"}}`

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	// 对齐 Legado AnalyzeUrl：显式 Content-Type 时保留该头，body 由 Build 统一按 charset 选项
	// 表单编码（S09-05）；无 charset 选项时按 URL percent 编码（urlencode 默认）。
	if string(req.Body) != "a=1&b=%E6%96%97%E7%A0%B4" {
		t.Fatalf("body = %q, want form-encoded body", string(req.Body))
	}
	if req.Headers["Content-Type"] != "text/plain" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestEncodesBodyOnceWithoutContentType(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://api.example.test"
	src.SearchURL = `/search,{"method":"POST","body":"a=1&b=斗破"}`

	req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("buildSearchRequest() error = %v", err)
	}
	if string(req.Body) != "a=1&b=%E6%96%97%E7%A0%B4" {
		t.Fatalf("body = %q", string(req.Body))
	}
	if req.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

func TestBuildSearchRequestHexEncodingForTypeKeyPresence(t *testing.T) {
	tests := []struct {
		option string
		want   bool
	}{
		{`{"type":""}`, false},
		{`{"type":null}`, false},
		{`{"type":"   "}`, false},
		{`{"type":123}`, true},
		{`{"type":"x"}`, true},
	}
	for _, tt := range tests {
		src := searchTestSource()
		src.BookSourceURL = "http://download.example.test"
		src.SearchURL = "data:;base64,L3NlYXJjaA==," + tt.option

		req, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
		if err != nil {
			t.Fatalf("buildSearchRequest(%s) error = %v", tt.option, err)
		}
		if req.EncodeBodyAsHex != tt.want {
			t.Fatalf("option %s: EncodeBodyAsHex = %v, want %v", tt.option, req.EncodeBodyAsHex, tt.want)
		}
	}
}

// TestSingleSourceSearchBookURLPatternNoStubOnParseFailure 验证 bookUrlPattern 命中但解析失败
// （无 ruleBookInfo）或 name 为空时返回空结果而非空名 stub 条目（S02-01，对齐 Legado BookList.kt）。
func TestSingleSourceSearchBookURLPatternNoStubOnParseFailure(t *testing.T) {
	src := searchTestSource()
	src.BookURLPattern = `https://example\.test/book/\d+`
	// 无 ruleBookInfo 时 PreviewSearchBookFromInfo 返回 ErrBookInfoRuleMissing，应返回空结果。
	src.RuleBookInfo = rule.Flexible[rule.BookInfoRule]{}
	fetcher := fakeFetcher{resp: &fetch.Response{StatusCode: 200, URL: "https://example.test/book/1", Body: []byte("<html></html>")}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "斗破", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("books = %#v, want empty (no nameless stub)", books)
	}
}

// TestSingleSourceSearchDedupesByWholeObject 验证单源去重按 bookUrl+name+author 对象级去重（S02-09），
// bookUrl 相同但 name 不同的条目保留，与 Legado LinkedHashSet 语义一致。
func TestSingleSourceSearchDedupesByWholeObject(t *testing.T) {
	src := searchTestSource()
	fetcher := fakeFetcher{resp: &fetch.Response{
		StatusCode: 200,
		URL:        "https://example.test/search?q=书&page=1",
		Body: []byte(`<div class="book"><a class="name" href="/b/1">书一</a></div>
			<div class="book"><a class="name" href="/b/1">书二</a></div>
			<div class="book"><a class="name" href="/b/1">书一</a></div>`),
	}}
	searcher := NewSingleSourceSearcher(fetcher, nil)

	books, err := searcher.Search(context.Background(), SearchInput{Source: src, Keyword: "书", Page: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("books = %#v, want 2 (same bookUrl+name deduped, different names kept)", books)
	}
	if books[0].Name != "书一" || books[1].Name != "书二" {
		t.Fatalf("books = %#v", books)
	}
}

// TestRenderSearchURLTrimsTrailingSlashConsistently 验证 {{source.key}} 与 {{source.bookSourceUrl}}
// 尾斜杠处理一致（S02-11），书源 URL 以 / 结尾时不拼出双斜杠。
func TestRenderSearchURLTrimsTrailingSlashConsistently(t *testing.T) {
	src := searchTestSource()
	src.BookSourceURL = "https://example.test/base/"
	src.SearchURL = "{{source.key}}/search?q={{key}}&page={{page}}"
	withKey, err := renderSearchURL(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("renderSearchURL(key) error = %v", err)
	}
	src.SearchURL = "{{source.bookSourceUrl}}/search?q={{key}}&page={{page}}"
	withURL, err := renderSearchURL(context.Background(), nil, src, "斗破", 1)
	if err != nil {
		t.Fatalf("renderSearchURL(bookSourceUrl) error = %v", err)
	}
	if withKey != withURL {
		t.Fatalf("inconsistent base: key=%q bookSourceUrl=%q", withKey, withURL)
	}
	if strings.Contains(withKey, "//search") {
		t.Fatalf("double slash in %q", withKey)
	}
}

// TestBuildSearchRequestKeepsNonOptionBuildErrors 验证非 URL 选项类 Build 错误不再挂
// ErrUnsupportedSearchURL（S02-06），错误分类不失真。
func TestBuildSearchRequestKeepsNonOptionBuildErrors(t *testing.T) {
	src := searchTestSource()
	// searchUrl 带不支持的 URL 选项仍报 ErrUnsupportedSearchURL。
	src.SearchURL = `https://example.test,{"foo":1}`
	_, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if !errors.Is(err, ErrUnsupportedSearchURL) {
		t.Fatalf("unsupported option error = %v, want ErrUnsupportedSearchURL", err)
	}
	// 普通 URL（无脚本无选项）不报 ErrUnsupportedSearchURL。
	src.SearchURL = "https://example.test/search?q={{key}}"
	if _, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1); err != nil {
		t.Fatalf("plain url error = %v, want nil", err)
	}
}
