package bookapp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
)

func TestTocServiceParsesSinglePageChapters(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	chapters := &fakeChapterRepo{}
	service := NewTocService(chapters, &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(tocHTML(false))},
	}}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[0].Title != "第一章" || got[0].URL != "https://site.example.test/c/1" || got[0].Tag != "2026-01-01" {
		t.Fatalf("first = %#v", got[0])
	}
	if !got[1].IsVIP || !got[1].IsPay || got[1].Index != 1 {
		t.Fatalf("second = %#v", got[1])
	}
	if len(chapters.saved) != 2 || chapters.bookURL != domainbook.IDFromURL(book.BookURL) {
		t.Fatalf("saved chapters = %#v bookURL=%q", chapters.saved, chapters.bookURL)
	}
}

func TestTocServiceFetchesNextTocPagesAndDedupes(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL:                       {URL: book.TocURL, Body: []byte(tocHTML(true))},
		"https://site.example.test/toc/2": {URL: "https://site.example.test/toc/2", Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/2">第二章</a><span class="time">dup</span></li><li class="chapter"><a class="name" href="/c/3">第三章</a><span class="time">2026-01-03</span></li></ul></body></html>`)},
	}}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[2].Title != "第三章" || got[2].Index != 2 {
		t.Fatalf("third = %#v", got[2])
	}
}

func TestTocServiceParsesVolumeChapter(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{
		Source:  src,
		Book:    book,
		TocHTML: `<ul><li class="chapter"><span class="name">第一卷</span><span class="volume">true</span></li><li class="chapter"><a class="name" href="/c/1">第一章</a></li></ul>`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 || !got[0].IsVolume || got[0].URL == "" {
		t.Fatalf("chapters = %+v first=%+v", got, *got[0])
	}
}

func TestTocServiceRunsFormatJS(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.FormatJS = `"第" + index + "节 " + title`
	book := testTocBook()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(tocHTML(false))},
	}}, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if got[0].Title != "第1节 第一章" || got[1].Title != "第2节 第二章" {
		t.Fatalf("chapters = %#v", got)
	}
}

func TestTocServiceAppliesTocURLOptions(t *testing.T) {
	src := testTocSource()
	src.Header = `{'User-Agent':'Reader-Test'}`
	src.EnabledCookieJar = true
	book := testTocBook()
	book.TocURL = `https://site.example.test/api/toc,{method:'POST',body:{bookId:'1'}}`
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/toc": {URL: "https://site.example.test/api/toc", Body: []byte(tocHTML(false))},
	}}
	service := NewTocService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 || fetcher.lastReq.Method != "POST" || string(fetcher.lastReq.Body) != `{"bookId":"1"}` || fetcher.lastReq.Headers["User-Agent"] != "Reader-Test" {
		t.Fatalf("chapters=%#v req=%#v", got, fetcher.lastReq)
	}
	if !fetcher.lastReq.UseCookieJar {
		t.Fatalf("req=%#v", fetcher.lastReq)
	}
}

func TestTocServiceAppliesLoginCheckJS(t *testing.T) {
	src := testTocSource()
	src.LoginCheckJS = `result.body.replace("old", "")`
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/toc/1": {URL: "https://site.example.test/toc/1", Body: []byte(`old` + tocHTML(false))},
	}}
	service := NewTocService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: testTocBook()})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters=%#v", got)
	}
}

func TestTocServiceReversesChapterListWhenRuleStartsWithMinus(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "-.chapter"
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/toc/1": {URL: "https://site.example.test/toc/1", Body: []byte(tocHTML(false))},
	}}
	service := NewTocService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: testTocBook()})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 || got[0].Title != "第二章" || got[1].Title != "第一章" {
		t.Fatalf("chapters=%#v", got)
	}
}

func TestTocServiceExpandsTocURLVariablesBeforeFetch(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	book.Variable = `{"bookId":"229221"}`
	book.TocURL = `https://site.example.test/api/toc?bookId=@get:{bookId}`
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		"https://site.example.test/api/toc?bookId=229221": {URL: "https://site.example.test/api/toc?bookId=229221", Body: []byte(tocHTML(false))},
	}}
	service := NewTocService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 || fetcher.lastReq.URL != "https://site.example.test/api/toc?bookId=229221" {
		t.Fatalf("chapters=%#v req=%#v", got, fetcher.lastReq)
	}
}

