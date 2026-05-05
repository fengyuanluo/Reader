package bookapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
)

func TestInfoParserParsesMockDetailPageAndSavesBook(t *testing.T) {
	src := testInfoSource()
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: detailHTML(), url: "https://site.example.test/book/1"}
	service := NewInfoService(repo, fetcher, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    "https://site.example.test/book/1",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.Name != "斗破苍穹" || got.Author != "天蚕土豆" || got.Kind != "玄幻,热血" || got.WordCount != "500万字" {
		t.Fatalf("book core fields = %#v", got)
	}
	if got.LatestChapterTitle != "第一千章" || got.Intro != "少年故事" {
		t.Fatalf("book text fields = %#v", got)
	}
	if got.CoverURL != "https://site.example.test/images/cover.jpg" {
		t.Fatalf("cover = %q", got.CoverURL)
	}
	if got.TocURL != "https://site.example.test/toc/1" {
		t.Fatalf("toc = %q", got.TocURL)
	}
	if repo.saved == nil || repo.saved.BookURL != got.BookURL {
		t.Fatalf("saved = %#v", repo.saved)
	}
}

func TestInfoParserUsesInfoHTMLAndInitRule(t *testing.T) {
	src := testInfoSource()
	repo := &fakeBookRepo{}
	service := NewInfoService(repo, &fakeInfoFetcher{}, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source:   src,
		InfoHTML: `<html><body><div class="noise"><h1>错误标题</h1></div><main class="detail">` + detailInnerHTML() + `</main></body></html>`,
		Book: domainbook.Book{
			BookURL:    "https://site.example.test/book/1",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.Name != "斗破苍穹" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestInfoParserFallsBackToBookURLWhenTocRuleMissing(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.TocURL = ""
	repo := &fakeBookRepo{}
	service := NewInfoService(repo, &fakeInfoFetcher{body: detailHTML(), url: "https://site.example.test/book/1"}, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    "https://site.example.test/book/1",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.TocURL != got.BookURL {
		t.Fatalf("toc = %q, want book URL %q", got.TocURL, got.BookURL)
	}
	if got.TocHTML == "" {
		t.Fatalf("tocHtml is empty")
	}
}

func TestInfoParserUsesFirstNonEmptyTocAndCoverURLValue(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.CoverURL = "$.covers[*]"
	src.RuleBookInfo.Value.TocURL = "$.tocs[*]"
	repo := &fakeBookRepo{}
	service := NewInfoService(repo, &fakeInfoFetcher{}, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source:   src,
		InfoHTML: `{"covers":["/images/cover-1.jpg","/images/cover-2.jpg"],"tocs":["/toc/1","/toc/backup"]}`,
		Book: domainbook.Book{
			BookURL:    "https://site.example.test/book/1",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.CoverURL != "https://site.example.test/images/cover-1.jpg" || got.TocURL != "https://site.example.test/toc/1" {
		t.Fatalf("book = %#v", got)
	}
}

func TestInfoParserExtractsDownloadURLsForFileSource(t *testing.T) {
	src := testInfoSource()
	src.BookSourceType = 3
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	src.RuleBookInfo.Value.DownloadURLs = "$.downloads[*]"
	repo := &fakeBookRepo{}
	service := NewInfoService(repo, &fakeInfoFetcher{}, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source:   src,
		InfoHTML: `{"name":"文件书","author":"作者","downloads":["/a.epub","https://cdn.example.test/b.epub"]}`,
		Book: domainbook.Book{
			BookURL:    "https://site.example.test/book/file",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if len(got.DownloadURLs) != 2 || got.DownloadURLs[0] != "https://site.example.test/a.epub" || got.DownloadURLs[1] != "https://cdn.example.test/b.epub" {
		t.Fatalf("download urls = %#v", got.DownloadURLs)
	}
}

func TestInfoParserExtractsUTF8TextSelectorTocURLFromRealFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "test", "fixtures", "html", "80zw_426.html")
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	src := &source.BookSource{
		SourceID:       "src_9e9340fd762b536ea8e2bf42",
		BookSourceURL:  "http://wap.80zw.la/",
		BookSourceName: "八零小说（优++）",
		BookSourceType: 0,
		RuleBookInfo: rule.Flexible[rule.BookInfoRule]{Value: &rule.BookInfoRule{
			TocURL: "text.查看更多章节@href",
		}},
	}
	repo := &fakeBookRepo{}
	service := NewInfoService(repo, &fakeInfoFetcher{}, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source:   src,
		InfoHTML: string(body),
		Book: domainbook.Book{
			BookURL:    "http://wap.80zw.la/426/",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.TocURL != "http://wap.80zw.la/426/page-1.html" {
		t.Fatalf("toc = %q, want http://wap.80zw.la/426/page-1.html", got.TocURL)
	}
}

func TestInfoServiceAppliesBookURLOptions(t *testing.T) {
	src := testInfoSource()
	src.Header = `{'User-Agent':'Reader-Test'}`
	src.EnabledCookieJar = true
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	src.RuleBookInfo.Value.TocURL = "$.toc"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"斗破苍穹","author":"天蚕土豆","toc":"https://site.example.test/toc/1"}`, url: "https://site.example.test/api/detail"}
	service := NewInfoService(repo, fetcher, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail,{method:'POST',body:{bookId:'1'}}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.Name != "斗破苍穹" || fetcher.lastReq.Method != "POST" || string(fetcher.lastReq.Body) != `{"bookId":"1"}` || fetcher.lastReq.Headers["User-Agent"] != "Reader-Test" {
		t.Fatalf("book=%#v req=%#v", got, fetcher.lastReq)
	}
	if !fetcher.lastReq.UseCookieJar {
		t.Fatalf("req=%#v", fetcher.lastReq)
	}
}

func TestInfoServiceAppliesFormEncodedBookBody(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"斗破苍穹","author":"天蚕土豆"}`, url: "https://site.example.test/api/detail"}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail,{method:'POST',body:'trace=1&bookId=218440'}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.Headers["Content-Type"] != "application/x-www-form-urlencoded" || string(fetcher.lastReq.Body) != "trace=1&bookId=218440" {
		t.Fatalf("req=%#v", fetcher.lastReq)
	}
}

func TestInfoServiceAppliesRetryOption(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"斗破苍穹","author":"天蚕土豆"}`, url: "https://site.example.test/api/detail"}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail,{retry:2}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.Retry != 2 {
		t.Fatalf("req=%#v", fetcher.lastReq)
	}
}

func TestInfoServiceEncodesQueryByDefault(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"论语","author":"孔子弟子"}`, url: "https://site.example.test/api/detail?keyword=%E8%AE%BA%E8%AF%AD"}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail?keyword=论语`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.URL != "https://site.example.test/api/detail?keyword=%E8%AE%BA%E8%AF%AD" {
		t.Fatalf("url = %q", fetcher.lastReq.URL)
	}
}

func TestInfoServiceAppliesHeaderJS(t *testing.T) {
	src := testInfoSource()
	src.Header = `@js:JSON.stringify({"X-Token":"abc"})`
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"论语","author":"孔子弟子"}`, url: "https://site.example.test/api/detail"}
	service := NewInfoService(repo, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src}))

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.Headers["X-Token"] != "abc" {
		t.Fatalf("headers = %#v", fetcher.lastReq.Headers)
	}
}

func TestInfoServiceExpandsBookURLVariablesBeforeFetch(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"斗破苍穹","author":"天蚕土豆"}`, url: "https://site.example.test/api/detail?book_id=7529358240547621950"}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail?book_id=@get:{book_id}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
			Variable:   `{"book_id":"7529358240547621950"}`,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.URL != "https://site.example.test/api/detail?book_id=7529358240547621950" {
		t.Fatalf("url = %q", fetcher.lastReq.URL)
	}
}

func TestInfoServiceAppliesOriginOptionAsHeaders(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"斗破苍穹","author":"天蚕土豆"}`, url: "https://site.example.test/api/detail"}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail,{method:'POST',origin:'https://site.example.test',body:'trace=1&bookId=218440'}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.Headers["Origin"] != "https://site.example.test" || fetcher.lastReq.Headers["Referer"] != "https://site.example.test" {
		t.Fatalf("req=%#v", fetcher.lastReq)
	}
}

