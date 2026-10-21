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
		"https://site.example.test/toc/2": {URL: "https://site.example.test/toc/2", Body: []byte(`<html><body><ul><li class="chapter"><a class="name" href="/c/2">第二章</a><span class="vip">true</span><span class="pay">1</span><span class="time">2026-01-02</span></li><li class="chapter"><a class="name" href="/c/3">第三章</a><span class="time">2026-01-03</span></li></ul></body></html>`)},
	}}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	// 完整键去重：第二页 /c/2 与首页 /c/2 的 url+title+tag 全部相同，按 Legado LinkedHashSet
	// 保留最后一次出现（第二页的），共 3 章。
	if len(got) != 3 {
		t.Fatalf("chapters = %#v", got)
	}
	if got[1].URL != "https://site.example.test/c/2" || got[1].Tag != "2026-01-02" {
		t.Fatalf("second = %#v", got[1])
	}
	if got[2].Title != "第三章" || got[2].Index != 2 {
		t.Fatalf("third = %#v", got[2])
	}
}

// TestTocServiceKeepsSameURLWithDifferentTags 对齐 Legado BookChapter data class equals：
// 同 URL 但 tag/标题不同的两条都会保留（S12-02），不再按 URL 单字段误删。
func TestTocServiceKeepsSameURLWithDifferentTags(t *testing.T) {
	src := testTocSource()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{
		Source: src,
		Book:   testTocBook(),
		TocHTML: `<ul>
<li class="chapter"><a class="name" href="/c/1">第一章</a><span class="time">2026-01-01</span></li>
<li class="chapter"><a class="name" href="/c/1">第一章</a><span class="time">2026-02-01</span></li>
<li class="chapter"><a class="name" href="/c/1">第一章修订</a><span class="time">2026-01-01</span></li>
</ul>`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("chapters = %#v, want same URL with different tags/titles kept", got)
	}
}

// TestTocServiceDedupeKeepsLastOccurrence 对齐 Legado 非倒序目录先 reverse 再去重：
// 完全相同的章节（url+title+tag 等全字段一致）重复时保留最后一次出现的章节（S12-02）。
func TestTocServiceDedupeKeepsLastOccurrence(t *testing.T) {
	src := testTocSource()
	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{
		Source: src,
		Book:   testTocBook(),
		TocHTML: `<ul>
<li class="chapter"><a class="name" href="/c/1">第一章</a><span class="time">2026-01-01</span></li>
<li class="chapter"><a class="name" href="/c/2">第二章</a><span class="time">2026-01-02</span></li>
<li class="chapter"><a class="name" href="/c/1">第一章</a><span class="time">2026-01-01</span></li>
</ul>`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	// 顺序为 reverse → 去重（保留后出现）→ reverse：/c/1 的第二次出现（与第一次全字段一致）
	// 被保留，反回后 /c/2 在 /c/1 之前。
	if got[0].URL != "https://site.example.test/c/2" || got[0].Tag != "2026-01-02" {
		t.Fatalf("first = %#v", got[0])
	}
	if got[1].URL != "https://site.example.test/c/1" || got[1].Tag != "2026-01-01" {
		t.Fatalf("second = %#v", got[1])
	}
}

// TestTocServiceWritesBackBookMetadataAfterRefresh 对齐 Legado BookChapterList.kt:152-161：
// 刷新后写回 total_chapter_num、lastCheckTime，新增章数>0 时更新 lastCheckCount/latestChapterTime（S12-01）。
func TestTocServiceWritesBackBookMetadataAfterRefresh(t *testing.T) {
	src := testTocSource()
	b := testTocBook()
	b.TotalChapterNum = 1
	chapters := &fakeChapterRepo{}
	service := NewTocService(chapters, &fakeTocFetcher{pages: map[string]fetch.Response{
		b.TocURL: {URL: b.TocURL, Body: []byte(tocHTML(false))},
	}}, nil).WithBooks(chapters)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: b})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	stats := chapters.stats
	if stats.TotalChapterNum != 2 {
		t.Fatalf("TotalChapterNum = %d, want 2", stats.TotalChapterNum)
	}
	if stats.LastCheckTime == 0 {
		t.Fatalf("LastCheckTime not set: %+v", stats)
	}
	if stats.LastCheckCount != 1 {
		t.Fatalf("LastCheckCount = %d, want 1 (2 - 1)", stats.LastCheckCount)
	}
	if stats.LatestChapterTime == 0 {
		t.Fatalf("LatestChapterTime not set: %+v", stats)
	}
}