func TestTocServiceUsesFirstNonEmptyChapterURLValue(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "$.chapters[*]"
	src.RuleToc.Value.ChapterName = "$.title"
	src.RuleToc.Value.ChapterURL = "$.urls[*]"
	book := testTocBook()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{
		Source:  src,
		Book:    book,
		TocHTML: `{"chapters":[{"title":"第一章","urls":["/c/1","/author/2"]}]}`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 1 || got[0].URL != "https://site.example.test/c/1" {
		t.Fatalf("chapters = %#v", got)
	}
}

func TestTocServiceFallsBackToBaseURLWhenHTMLFallbackNodeHasNoHref(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "tag.html"
	src.RuleToc.Value.ChapterName = "h1@text"
	src.RuleToc.Value.ChapterURL = "href"
	book := testTocBook()
	book.TocURL = "https://site.example.test/poem/1"
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{
		Source:  src,
		Book:    book,
		TocHTML: `<html><head><link rel="stylesheet" href="/css/style.css"></head><body><h1>将进酒</h1></body></html>`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[0].URL != book.TocURL {
		t.Fatalf("chapter url = %q, want %q", got[0].URL, book.TocURL)
	}
}

func TestTocServiceExpandsBookVariablesInChapterURLScriptResult(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "$.[*]"
	src.RuleToc.Value.ChapterName = "$.title"
	src.RuleToc.Value.ChapterURL = `@js:"https://site.example.test/book/chapter/content.do,{\"body\":\"{\\\"bookId\\\":\\\"@get:{bid}\\\",\\\"order\\\":\\\"{{$..order}}\\\"}\",\"method\":\"POST\"}"`
	book := testTocBook()
	book.Variable = `{"bid":"N1122344671000280502"}`
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(`[{"title":"第一章","order":"1"}]`)},
	}}
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second})
	service := NewTocService(&fakeChapterRepo{}, fetcher, runtime)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("chapters = %#v", got)
	}
	want := `https://site.example.test/book/chapter/content.do,{"body":"{\"bookId\":\"N1122344671000280502\",\"order\":\"1\"}","method":"POST"}`
	if got[0].URL != want {
		t.Fatalf("chapter url = %q, want %q", got[0].URL, want)
	}
}

func TestTocServiceExpandsRealQuyueChapterURLRule(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "$.[*]"
	src.RuleToc.Value.ChapterName = "$.title"
	src.RuleToc.Value.ChapterURL = `@js:
try{
"https://vreader.vivo.com.cn/book/chapter/content.do,{'body': '{\"model\":\"MI PAD 4\",\"imei\":\"\",\"clientVersion\":\"121020\",\"elapsedtime\":\"981290\",\"sysver\":\"\",\"nt\":\"wifi\",\"ver\":\"121020\",\"u\":\"\",\"pver\":\"0\",\"resolution\":\"1920*1200\",\"sessionId\":\"11901470641675854550446\",\"pixel\":\"320\",\"av\":\"27\",\"adrVerName\":\"8.1.0\",\"timestamp\":\"1675855424879\",\"browserSystem\":\"1\",\"browserSubSystem\":\"1\",\"personalRecommend\":\"1\",\"bookVersion\":\"30800\",\"vreaderVersion\":\"121020\",\"packageName\":\"com.vivo.vreader\",\"udid\":\"\",\"openudid\":\"b443cebc6f614746\",\"bookshelfBookIds\":[],\"bookShelfListenBookIds\":[],\"featureValues\":\"2\",\"bookId\":\"@get:{bid}\",\"order\":\"{{$..order}}\"}','method':'POST'}"
}
catch(e){
"https://fallback.example.test"
}`
	book := testTocBook()
	book.Variable = `{"bid":"N1122344671000280502"}`
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(`[{"title":"第一章 陨落的天才","order":"1"}]`)},
	}}
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second})
	service := NewTocService(&fakeChapterRepo{}, fetcher, runtime)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("chapters = %#v", got)
	}
	if strings.Contains(got[0].URL, "@get:{bid}") || strings.Contains(got[0].URL, "{{$..order}}") {
		t.Fatalf("chapter url still unresolved: %q", got[0].URL)
	}
}

