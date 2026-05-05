package searchapp

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
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
	src.BookURLPattern = `book/\d+`
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

func TestBuildSearchRequestRejectsUnsupportedURLOption(t *testing.T) {
	src := searchTestSource()
	src.SearchURL = `https://example.test/search,{"webView":true}`
	_, err := buildSearchRequest(context.Background(), nil, src, "斗破", 1)
	if !errors.Is(err, ErrUnsupportedSearchURL) {
		t.Fatalf("error = %v, want ErrUnsupportedSearchURL", err)
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
	if req.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers = %#v", req.Headers)
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