// TestTocServiceClampsProgressWhenTocShrinks 对齐 Legado BookHelp.getDurChapter：
// 章节收缩后 dur_chapter_index 被钳制且 total_chapter_num 更新（S12-01）。
func TestTocServiceClampsProgressWhenTocShrinks(t *testing.T) {
	src := testTocSource()
	b := testTocBook()
	b.DurChapterIndex = 5
	b.DurChapterTitle = "第六章"
	b.TotalChapterNum = 6
	chapters := &fakeChapterRepo{}
	service := NewTocService(chapters, &fakeTocFetcher{pages: map[string]fetch.Response{
		b.TocURL: {URL: b.TocURL, Body: []byte(tocHTML(false))},
	}}, nil).WithBooks(chapters)

	if _, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: b}); err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	stats := chapters.stats
	if stats.TotalChapterNum != 2 {
		t.Fatalf("TotalChapterNum = %d, want 2", stats.TotalChapterNum)
	}
	if stats.DurChapterIndex < 0 || stats.DurChapterIndex > 1 {
		t.Fatalf("DurChapterIndex = %d, want clamped into [0,1]", stats.DurChapterIndex)
	}
	if stats.DurChapterTitle == "" {
		t.Fatalf("DurChapterTitle not set: %+v", stats)
	}
	if !stats.DurChapterChanged {
		t.Fatalf("DurChapterChanged = false, want true (index 5 -> clamped)")
	}
}

// TestTocServiceRemapsProgressByTitle 对齐 Legado BookHelp.getDurChapter 的标题 Jaccard
// 相似度重映射：头部插入新章后旧进度按标题对齐到新列表中的同一章节。
func TestTocServiceRemapsProgressByTitle(t *testing.T) {
	src := testTocSource()
	b := testTocBook()
	// Legado getDurChapter 对 index<=0 直接返回 0，这里用 index=1 触发重映射。
	b.DurChapterIndex = 1
	b.DurChapterTitle = "第一章 陨落的天才"
	b.TotalChapterNum = 1
	chapters := &fakeChapterRepo{}
	service := NewTocService(chapters, &fakeTocFetcher{pages: map[string]fetch.Response{
		b.TocURL: {URL: b.TocURL, Body: []byte(`<html><body><ul>
<li class="chapter"><a class="name" href="/c/0">序章</a></li>
<li class="chapter"><a class="name" href="/c/1">第一章 陨落的天才</a></li>
<li class="chapter"><a class="name" href="/c/2">第二章</a></li>
</ul></body></html>`)},
	}}, nil).WithBooks(chapters)

	if _, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: b}); err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if chapters.stats.DurChapterIndex != 1 {
		t.Fatalf("DurChapterIndex = %d, want 1 (remapped by title)", chapters.stats.DurChapterIndex)
	}
}

// TestTocServicePersistsVariableChangedByPreUpdateJS 对齐 Legado WebBook.kt:211-221：
// preUpdateJs 修改的 book.variable 跨刷新持久化（S12-05）。
func TestTocServicePersistsVariableChangedByPreUpdateJS(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.PreUpdateJS = `book.putVariable("refreshedAt", "2026-10-21")`
	b := testTocBook()
	chapters := &fakeChapterRepo{}
	service := NewTocService(chapters, &fakeTocFetcher{pages: map[string]fetch.Response{
		b.TocURL: {URL: b.TocURL, Body: []byte(tocHTML(false))},
	}}, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})).WithBooks(chapters)

	if _, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: b}); err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if !chapters.stats.VariableChanged {
		t.Fatalf("VariableChanged = false, want true: %+v", chapters.stats)
	}
	if !strings.Contains(chapters.stats.Variable, "refreshedAt") {
		t.Fatalf("Variable = %q, want refreshedAt persisted", chapters.stats.Variable)
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
	stats   domainbook.TocStats
}

func (r *fakeChapterRepo) ReplaceByBook(ctx context.Context, bookID string, chapters []*domainbook.BookChapter) error {
	r.bookURL = bookID
	r.saved = append([]*domainbook.BookChapter(nil), chapters...)
	return nil
}