func TestTocServicePreservesChapterVariableFromRuleContext(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "$.[*]"
	src.RuleToc.Value.ChapterName = "$.title@put:{bid:bid}"
	src.RuleToc.Value.ChapterURL = `@js:"https://site.example.test/book/chapter/content.do,{'body':'{\"bookId\":\"@get:{bid}\"}','method':'POST'}"`
	book := testTocBook()
	book.Variable = `{"bid":"N1122344671000280502"}`
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(`[{"title":"第一章","order":"1"}]`)},
	}}
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second})
	service := NewTocService(&fakeChapterRepo{}, fetcher, runtime)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[0].Variable == "" {
		t.Fatalf("chapter variable is empty: %#v", got[0])
	}
}

func TestTocServiceCarriesBookVariablesAcrossNextPages(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "$.chapters[*]"
	src.RuleToc.Value.ChapterName = `@js:if (result.shared) { book.putVariable("shared", result.shared); } result.name`
	src.RuleToc.Value.ChapterURL = `@get:{shared}/{{$.name}}`
	src.RuleToc.Value.NextTocURL = "$.next"
	book := testTocBook()
	fetcher := &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL:                       {URL: book.TocURL, Body: []byte(`{"chapters":[{"name":"第一页章节","shared":"/carry/1"}],"next":"/toc/2"}`)},
		"https://site.example.test/toc/2": {URL: "https://site.example.test/toc/2", Body: []byte(`{"chapters":[{"name":"第二页章节"}],"next":""}`)},
	}}
	service := NewTocService(&fakeChapterRepo{}, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[0].URL != "https://site.example.test/carry/1/%E7%AC%AC%E4%B8%80%E9%A1%B5%E7%AB%A0%E8%8A%82" || got[1].URL != "https://site.example.test/carry/1/%E7%AC%AC%E4%BA%8C%E9%A1%B5%E7%AB%A0%E8%8A%82" {
		t.Fatalf("chapter0=%#v chapter1=%#v", *got[0], *got[1])
	}
}

func testTocSource() *source.BookSource {
	return &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://site.example.test/source",
		BookSourceName: "Source",
		RuleToc: rule.Flexible[rule.TocRule]{Value: &rule.TocRule{
			ChapterList: ".chapter",
			ChapterName: ".name@text",
			ChapterURL:  ".name@href",
			IsVolume:    ".volume@text",
			IsVIP:       ".vip@text",
			IsPay:       ".pay@text",
			UpdateTime:  ".time@text",
			NextTocURL:  ".next@href",
		}},
	}
}

func testTocBook() domainbook.Book {
	return domainbook.Book{
		BookURL:    "https://site.example.test/book/1",
		TocURL:     "https://site.example.test/toc/1",
		Origin:     "https://site.example.test/source",
		OriginName: "Source",
		Name:       "斗破苍穹",
		Author:     "天蚕土豆",
	}
}

func tocHTML(next bool) string {
	nextLink := ""
	if next {
		nextLink = `<a class="next" href="/toc/2">next</a>`
	}
	return `<html><body><ul><li class="chapter"><a class="name" href="/c/1">第一章</a><span class="time">2026-01-01</span></li><li class="chapter"><a class="name" href="/c/2">第二章</a><span class="vip">true</span><span class="pay">1</span><span class="time">2026-01-02</span></li></ul>` + nextLink + `</body></html>`
}

type fakeTocFetcher struct {
	pages   map[string]fetch.Response
	lastReq fetch.Request
}

func (f *fakeTocFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.lastReq = req
	if resp, ok := f.pages[req.URL]; ok {
		copy := resp
		return &copy, nil
	}
	return &fetch.Response{URL: req.URL}, nil
}

type fakeChapterRepo struct {
	bookURL string
	saved   []*domainbook.BookChapter
}

func (r *fakeChapterRepo) ReplaceByBook(ctx context.Context, bookID string, chapters []*domainbook.BookChapter) error {
	r.bookURL = bookID
	r.saved = append([]*domainbook.BookChapter(nil), chapters...)
	return nil
}
func (r *fakeChapterRepo) ListByBook(ctx context.Context, bookID string) ([]*domainbook.BookChapter, error) {
	return nil, nil
}
func (r *fakeChapterRepo) FindByIndex(ctx context.Context, bookID string, index int) (*domainbook.BookChapter, error) {
	return nil, nil
}
func (r *fakeChapterRepo) SaveTitle(ctx context.Context, bookID string, chapterIndex int, title string) error {
	return nil
}
func (r *fakeChapterRepo) SaveContent(ctx context.Context, bookID string, chapterIndex int, content string) error {
	return nil
}
func (r *fakeChapterRepo) GetContent(ctx context.Context, bookID string, chapterIndex int) (string, bool, error) {
	return "", false, nil
}