func TestInfoServiceRejectsUnsupportedURLOption(t *testing.T) {
	src := testInfoSource()
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://site.example.test/api/detail,{"webView":true}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported url option") {
		t.Fatalf("err = %v", err)
	}
}

func TestInfoServiceAppliesRealMiduSearchResultBookURLAsPOST(t *testing.T) {
	src := testInfoSource()
	src.Header = `{"User-Agent":"okhttp/3.12.1"}`
	src.RuleBookInfo.Value.Init = ""
	src.RuleBookInfo.Value.Name = "$.name"
	src.RuleBookInfo.Value.Author = "$.author"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"name":"斗破苍穹","author":"天蚕土豆"}`, url: "https://api.midureader.com/fiction/book/getDetail"}
	service := NewInfoService(repo, fetcher, nil)

	_, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    `https://api.midureader.com/fiction/book/getDetail?platId=2&appId=com.yueyou.adreader&channelId=pc&appVersion=3.6.4&srcChannelId=pc&time=1777858303&_s=C66A42978B576873D32F0EACEBBF044D&_p=htmFNli%2Bnnlpn%2Bk%2FgzU86LNduiGvDrJxFreuHvzy3HrwWfUtjX%2FeDtEHBg9QC7R7pObDHiTkTkiT%2BYJoHzM8mJbq2TcXL0zhdfzBt0flK2fD6u002cjtg9wroFzH4zusYkrbS37pP6sLTnhvH8i2saJECg2ROdfULMW3gToYAujMfzD7jrdrGohVJVDDOKhm5gbz9UUMQWSNLXK0STn4UQEJ2PP8xcVb6sKQSt4Ergm6KBXxnSDajY6ZqFfvhWXPrr%2FZkYEjGDx07OxpJeMKOhLfaDcyQDrbUBH2X%2BR4m397Vn0cGGhGZ7k%2BOUklXYbalkWOkeL2ZnA%3D,{"body":"trace=33_33-10-1x0_40_40-4-11x229221%253Ftype%253Dbook%2526sortValue%253D%2526pos%253D2&bookId=229221","method":"POST"}`,
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if fetcher.lastReq.Method != "POST" {
		t.Fatalf("method = %q", fetcher.lastReq.Method)
	}
	if string(fetcher.lastReq.Body) != "trace=33_33-10-1x0_40_40-4-11x229221%253Ftype%253Dbook%2526sortValue%253D%2526pos%253D2&bookId=229221" {
		t.Fatalf("body = %q", string(fetcher.lastReq.Body))
	}
}