// UpdateTocStats 与 ReplaceByBook 等价记录：fake 仓库不区分列级更新。
func (r *fakeChapterRepo) UpdateTocStats(ctx context.Context, bookID string, stats domainbook.TocStats) error {
	r.stats = stats
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

func (r *fakeChapterRepo) Save(ctx context.Context, b *domainbook.Book) error { return nil }
func (r *fakeChapterRepo) UpdateInfo(ctx context.Context, b *domainbook.Book) error {
	return nil
}
func (r *fakeChapterRepo) FindByID(ctx context.Context, id string) (*domainbook.Book, error) {
	return nil, nil
}
func (r *fakeChapterRepo) FindByURL(ctx context.Context, bookURL string) (*domainbook.Book, error) {
	return nil, nil
}
func (r *fakeChapterRepo) List(ctx context.Context, filter domainbook.BookFilter) ([]*domainbook.Book, int, error) {
	return nil, 0, nil
}
func (r *fakeChapterRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *fakeChapterRepo) SaveProgress(ctx context.Context, id string, progress domainbook.BookProgress) error {
	return nil
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

// SkipSave 对应 Legado CheckSource/Debug 的 needSave=false：目录可正常解析，但不得写 chapters 表。
func TestTocServiceSkipSaveDoesNotWriteChapters(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	repo := &fakeChapterRepo{}
	service := NewTocService(repo, &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(tocHTML(false))},
	}}, nil)

	got, err := service.RefreshToc(context.Background(), TocInput{Source: src, Book: book, SkipSave: true})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if len(repo.saved) != 0 || repo.bookURL != "" {
		t.Fatalf("chapters were saved: %#v bookURL=%q", repo.saved, repo.bookURL)
	}
}

func TestBookServiceFetchTocSkipsChaptersTable(t *testing.T) {
	src := testTocSource()
	book := testTocBook()
	repo := &fakeChapterRepo{}
	service := NewService(nil, repo, nil, nil, NewTocService(repo, &fakeTocFetcher{pages: map[string]fetch.Response{
		book.TocURL: {URL: book.TocURL, Body: []byte(tocHTML(false))},
	}}, nil))

	got, err := service.FetchToc(context.Background(), src, book)
	if err != nil {
		t.Fatalf("FetchToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("chapters were saved: %#v", repo.saved)
	}
}

func TestFirstContentChapter(t *testing.T) {
	chapter := func(index int, url string, volume bool) *domainbook.BookChapter {
		return &domainbook.BookChapter{Index: index, URL: url, IsVolume: volume}
	}
	cases := []struct {
		name      string
		chapters  []*domainbook.BookChapter
		wantIndex int
		wantNext  string
		wantOK    bool
	}{
		{name: "empty", chapters: nil, wantOK: false},
		{name: "all nil", chapters: []*domainbook.BookChapter{nil, nil}, wantOK: false},
		{
			name:      "first chapter is content",
			chapters:  []*domainbook.BookChapter{chapter(0, "u0", false), chapter(1, "u1", false)},
			wantIndex: 0,
			wantNext:  "u1",
			wantOK:    true,
		},
		{
			name:      "skips volume chapters",
			chapters:  []*domainbook.BookChapter{chapter(5, "v0", true), nil, chapter(6, "u6", false), chapter(7, "u7", false)},
			wantIndex: 6,
			wantNext:  "u7",
			wantOK:    true,
		},
		{
			name:      "only last chapter is content falls back to first url",
			chapters:  []*domainbook.BookChapter{chapter(0, "v0", true), chapter(1, "v1", true), chapter(2, "u2", false)},
			wantIndex: 2,
			wantNext:  "v0",
			wantOK:    true,
		},
		{
			name:      "only volumes picks chapter zero",
			chapters:  []*domainbook.BookChapter{chapter(0, "v0", true), chapter(1, "v1", true)},
			wantIndex: 0,
			wantNext:  "v1",
			wantOK:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, next, ok := FirstContentChapter(tc.chapters)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.Index != tc.wantIndex {
				t.Fatalf("index = %d, want %d", got.Index, tc.wantIndex)
			}
			if next != tc.wantNext {
				t.Fatalf("next = %q, want %q", next, tc.wantNext)
			}
		})
	}
}

// 末章必须回退到第 0 章 URL（对齐 Legado BookContent.kt:52-56）。
func TestFirstContentChapterLastChapterFallsBackToFirstURL(t *testing.T) {
	chapters := []*domainbook.BookChapter{
		{Index: 0, URL: "u0"},
		{Index: 1, URL: "u1"},
		{Index: 2, URL: "u2"},
	}
	// 把最后一章挪到首位，确保选中它时越界回退到切片第 0 个元素。
	got, next, ok := FirstContentChapter([]*domainbook.BookChapter{chapters[2], chapters[0], chapters[1]})
	if !ok || got.URL != "u2" {
		t.Fatalf("ok=%v url=%q", ok, got.URL)
	}
	if next != "u0" {
		t.Fatalf("next = %q, want %q", next, "u0")
	}
}