// 对齐 Legado StringExtensions.kt:74-79：非空且不是 false/no/not/0 即为真。
// 书源常用“VIP”“是”表示真值，旧实现把它们判成 false，丢掉 VIP/付费标识与卷章节语义。
func TestIsTruthyMatchesLegadoIsTrue(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"   ":   false,
		"null":  false,
		"false": false,
		"FALSE": false,
		"no":    false,
		"not":   false,
		"0":     false,
		"true":  true,
		"1":     true,
		"yes":   true,
		"VIP":   true,
		"是":     true,
		"否":     true,
		" null": true,
	}
	for value, want := range cases {
		if got := isTruthy(value); got != want {
			t.Fatalf("isTruthy(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestTocServiceParsesTruthyFlagsLikeLegado(t *testing.T) {
	src := testTocSource()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{
		Source: src,
		Book:   testTocBook(),
		TocHTML: `<ul>
<li class="chapter"><span class="name">第一卷</span><span class="volume">是</span></li>
<li class="chapter"><a class="name" href="/c/1">第一章</a><span class="vip">VIP</span><span class="pay">0</span></li>
</ul>`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if !got[0].IsVolume {
		t.Fatalf("volume flag = false, want true: %#v", got[0])
	}
	if !got[1].IsVIP || got[1].IsPay {
		t.Fatalf("vip/pay = %v/%v, want true/false: %#v", got[1].IsVIP, got[1].IsPay, got[1])
	}
}

// 对齐 Legado BookChapterList.kt:135-146：单章 formatJs 出错只记日志并保留原标题，不中断整次目录刷新。
func TestTocServiceKeepsChapterTitleWhenFormatJSFails(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.FormatJS = `if (index === 1) { throw new Error("boom"); }
"第" + index + "节 " + title`
	book := testTocBook()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(tocHTML(false))},
	}}, script.NewRuntime(script.Policy{Timeout: time.Second}))

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[0].Title != "第一章" {
		t.Fatalf("first title = %q, want original title kept", got[0].Title)
	}
	if got[1].Title != "第2节 第二章" {
		t.Fatalf("second title = %q", got[1].Title)
	}
}