func TestInfoParserNormalizesTemplatedRelativeURLs(t *testing.T) {
	src := testInfoSource()
	src.RuleBookInfo.Value.Init = "$.data"
	src.RuleBookInfo.Value.Name = "$.novelName"
	src.RuleBookInfo.Value.CoverURL = "/covers/{{$.novelId}}.jpg"
	src.RuleBookInfo.Value.TocURL = "/novel/{{$.novelId}}/chapters"
	repo := &fakeBookRepo{}
	fetcher := &fakeInfoFetcher{body: `{"data":{"novelId":"zbqYrb","novelName":"斗破苍穹"}}`, url: "http://api.lfdapengu.com/novel/zbqYrb"}
	service := NewInfoService(repo, fetcher, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    "http://api.lfdapengu.com/novel/zbqYrb",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
		},
		CanRename: true,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.CoverURL != "http://api.lfdapengu.com/covers/zbqYrb.jpg" || got.TocURL != "http://api.lfdapengu.com/novel/zbqYrb/chapters" {
		t.Fatalf("book = %#v", got)
	}
}

func TestInfoParserHonorsCanRename(t *testing.T) {
	src := testInfoSource()
	repo := &fakeBookRepo{}
	service := NewInfoService(repo, &fakeInfoFetcher{body: detailHTML(), url: "https://site.example.test/book/1"}, nil)

	got, err := service.RefreshInfo(context.Background(), InfoInput{
		Source: src,
		Book: domainbook.Book{
			BookURL:    "https://site.example.test/book/1",
			Origin:     src.BookSourceURL,
			OriginName: src.BookSourceName,
			Name:       "旧书名",
			Author:     "旧作者",
		},
		CanRename: false,
	})
	if err != nil {
		t.Fatalf("RefreshInfo() error = %v", err)
	}
	if got.Name != "旧书名" || got.Author != "旧作者" {
		t.Fatalf("book = %#v", got)
	}
}

func testInfoSource() *source.BookSource {
	return &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://site.example.test/source",
		BookSourceName: "Source",
		BookSourceType: 0,
		RuleBookInfo: rule.Flexible[rule.BookInfoRule]{Value: &rule.BookInfoRule{
			Init:        ".detail",
			Name:        "h1@text",
			Author:      ".author@text",
			Kind:        ".kind a@text",
			WordCount:   ".word@text",
			LastChapter: ".latest@text",
			Intro:       ".intro@text",
			CoverURL:    ".cover@src",
			TocURL:      ".toc@href",
			CanReName:   "1",
		}},
	}
}

func detailHTML() string {
	return `<html><body><main class="detail">` + detailInnerHTML() + `</main></body></html>`
}

func detailInnerHTML() string {
	return `<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><div class="kind"><a>玄幻</a><a>热血</a></div><span class="word">500万字</span><span class="latest">第一千章</span><p class="intro">少年故事</p><img class="cover" src="/images/cover.jpg"/><a class="toc" href="/toc/1">目录</a>`
}

type fakeInfoFetcher struct {
	body    string
	url     string
	lastReq fetch.Request
}

func (f *fakeInfoFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.lastReq = req
	if f.url == "" {
		f.url = req.URL
	}
	return &fetch.Response{URL: f.url, Body: []byte(f.body)}, nil
}

type fakeBookRepo struct {
	saved *domainbook.Book
}

func (r *fakeBookRepo) Save(ctx context.Context, b *domainbook.Book) error {
	copy := *b
	r.saved = &copy
	return nil
}
func (r *fakeBookRepo) FindByID(ctx context.Context, id string) (*domainbook.Book, error) {
	return nil, nil
}
func (r *fakeBookRepo) FindByURL(ctx context.Context, bookURL string) (*domainbook.Book, error) {
	return nil, nil
}
func (r *fakeBookRepo) List(ctx context.Context, filter domainbook.BookFilter) ([]*domainbook.Book, int, error) {
	return nil, 0, nil
}
func (r *fakeBookRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *fakeBookRepo) SaveProgress(ctx context.Context, id string, progress domainbook.BookProgress) error {
	return nil
}
