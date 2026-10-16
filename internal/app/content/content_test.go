package contentapp

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	replaceapp "reader/internal/app/replace"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/replace"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
)

func TestContentServiceParsesSinglePageAndCaches(t *testing.T) {
	src := testContentSource()
	b := testContentBook()
	chapter := testContentChapter()
	chapters := &fakeChapterRepo{}
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<html><body><h1>正文标题</h1><div class="content"><p>第一段</p><p>广告</p></div></body></html>`)},
	}}
	service := NewService(chapters, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: b, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Chapter.Title != "正文标题" {
		t.Fatalf("title = %q", got.Chapter.Title)
	}
	if got.Content != "第一段" {
		t.Fatalf("content = %q", got.Content)
	}
	if chapters.savedContent != "第一段" || chapters.savedIndex != 0 {
		t.Fatalf("saved content = %q index=%d", chapters.savedContent, chapters.savedIndex)
	}
}

func TestContentServiceUsesCacheBeforeFetching(t *testing.T) {
	src := testContentSource()
	chapters := &fakeChapterRepo{cached: "cached content", cachedOK: true}
	fetcher := &fakeContentFetcher{}
	service := NewService(chapters, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: testContentChapter()})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "cached content" {
		t.Fatalf("content = %q", got.Content)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d", fetcher.calls)
	}
}

// Legado WebBook.getContentAwait: 卷名章节（url 为 toc 阶段合成的 title+index 占位串）不抓取、不解析正文规则。
func TestContentServiceSkipsFetchForVolumeChapterWithURLStartingWithTitle(t *testing.T) {
	src := testContentSource()
	chapter := book.BookChapter{
		URL:     "📖[卷一 高手乍成]📖0",
		Title:   "📖[卷一 高手乍成]📖",
		BaseURL: "https://site.example.test/toc/1",
		BookURL: "https://site.example.test/book/1",
		Index:   0,
		Tag:     "更新时间：2019-10-21",
	}
	chapter.IsVolume = true
	chapters := &fakeChapterRepo{}
	fetcher := &fakeContentFetcher{}
	service := NewService(chapters, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "更新时间：2019-10-21" {
		t.Fatalf("content = %q, want tag text", got.Content)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0 for volume chapter", fetcher.calls)
	}
	if chapters.savedContent != "" {
		t.Fatalf("volume content should not be cached, got %q", chapters.savedContent)
	}
}

func TestContentServiceVolumeChapterWithoutTagReturnsEmptyContent(t *testing.T) {
	src := testContentSource()
	chapter := testContentChapter()
	chapter.IsVolume = true
	chapter.URL = chapter.Title + "0"
	chapter.Tag = ""
	service := NewService(&fakeChapterRepo{}, &fakeContentFetcher{}, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "" {
		t.Fatalf("content = %q, want empty", got.Content)
	}
}

func TestContentServiceStillFetchesVolumeChapterWithRealURL(t *testing.T) {
	src := testContentSource()
	chapter := book.BookChapter{
		URL:     "https://site.example.test/volume/1",
		Title:   "卷一",
		BaseURL: "https://site.example.test/toc/1",
		BookURL: "https://site.example.test/book/1",
		Index:   0,
	}
	chapter.IsVolume = true
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<html><body><h1>卷一</h1><div class="content"><p>卷首语</p></div></body></html>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "卷首语" {
		t.Fatalf("content = %q, want parsed volume page", got.Content)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
}

func TestContentServiceUsesBookTocHTMLBeforeFetching(t *testing.T) {
	src := testContentSource()
	chapter := testContentChapter()
	chapter.URL = "https://site.example.test/book/1"
	bookValue := testContentBook()
	bookValue.TocHTML = `<html><body><h1>缓存正文标题</h1><div class="content">缓存正文</div></body></html>`
	fetcher := &fakeContentFetcher{}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: bookValue, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "缓存正文" || got.Chapter.Title != "缓存正文标题" {
		t.Fatalf("result = %#v", got)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d", fetcher.calls)
	}
}

func TestContentServiceAppliesNetworkURLOptions(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		option    string
		wantProxy string
		wantDNSIP string
		wantErr   string
	}{
		{name: "proxy option", option: `{"proxy":"http://127.0.0.1:8888"}`, wantProxy: "http://127.0.0.1:8888"},
		{name: "dnsIp option", option: `{"dnsIp":"93.184.216.34"}`, wantDNSIP: "93.184.216.34"},
		{name: "source header proxy", header: `{"proxy":"socks5://proxy.test:1080@u@p","X-Test":"1"}`, option: `{}`, wantProxy: "socks5://proxy.test:1080@u@p"},
		{name: "serverID and webView delay ignored", option: `{"serverID":"1","webViewDelayTime":"500","webJs":"1"}`},
		{name: "webView unsupported", option: `{"webView":true}`, wantErr: "unsupported url option"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := testContentSource()
			src.Header = tt.header
			chapter := testContentChapter()
			chapter.URL = `https://site.example.test/api/content,` + tt.option
			fetcher := &fakeContentFetcher{}
			service := NewService(&fakeChapterRepo{}, fetcher, nil)

			_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if fetcher.calls == 0 {
				t.Fatalf("fetch not called, err = %v", err)
			}
			req := fetcher.lastReq
			if req.Proxy != tt.wantProxy || req.DNSIP != tt.wantDNSIP {
				t.Fatalf("Proxy/DNSIP = %q/%q, want %q/%q", req.Proxy, req.DNSIP, tt.wantProxy, tt.wantDNSIP)
			}
			if _, ok := req.Headers["proxy"]; ok {
				t.Fatalf("proxy leaked into headers: %#v", req.Headers)
			}
		})
	}
}