// 对齐 Legado BookChapterList.kt:101-113：多个 nextTocUrl 并发抓取，章节按规则返回顺序拼接。
func TestTocServiceFetchesMultipleNextPagesConcurrentlyInOrder(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	pages := map[string]fetch.Response{
		book.TocURL:                       {URL: book.TocURL, Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/1">第一章</a></li></ul><a class="next" href="/toc/2">n</a><a class="next" href="/toc/3">n</a></body></html>`)},
		"https://site.example.test/toc/2": {URL: "https://site.example.test/toc/2", Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/2">第二章</a></li></ul></body></html>`)},
		"https://site.example.test/toc/3": {URL: "https://site.example.test/toc/3", Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/3">第三章</a></li></ul></body></html>`)},
	}
	fetcher := newRendezvousTocFetcher(2, pages, []string{"https://site.example.test/toc/2", "https://site.example.test/toc/3"})
	service := NewTocService(&fakeChapterRepo{}, fetcher, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("chapters = %#v", got)
	}
	for i, want := range []string{"第一章", "第二章", "第三章"} {
		if got[i].Title != want || got[i].Index != i {
			t.Fatalf("chapter %d = %#v, want %q", i, got[i], want)
		}
	}
}

// rendezvousTocFetcher 让 paginated 中的每个 URL 都等到 wait 个请求同时在途才返回，
// 从而把“并发抓取”变成可断言的行为（顺序抓取会等到超时）。
type rendezvousTocFetcher struct {
	pages     map[string]fetch.Response
	paginated map[string]struct{}
	wait      int32
	arrived   int32
	gate      chan struct{}
}

func newRendezvousTocFetcher(wait int32, pages map[string]fetch.Response, paginated []string) *rendezvousTocFetcher {
	marked := make(map[string]struct{}, len(paginated))
	for _, url := range paginated {
		marked[url] = struct{}{}
	}
	return &rendezvousTocFetcher{pages: pages, paginated: marked, wait: wait, gate: make(chan struct{})}
}

func (f *rendezvousTocFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
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
			return nil, errors.New("next toc pages were not fetched concurrently")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	copy := resp
	return &copy, nil
}

// 目录多分页中单个分页失败时目录不完整，RefreshToc 返回错误，
// 库内旧目录不会被残缺结果覆盖（C-016 同类修复）。
func TestTocServiceFailsAndKeepsExistingChaptersWhenNextPageFails(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	existing := []*domainbook.BookChapter{
		{Index: 0, Title: "旧章节", URL: "https://site.example.test/c/old"},
	}
	repo := &fakeChapterRepo{saved: existing}
	failing := &failingTocFetcher{pages: map[string]fetch.Response{
		book.TocURL:                       {URL: book.TocURL, Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/1">第一章</a></li></ul><a class="next" href="/toc/2">n</a><a class="next" href="/toc/3">n</a></body></html>`)},
		"https://site.example.test/toc/2": {URL: "https://site.example.test/toc/2", Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/2">第二章</a></li></ul></body></html>`)},
	}, fail: "https://site.example.test/toc/3"}
	service := NewTocService(repo, failing, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err == nil {
		t.Fatalf("RefreshToc() error = nil, want incomplete toc error; chapters = %#v", got)
	}
	var incomplete *incompleteTocError
	if !errors.As(err, &incomplete) {
		t.Fatalf("error = %v, want *incompleteTocError", err)
	}
	if len(repo.saved) != len(existing) {
		t.Fatalf("saved chapters = %#v, want existing %d chapters kept", repo.saved, len(existing))
	}
}

// 首页本身失败（如 HTTP 500）时目录不完整，RefreshToc 返回错误，
// 库内旧目录不会被残缺结果覆盖（C-016 同类修复）。
func TestTocServiceFailsAndKeepsExistingChaptersWhenFirstPageFails(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	existing := []*domainbook.BookChapter{
		{Index: 0, Title: "旧章节", URL: "https://site.example.test/c/old"},
	}
	repo := &fakeChapterRepo{saved: existing}
	failing := &failingTocFetcher{pages: map[string]fetch.Response{}, fail: book.TocURL}
	service := NewTocService(repo, failing, nil)

	_, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err == nil {
		t.Fatalf("RefreshToc() error = nil, want error")
	}
	if len(repo.saved) != len(existing) {
		t.Fatalf("saved chapters = %#v, want existing %d chapters kept", repo.saved, len(existing))
	}
}

// 分页数超过 maxTocPages 上限被截断时目录不完整，RefreshToc 返回 ErrTocPaginationLimit，
// 库内旧目录不会被残缺结果覆盖（C-016 同类修复）。
func TestTocServiceFailsAndKeepsExistingChaptersWhenPaginationExceedsLimit(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	existing := []*domainbook.BookChapter{
		{Index: 0, Title: "旧章节", URL: "https://site.example.test/c/old"},
	}
	repo := &fakeChapterRepo{saved: existing}
	// 每页都有指向下一页的 nextTocURL，页数远超 maxTocPages=100，触发截断。
	pageHTML := func(n int) string {
		return fmt.Sprintf(`<html><body><ul><li class="chapter"><a class="name" href="/c/%d">第%d章</a></li></ul><a class="next" href="/toc/%d">n</a></body></html>`, n, n, n+1)
	}
	fetcher := &selfReferencingTocFetcher{page: pageHTML}
	service := NewTocService(repo, fetcher, nil)

	_, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if !errors.Is(err, ErrTocPaginationLimit) {
		t.Fatalf("error = %v, want ErrTocPaginationLimit", err)
	}
	if len(repo.saved) != len(existing) {
		t.Fatalf("saved chapters = %#v, want existing %d chapters kept", repo.saved, len(existing))
	}
}

// selfReferencingTocFetcher 对任意 /toc/<n> 返回带 next 链接的页面。
type selfReferencingTocFetcher struct {
	page func(n int) string
}

func (f *selfReferencingTocFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	const prefix = "https://site.example.test/toc/"
	if !strings.HasPrefix(req.URL, prefix) {
		return &fetch.Response{URL: req.URL}, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(req.URL, prefix))
	if err != nil {
		return &fetch.Response{URL: req.URL}, nil
	}
	return &fetch.Response{URL: req.URL, Body: []byte(f.page(n))}, nil
}

// failingTocFetcher 对 fail 指定的 URL 返回错误，其余走 pages。
type failingTocFetcher struct {
	pages map[string]fetch.Response
	fail  string
}

func (f *failingTocFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if req.URL == f.fail {
		return nil, errors.New("boom")
	}
	if resp, ok := f.pages[req.URL]; ok {
		copy := resp
		return &copy, nil
	}
	return &fetch.Response{URL: req.URL}, nil
}