func TestContentServiceRejectsSourceRegexBrowserFlow(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.SourceRegex = `https://cdn.example.test/content/.*`
	service := NewService(&fakeChapterRepo{}, &fakeContentFetcher{}, nil)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: testContentChapter()})
	if !errors.Is(err, ErrUnsupportedContentBrowserFlow) {
		t.Fatalf("err = %v", err)
	}
}

// 缓存保存净化前正文（对齐 Legado BookContent.kt:150-153 needSave 的时机），用户替换规则在读取时应用，
// 这样用户事后改规则对已抓章节立即生效。
func TestContentServiceCachesRawContentAndAppliesProcessorOnRead(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.ReplaceRegex = ""
	b := testContentBook()
	chapter := testContentChapter()
	chapters := &fakeChapterRepo{}
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<div class="content">第一章
广告
正文</div>`)},
	}}
	processor := replaceapp.NewProcessor(&fakeContentReplaceRepo{
		content: []*replace.ReplaceRule{{ID: 10, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: true, ScopeContent: true}},
	})
	service := NewService(chapters, fetcher, nil).WithProcessor(processor)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: b, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "　　正文" {
		t.Fatalf("content = %q", got.Content)
	}
	if chapters.savedContent != "第一章\n广告\n正文" {
		t.Fatalf("saved content = %q, want raw content without user replace rules", chapters.savedContent)
	}
}

// 缓存命中时同样要应用用户替换规则，否则用户新增规则对已抓章节无效（R-045）。
func TestContentServiceAppliesProcessorToCachedContent(t *testing.T) {
	src := testContentSource()
	b := testContentBook()
	chapter := testContentChapter()
	chapters := &fakeChapterRepo{cached: "第一章\n广告\n正文", cachedOK: true}
	fetcher := &fakeContentFetcher{}
	processor := replaceapp.NewProcessor(&fakeContentReplaceRepo{
		content: []*replace.ReplaceRule{{ID: 10, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: true, ScopeContent: true}},
	})
	service := NewService(chapters, fetcher, nil).WithProcessor(processor)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: b, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "　　正文" {
		t.Fatalf("content = %q", got.Content)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d", fetcher.calls)
	}
}

func TestContentServiceMergesNextContentPagesInOrder(t *testing.T) {
	src := testContentSource()
	b := testContentBook()
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL:                       {URL: chapter.URL, Body: []byte(`<div class="content">第一页</div><a class="next" href="/c/1-2">next</a>`)},
		"https://site.example.test/c/1-2": {URL: "https://site.example.test/c/1-2", Body: []byte(`<div class="content">第二页</div>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: b, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一页\n第二页" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceParsesBareJSONContentRule(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "data[0].content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`{"data":[{"content":"第一段"}]}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一段" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceAppliesChapterURLOptions(t *testing.T) {
	src := testContentSource()
	src.Header = `{'User-Agent':'Reader-Test'}`
	src.EnabledCookieJar = true
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content,{"method":"POST","body":{"chapter_ids":"1","novelId":7}}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一段" {
		t.Fatalf("content = %q", got.Content)
	}
	if fetcher.lastReq.Method != "POST" || string(fetcher.lastReq.Body) != `{"chapter_ids":"1","novelId":7}` || fetcher.lastReq.Headers["Content-Type"] != "application/json" || fetcher.lastReq.Headers["User-Agent"] != "Reader-Test" {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
	if !fetcher.lastReq.UseCookieJar {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
}

func TestContentServiceAppliesRetryOption(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content,{retry:3}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if fetcher.lastReq.Retry != 3 {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
}

func TestContentServiceAppliesLoginCheckJS(t *testing.T) {
	src := testContentSource()
	src.LoginCheckJS = `result.replace("old", "")`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/c/1": {URL: "https://site.example.test/c/1", Body: []byte(`old<html><body><h1>第一章</h1><div class="content">正文</div></body></html>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: testContentChapter()})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "正文" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceAppliesContentWebJSAfterLoginCheck(t *testing.T) {
	src := testContentSource()
	src.LoginCheckJS = `result.replace("old", "")`
	src.RuleContent.Value.Content = ".content@text"
	src.RuleContent.Value.WebJS = `result.replace("mid", "")`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/c/1": {URL: "https://site.example.test/c/1", Body: []byte(`old<html><body><div class="content">mid正文</div></body></html>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: testContentChapter()})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "正文" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceAppliesContentWebJSToEachPage(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Content = ".content@text"
	src.RuleContent.Value.WebJS = `result.replace("mid", "")`
	b := testContentBook()
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL:                       {URL: chapter.URL, Body: []byte(`<div class="content">mid第一页</div><a class="next" href="/c/1-2">next</a>`)},
		"https://site.example.test/c/1-2": {URL: "https://site.example.test/c/1-2", Body: []byte(`<div class="content">mid第二页</div>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: b, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一页\n第二页" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServicePreservesBookVariablesWrittenByWebJS(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = `{{java.get('shared')}}`
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	src.RuleContent.Value.WebJS = `book.putVariable("shared", "共享正文"); result`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/c/1": {URL: "https://site.example.test/c/1", Body: []byte(`<div class="content">ignored</div>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: testContentChapter()})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "共享正文" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceReadsChapterVariablesBeforeBookVariables(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = `{{java.get('shared')}}|{{java.get('chapterId')}}|{{$.content}}`
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	b := testContentBook()
	b.Variable = `{"shared":"book"}`
	chapter := testContentChapter()
	chapter.Variable = `{"shared":"chapter","chapterId":"c1"}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`{"content":"正文"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: b, Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "chapter|c1|正文" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceFormatsHTMLContentAndKeepsImages(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = ".content@html"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<div class="content"><p>第一段&nbsp;</p><!-- ignored --><img data-src="/img/1.jpg"><p>第二段</p></div>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一段\n<img src=\"https://site.example.test/img/1.jpg\">\n第二段" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceAppliesFormEncodedChapterBody(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content,{method:'POST',body:'chapter_id=1&bookId=218440'}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if fetcher.lastReq.Headers["Content-Type"] != "application/x-www-form-urlencoded" || string(fetcher.lastReq.Body) != "chapter_id=1&bookId=218440" {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
}

func TestContentServiceEncodesQueryByDefault(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content?keyword=论语`
	fetcher := &fakeContentFetcher{match: func(req fetch.Request) (*fetch.Response, bool) {
		if req.URL == "https://site.example.test/api/content?keyword=%E8%AE%BA%E8%AF%AD" {
			return &fetch.Response{URL: req.URL, Body: []byte(`{"content":"第一段"}`)}, true
		}
		return nil, false
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if fetcher.lastReq.URL != "https://site.example.test/api/content?keyword=%E8%AE%BA%E8%AF%AD" {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
}

func TestContentServiceAppliesHeaderJS(t *testing.T) {
	src := testContentSource()
	src.Header = `@js:JSON.stringify({"X-Token":"abc"})`
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src}))

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if fetcher.lastReq.Headers["X-Token"] != "abc" {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
}

func TestContentServiceAppliesOriginOptionAsHeaders(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content,{method:'POST',origin:'https://site.example.test',body:'chapter_id=1&bookId=218440'}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if fetcher.lastReq.Headers["Origin"] != "https://site.example.test" || fetcher.lastReq.Headers["Referer"] != "https://site.example.test" {
		t.Fatalf("last request = %#v", fetcher.lastReq)
	}
}

func TestContentServiceExpandsChapterURLVariablesBeforeFetch(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content,{"method":"POST","body":"chapter_id=1&bookId=@get:{bid}"}`
	chapter.BaseURL = "https://site.example.test/api/detail"
	chapter.Variable = `{"bid":"N1122344671000280502"}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if strings.Contains(string(fetcher.lastReq.Body), "bookId=@get:{bid}") {
		t.Fatalf("request body still unresolved: %#v", fetcher.lastReq)
	}
	if got.Content != "第一段" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceAppliesTwoPartReplaceRegexAsRemoval(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = "##广告|7017k"
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`{"content":"第一段\n广告\n第二段\n7017k"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一段\n\n第二段" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestContentServiceReturnsContentEmptyForNonVolumeBlankContent(t *testing.T) {
	src := testContentSource()
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<div class="content"></div>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if !errors.Is(err, ErrContentEmpty) {
		t.Fatalf("err = %v", err)
	}
}

func TestContentServiceAllowsRuleEvaluationOnNonSuccessResponse(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = `@js:"正文可继续解析"`
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {StatusCode: 404, URL: chapter.URL, Body: []byte(`not found`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "正文可继续解析" {
		t.Fatalf("content = %q", got.Content)
	}
	if !fetcher.lastReq.AllowNonSuccess {
		t.Fatalf("request did not enable AllowNonSuccess: %#v", fetcher.lastReq)
	}
}

func TestContentServiceSurfacesYueyouDownloadChapterShapeFailure(t *testing.T) {
	src := testContentSource()
	src.BookSourceURL = "https://goway.reader.yueyouxs.com/"
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	src.RuleContent.Value.Content = `@js:
time=Math.round(new Date()/1000);
uth=java.HMacHex("b313789b-2d2a-41ce-8982-26af5271fe7c&"+time,"HmacMD5","snY%169j");
sign=java.HMacHex("/userCenter/v310/downloadChapterplatId=2&appId=com.yueyou.adreader&channelId=pc&appVersion=3.6.4&srcChannelId=pc&time="+time+"&_s=C66A42978B576873D32F0EACEBBF044D&utId=996ff65deb7fbfc9f9bdcefb6c62f4ce&deviceId=183860518871512&userId=y76498329&sex=boy&wx=0&tmpToken="+java.get("tmpToken")+"&st=2&uth="+uth+"&autoBuy=false&bookId="+java.get("id")+"&chapterId="+java.get("chapterId")+"&feeState=1&inBuyView=false&isOssed=1&ossSwitch=1&readCount=2&useSrvAutoBuy=1","HmacSHA256","snY%169j");
_p=java.desEncodeToBase64String("utId=996ff65deb7fbfc9f9bdcefb6c62f4ce&deviceId=183860518871512&userId=y76498329&sex=boy&wx=0&tmpToken="+java.get("tmpToken")+"&st=2&uth="+uth+"&sign="+sign,"snY%169j","DES/ECB/PKCS5Padding","");
let option={
"method": "POST",
"body":"readCount=2&inBuyView=false&isOssed=1&bookId={{java.get("id")}}&autoBuy=false&feeState=1&useSrvAutoBuy=1&chapterId={{java.get("chapterId")}}&ossSwitch=1"};
url="https://dl.reader.yueyouxs.com/userCenter/v310/downloadChapter?platId=2&appId=com.yueyou.adreader&channelId=pc&appVersion=3.6.4&srcChannelId=pc&time="+time+"&_s=C66A42978B576873D32F0EACEBBF044D&_p="+java.encodeURI(_p)+","+JSON.stringify(option);
java.ajax(JSON.parse(java.ajax(url)).data.contentUrl)`
	chapter := testContentChapter()
	chapter.URL = `http://dl.reader.yueyouxs.com/userCenter/229222`
	chapter.BaseURL = `http://dl.reader.yueyouxs.com/userCenter/getChapterList?bookId=229221`
	chapter.Variable = `{"chapterId":"229222","id":"229221"}`
	book := testContentBook()
	book.Variable = `{"id":"229221"}`
	fetcher := &fakeContentFetcher{match: func(req fetch.Request) (*fetch.Response, bool) {
		if strings.Contains(req.URL, "/userCenter/229222") {
			return &fetch.Response{StatusCode: 404, URL: req.URL, Body: []byte(`not found`)}, true
		}
		if strings.Contains(req.URL, "/userCenter/v310/downloadChapter") {
			return &fetch.Response{StatusCode: 200, URL: req.URL, Body: []byte(`{"code":18,"data":null,"msg":"签名错误"}`)}, true
		}
		return nil, false
	}}
	runtime := script.NewRuntime(script.Policy{
		Timeout: time.Second,
		Fetcher: fetcher,
	})
	service := NewService(&fakeChapterRepo{}, fetcher, runtime)

	_, err := service.GetContent(context.Background(), Input{Source: src, Book: book, Chapter: chapter})
	if err == nil || !strings.Contains(err.Error(), "contentUrl") {
		t.Fatalf("err = %v, want contentUrl shape failure", err)
	}
}

func TestContentServiceBindsNextChapterURLToJS(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	src.RuleContent.Value.Content = `@js:nextChapterUrl`
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<html></html>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.GetContent(context.Background(), Input{
		Source:         src,
		Book:           testContentBook(),
		Chapter:        chapter,
		NextChapterURL: "https://site.example.test/c/2",
	})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "https://site.example.test/c/2" {
		t.Fatalf("content = %q", got.Content)
	}
}

func testContentSource() *source.BookSource {
	return &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://site.example.test/source",
		BookSourceName: "Source",
		RuleContent: rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{
			Title:          "h1@text",
			Content:        ".content@text",
			NextContentURL: ".next@href",
			ReplaceRegex:   "##广告##",
		}},
	}
}

func testContentBook() book.Book {
	return book.Book{
		BookURL:    "https://site.example.test/book/1",
		Origin:     "https://site.example.test/source",
		OriginName: "Source",
		Name:       "斗破苍穹",
		Author:     "天蚕土豆",
	}
}

func testContentChapter() book.BookChapter {
	return book.BookChapter{
		URL:     "https://site.example.test/c/1",
		Title:   "第一章",
		BaseURL: "https://site.example.test/toc/1",
		BookURL: "https://site.example.test/book/1",
		Index:   0,
	}
}

type fakeContentFetcher struct {
	pages   map[string]fetch.Response
	calls   int
	lastReq fetch.Request
	match   func(req fetch.Request) (*fetch.Response, bool)
}

func (f *fakeContentFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.calls++
	f.lastReq = req
	if f.match != nil {
		if resp, ok := f.match(req); ok {
			return resp, nil
		}
	}
	if resp, ok := f.pages[req.URL]; ok {
		copy := resp
		return &copy, nil
	}
	return &fetch.Response{URL: req.URL}, nil
}

type fakeChapterRepo struct {
	cached       string
	cachedOK     bool
	savedIndex   int
	savedContent string
	savedTitle   string
	savedTitles  int
}

func (r *fakeChapterRepo) ReplaceByBook(ctx context.Context, bookID string, chapters []*book.BookChapter) error {
	return nil
}
func (r *fakeChapterRepo) ListByBook(ctx context.Context, bookID string) ([]*book.BookChapter, error) {
	return nil, nil
}
func (r *fakeChapterRepo) FindByIndex(ctx context.Context, bookID string, index int) (*book.BookChapter, error) {
	return nil, nil
}
func (r *fakeChapterRepo) SaveTitle(ctx context.Context, bookID string, chapterIndex int, title string) error {
	r.savedTitles++
	r.savedTitle = title
	return nil
}
func (r *fakeChapterRepo) SaveContent(ctx context.Context, bookID string, chapterIndex int, content string) error {
	r.savedIndex = chapterIndex
	r.savedContent = content
	return nil
}
func (r *fakeChapterRepo) GetContent(ctx context.Context, bookID string, chapterIndex int) (string, bool, error) {
	return r.cached, r.cachedOK, nil
}

type fakeContentReplaceRepo struct {
	title   []*replace.ReplaceRule
	content []*replace.ReplaceRule
}

func (r *fakeContentReplaceRepo) Save(ctx context.Context, rule *replace.ReplaceRule) error {
	return nil
}
func (r *fakeContentReplaceRepo) FindByID(ctx context.Context, id int64) (*replace.ReplaceRule, error) {
	return nil, nil
}
func (r *fakeContentReplaceRepo) List(ctx context.Context, filter replace.ReplaceRuleFilter) ([]*replace.ReplaceRule, int, error) {
	return nil, 0, nil
}
func (r *fakeContentReplaceRepo) Delete(ctx context.Context, id int64) error { return nil }
func (r *fakeContentReplaceRepo) FindEnabledByTitleScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return r.title, nil
}
func (r *fakeContentReplaceRepo) FindEnabledByContentScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return r.content, nil
}

// 对齐 Legado BookContent.kt:86-89：本章末页的“下一章”链接与下一章 URL 相同时停止分页，
// 否则会把下一章正文并入本章并写入缓存（旧实现只做 seen 去重）。
func TestContentServiceStopsPagingAtNextChapterURL(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = ".content@text"
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapters := &fakeChapterRepo{}
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL:                     {URL: chapter.URL, Body: []byte(`<div class="content">CH1-para1</div><a class="next" href="/c/2">下一章</a>`)},
		"https://site.example.test/c/2": {URL: "https://site.example.test/c/2", Body: []byte(`<div class="content">CH2-para1</div>`)},
	}}
	service := NewService(chapters, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{
		Source:         src,
		Book:           testContentBook(),
		Chapter:        chapter,
		NextChapterURL: "https://site.example.test/c/2",
	})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "CH1-para1" {
		t.Fatalf("content = %q, want only current chapter", got.Content)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	if chapters.savedContent != "CH1-para1" {
		t.Fatalf("saved content = %q", chapters.savedContent)
	}
}

// 下一页链接与下一章 URL 不同（正常分页）时仍要抓取，避免把停止条件做得过宽。
func TestContentServiceKeepsPagingWhenNextURLIsNotNextChapter(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = ".content@text"
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL:                       {URL: chapter.URL, Body: []byte(`<div class="content">第一页</div><a class="next" href="/c/1-2">下一页</a>`)},
		"https://site.example.test/c/1-2": {URL: "https://site.example.test/c/1-2", Body: []byte(`<div class="content">第二页</div>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{
		Source:         src,
		Book:           testContentBook(),
		Chapter:        chapter,
		NextChapterURL: "https://site.example.test/c/2",
	})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一页\n第二页" {
		t.Fatalf("content = %q", got.Content)
	}
}

// 正文规则解析出的标题要写回章节表（对齐 Legado BookContent.kt:66-69 更新 bookChapter.title）。
func TestContentServicePersistsParsedChapterTitle(t *testing.T) {
	src := testContentSource()
	chapter := testContentChapter()
	chapters := &fakeChapterRepo{}
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<html><body><h1>第一章 正文标题</h1><div class="content">正文</div></body></html>`)},
	}}
	service := NewService(chapters, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Chapter.Title != "第一章 正文标题" {
		t.Fatalf("title = %q", got.Chapter.Title)
	}
	if chapters.savedTitles != 1 || chapters.savedTitle != "第一章 正文标题" {
		t.Fatalf("saved titles = %d %q", chapters.savedTitles, chapters.savedTitle)
	}
}

// 标题未变化时不写库，避免每次读正文都白写一次章节表。
func TestContentServiceSkipsTitleSaveWhenUnchanged(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	chapter := testContentChapter()
	chapters := &fakeChapterRepo{}
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<div class="content">正文</div>`)},
	}}
	service := NewService(chapters, fetcher, nil)

	if _, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter}); err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if chapters.savedTitles != 0 {
		t.Fatalf("saved titles = %d, want 0", chapters.savedTitles)
	}
}

// 对齐 Legado AnalyzeRule.kt:785：evalJS 顶层 title 绑定取当前章节标题。
func TestContentServiceBindsChapterTitleToJS(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	src.RuleContent.Value.Content = `@js:title`
	chapter := testContentChapter()
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		chapter.URL: {URL: chapter.URL, Body: []byte(`<html></html>`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一章" {
		t.Fatalf("content = %q, want chapter title binding", got.Content)
	}
}

// 对齐 Legado BookContent.kt:103-130：多个 nextContentUrl 并发抓取，结果按规则返回顺序拼接。
// rendezvousFetcher 让所有分页请求互相等待，顺序抓取会让第一个请求等到超时。
func TestContentServiceFetchesMultipleNextPagesConcurrentlyInOrder(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = ".content@text"
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	pages := map[string]fetch.Response{
		chapter.URL:                       {URL: chapter.URL, Body: []byte(`<div class="content">第一页</div><a class="next" href="/c/1-2">n</a><a class="next" href="/c/1-3">n</a><a class="next" href="/c/1-4">n</a>`)},
		"https://site.example.test/c/1-2": {URL: "https://site.example.test/c/1-2", Body: []byte(`<div class="content">第二页</div>`)},
		"https://site.example.test/c/1-3": {URL: "https://site.example.test/c/1-3", Body: []byte(`<div class="content">第三页</div>`)},
		"https://site.example.test/c/1-4": {URL: "https://site.example.test/c/1-4", Body: []byte(`<div class="content">第四页</div>`)},
	}
	fetcher := newRendezvousFetcher(3, pages, []string{
		"https://site.example.test/c/1-2",
		"https://site.example.test/c/1-3",
		"https://site.example.test/c/1-4",
	})
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter})
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if got.Content != "第一页\n第二页\n第三页\n第四页" {
		t.Fatalf("content = %q", got.Content)
	}
}

// rendezvousFetcher 让 paginated 中的每个 URL 都等到 wait 个请求同时在途才返回，
// 从而把“并发抓取”变成可断言的行为。
type rendezvousFetcher struct {
	pages     map[string]fetch.Response
	paginated map[string]struct{}
	wait      int32
	arrived   int32
	gate      chan struct{}
}

func newRendezvousFetcher(wait int32, pages map[string]fetch.Response, paginated []string) *rendezvousFetcher {
	marked := make(map[string]struct{}, len(paginated))
	for _, url := range paginated {
		marked[url] = struct{}{}
	}
	return &rendezvousFetcher{pages: pages, paginated: marked, wait: wait, gate: make(chan struct{})}
}

func (f *rendezvousFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	resp, ok := f.pages[req.URL]
	if !ok {
		return &fetch.Response{URL: req.URL}, nil
	}
	if _, ok := f.paginated[req.URL]; ok {
		if atomic.AddInt32(&f.arrived, 1) == f.wait {
			close(f.gate)
		}
		select {
		case <-f.gate:
		case <-time.After(5 * time.Second):
			return nil, errors.New("next pages were not fetched concurrently")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	copy := resp
	return &copy, nil
}

// 正文链路的 URL 选项 headers 同样要把非字符串值转成字符串（对齐 Legado AnalyzeUrl.kt:235-237）。
func TestContentServiceWidensNonStringHeaderValues(t *testing.T) {
	src := testContentSource()
	src.RuleContent.Value.Title = ""
	src.RuleContent.Value.Content = "$.content"
	src.RuleContent.Value.NextContentURL = ""
	src.RuleContent.Value.ReplaceRegex = ""
	chapter := testContentChapter()
	chapter.URL = `https://site.example.test/api/content,{"headers":{"X-Count":3,"X-Flag":true}}`
	fetcher := &fakeContentFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/content": {URL: "https://site.example.test/api/content", Body: []byte(`{"content":"第一段"}`)},
	}}
	service := NewService(&fakeChapterRepo{}, fetcher, nil)

	if _, err := service.GetContent(context.Background(), Input{Source: src, Book: testContentBook(), Chapter: chapter}); err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if fetcher.lastReq.Headers["X-Count"] != "3" || fetcher.lastReq.Headers["X-Flag"] != "true" {
		t.Fatalf("headers = %#v", fetcher.lastReq.Headers)
	}
}
