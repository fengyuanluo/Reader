package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reader/internal/domain/book"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
)

// TestSelectCandidatesAuthorVote 验证多源投票：同名书 2 个源为天蚕土豆、
// 1 个源为同人作者，不指定 --author 时只剩天蚕土豆的条目。
func TestSelectCandidatesAuthorVote(t *testing.T) {
	sources := map[string]*source.BookSource{}
	items := []book.SearchBook{}
	for i, author := range []string{"天蚕土豆", "某同人", "天蚕土豆"} {
		origin := "http://origin-" + string(rune('a'+i))
		sources[origin] = &source.BookSource{SourceID: origin, BookSourceURL: origin, BookSourceName: origin, Weight: 1, RespondTime: 100}
		items = append(items, book.SearchBook{BookURL: origin + "/book/1", Origin: origin, Name: "斗破苍穹", Author: author})
	}
	got := selectCandidates(items, "斗破苍穹", "", sources)
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2 (天蚕土豆 only)", len(got))
	}
	for _, c := range got {
		if c.item.Author != "天蚕土豆" {
			t.Fatalf("author = %q, want 天蚕土豆", c.item.Author)
		}
	}
}

// TestRankProbes 验证选源排名：980 章 1s 优于 1000 章 5s（第一梯队按耗时）；
// 500 章排后面；首章过短的探测不合格被剔除。
func TestRankProbes(t *testing.T) {
	mk := func(id string, chapters, contentLen int, elapsed time.Duration) probeResult {
		return probeResult{
			candidate:      fetchCandidate{source: &source.BookSource{BookSourceName: id}},
			ok:             true,
			nonVolumeCount: chapters,
			contentLen:     contentLen,
			elapsed:        elapsed,
		}
	}
	probes := []probeResult{
		mk("slow", 1000, 5000, 5*time.Second),
		mk("fast", 980, 5000, 1*time.Second),
		mk("few", 500, 5000, 100*time.Millisecond),
		mk("short", 900, 100, 10*time.Millisecond),
	}
	// 不合格的探测直接标记 ok=false。
	probes[3].ok = false
	got := rankProbes(probes)
	if len(got) != 3 {
		t.Fatalf("ranked = %d, want 3 (short probe excluded)", len(got))
	}
	if got[0].candidate.source.BookSourceName != "fast" {
		t.Fatalf("first = %s, want fast (980 >= 0.95*1000, T=1s)", got[0].candidate.source.BookSourceName)
	}
	if got[1].candidate.source.BookSourceName != "slow" {
		t.Fatalf("second = %s, want slow", got[1].candidate.source.BookSourceName)
	}
	if got[2].candidate.source.BookSourceName != "few" {
		t.Fatalf("third = %s, want few", got[2].candidate.source.BookSourceName)
	}
}

// TestDownloadChaptersOrderAndConcurrency 基于 mock 源用 workers=4 下载含卷章节的
// 多章，断言 sections 顺序与目录一致、卷章节被跳过，并记录最大并发数。
func TestDownloadChaptersOrderAndConcurrency(t *testing.T) {
	env := newTestEnv(t)
	mock := newCLIMockSource(t)

	var (
		mu       sync.Mutex
		inflight int
		maxGain  int
		gate     = make(chan struct{})
		unblock  sync.Once
	)
	mock.onContent1 = func() {
		mu.Lock()
		inflight++
		if inflight > maxGain {
			maxGain = inflight
		}
		if inflight >= 2 {
			unblock.Do(func() { close(gate) })
		}
		mu.Unlock()
		<-gate
	}
	bookID := addMockBook(t, env, mock)

	chapters, b, err := env.rt.Books.ListChapters(env.ctx, bookID)
	if err != nil {
		t.Fatalf("ListChapters: %v", err)
	}

	var stderr bytes.Buffer
	sections, summary := downloadChapters(env.ctx, env.rt, b, bookID, chapters, 0, len(chapters)-1, 4, &stderr)
	mu.Lock()
	observedMax := maxGain
	mu.Unlock()
	if observedMax < 2 {
		t.Fatalf("observed max concurrency = %d, want >= 2", observedMax)
	}
	if summary.Skipped != 1 || summary.Downloaded != 2 || len(summary.Failed) != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	want := []string{"第一章", "第二章"}
	if len(sections) != len(want) {
		t.Fatalf("sections = %d, want %d", len(sections), len(want))
	}
	for i, title := range want {
		if !strings.Contains(sections[i], "=== "+[]string{"0", "2"}[i]+" "+title) {
			t.Fatalf("sections[%d] = %q, want %s", i, sections[i], title)
		}
	}
}

// TestDownloadChaptersNextChapterURLByCatalog 验证子区间下载时 nextChapterURL 按
// 目录判断（与 --to 无关）：--to 落在中间时，第 to 章的 nextChapterURL 是
// chapters[to+1].URL，而不是错误回退到第 0 章（C-050d 回归）。
func TestDownloadChaptersNextChapterURLByCatalog(t *testing.T) {
	env := newTestEnv(t)
	rt := env.rt
	ctx := env.ctx
	// 4 个非卷章节，内容规则直接回显 nextChapterUrl。
	mux := http.NewServeMux()
	mux.HandleFunc("/c/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">占位</div>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	src := &source.BookSource{
		SourceID:       "src-nexturl",
		BookSourceURL:  server.URL + "/source",
		BookSourceName: "NextURL",
		Enabled:        true,
		RuleContent:    rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: `@js:nextChapterUrl`}},
	}
	if err := rt.SourceRepo.Save(ctx, src); err != nil {
		t.Fatalf("save source: %v", err)
	}
	b := &book.Book{
		BookURL:    server.URL + "/book/1",
		Origin:     src.BookSourceURL,
		OriginName: src.BookSourceName,
		Name:       "斗破苍穹",
	}
	if _, err := rt.Books.Save(ctx, b); err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(b.BookURL)
	chapters := make([]*book.BookChapter, 4)
	for i := range chapters {
		chapters[i] = &book.BookChapter{
			BookURL: b.BookURL,
			URL:     fmt.Sprintf("%s/c/%d", server.URL, i+1),
			Title:   fmt.Sprintf("第%d章", i+1),
			Index:   i,
		}
	}
	if err := rt.ChapterRepo.ReplaceByBook(ctx, bookID, chapters); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}

	var stderr bytes.Buffer
	// 子区间 [0,1]：第 1 章（index 1）的 nextChapterURL 应是 chapters[2].URL。
	sections, summary := downloadChapters(ctx, rt, b, bookID, chapters, 0, 1, 4, &stderr)
	if len(summary.Failed) != 0 {
		t.Fatalf("summary = %+v, stderr: %s", summary, stderr.String())
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(sections))
	}
	if !strings.Contains(sections[1], chapters[2].URL) {
		t.Fatalf("sections[1] = %q, want nextChapterURL %q", sections[1], chapters[2].URL)
	}
	// 末章回退到第 0 章 URL。
	sections, summary = downloadChapters(ctx, rt, b, bookID, chapters, 0, 3, 4, &stderr)
	if len(summary.Failed) != 0 {
		t.Fatalf("summary = %+v, stderr: %s", summary, stderr.String())
	}
	if !strings.Contains(sections[3], chapters[0].URL) {
		t.Fatalf("last section = %q, want fallback %q", sections[3], chapters[0].URL)
	}
}

// TestDownloadChaptersFailedOrderAscending 让多个章节失败，断言 Failed 的 index
// 严格递增（map 迭代顺序随机的回归，C-050b）。
func TestDownloadChaptersFailedOrderAscending(t *testing.T) {
	env := newTestEnv(t)
	// 手动构造书与章节，来源指向不存在的书源使每章抓取失败。
	b := &book.Book{BookURL: "https://missing.example.test/book/1", Origin: "https://missing.example.test/source", Name: "书"}
	if _, err := env.rt.Books.Save(env.ctx, b); err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(b.BookURL)
	chapters := make([]*book.BookChapter, 6)
	for i := range chapters {
		chapters[i] = &book.BookChapter{BookURL: b.BookURL, URL: fmt.Sprintf("https://missing.example.test/c/%d", i+1), Title: fmt.Sprintf("第%d章", i+1), Index: i}
	}
	if err := env.rt.ChapterRepo.ReplaceByBook(env.ctx, bookID, chapters); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}
	var stderr bytes.Buffer
	_, summary := downloadChapters(env.ctx, env.rt, b, bookID, chapters, 0, 5, 4, &stderr)
	if len(summary.Failed) != 6 {
		t.Fatalf("failed = %d, want 6; stderr: %s", len(summary.Failed), stderr.String())
	}
	for i := 1; i < len(summary.Failed); i++ {
		if summary.Failed[i].Index <= summary.Failed[i-1].Index {
			t.Fatalf("failed indexes not ascending: %+v", summary.Failed)
		}
	}
}

// TestDownloadChaptersMissingSourceFailsAll 验证书源查不到时整个区间直接失败，
// 不静默传 nil（C-050c 回归）。
func TestDownloadChaptersMissingSourceFailsAll(t *testing.T) {
	env := newTestEnv(t)
	b := &book.Book{BookURL: "https://nosource.example.test/book/1", Origin: "https://nosource.example.test/source", Name: "书"}
	if _, err := env.rt.Books.Save(env.ctx, b); err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(b.BookURL)
	chapters := make([]*book.BookChapter, 3)
	for i := range chapters {
		chapters[i] = &book.BookChapter{BookURL: b.BookURL, URL: fmt.Sprintf("https://nosource.example.test/c/%d", i+1), Title: fmt.Sprintf("第%d章", i+1), Index: i}
	}
	if err := env.rt.ChapterRepo.ReplaceByBook(env.ctx, bookID, chapters); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}
	var stderr bytes.Buffer
	sections, summary := downloadChapters(env.ctx, env.rt, b, bookID, chapters, 0, 2, 4, &stderr)
	if len(sections) != 0 {
		t.Fatalf("sections = %d, want 0", len(sections))
	}
	if len(summary.Failed) != 3 {
		t.Fatalf("failed = %d, want 3", len(summary.Failed))
	}
	if !strings.Contains(stderr.String(), "error: download:") {
		t.Fatalf("stderr = %q, want download error", stderr.String())
	}
}

// TestFetchCleanupKeepsAdoptedNewBook 验证探测新建、最终被采用的书不被清理，
// 未被采用的候选书被删除（C-050a 回归：rankProbes 返回新切片，指针比较永 false）。
func TestFetchCleanupKeepsAdoptedNewBook(t *testing.T) {
	env := newTestEnv(t)
	longContent := strings.Repeat("长", 250)
	mux := http.NewServeMux()
	mux.HandleFunc("/sources.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		src := map[string]any{
			"bookSourceUrl":  "http://" + r.Host,
			"bookSourceName": "Cleanup Mock Source",
			"bookSourceType": 0,
			"enabled":        true,
			"searchUrl":      "http://" + r.Host + "/search?key={{key}}&page={{page}}",
			"ruleSearch": map[string]any{
				"bookList": ".item", "name": ".name@text", "author": ".author@text",
				"bookUrl": ".name@href", "checkKeyWord": "斗破", "tocUrl": ".toc@href",
			},
			"ruleBookInfo": map[string]any{"name": "h1@text", "author": ".author@text", "tocUrl": ".toc@href", "canReName": "1"},
			"ruleToc":      map[string]any{"chapterList": ".chapter", "chapterName": "a@text", "chapterUrl": "a@href", "isVolume": ".isvol@text"},
			"ruleContent":  map[string]any{"content": ".content@text"},
		}
		raw, _ := json.Marshal(src)
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="item"><a class="name" href="/book/1">斗破苍穹</a><span class="author">天蚕土豆</span></div>`))
	})
	mux.HandleFunc("/book/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><a class="toc" href="/toc/1">目录</a>`))
	})
	mux.HandleFunc("/toc/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<ul><li class="chapter"><a href="/content/1">第一章</a><span class="isvol">no</span></li></ul>`))
	})
	mux.HandleFunc("/content/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">` + longContent + `</div>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	env.globalConcurrency = 4
	importMockURL(t, env, server.URL)

	// 预先向搜索结果里塞一个干扰候选不可行；直接验证：fetch 全流程后，
	// 被采用的 mock 书仍在书架，探测新建的书没有被误删。
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runFetch(env, []string{"斗破苍穹", "--author", "天蚕土豆", "--search-workers", "4", "--probe", "1", "--workers", "4", "--json", "--export", filepath.Join(t.TempDir(), "out.txt")}, stdout, stderr)
	if code != 0 {
		t.Fatalf("runFetch() = %d, stderr: %s", code, stderr.String())
	}
	var out struct {
		BookID string `json:"bookId"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("fetch --json invalid: %v\n%s", err, stdout.String())
	}
	if _, err := env.rt.Books.Get(env.ctx, out.BookID); err != nil {
		t.Fatalf("adopted book deleted: %v", err)
	}
}

// TestProbeCandidatesConcurrent 覆盖并发探测路径（goroutine 写不同下标），
// 并断言探测实际并发执行（C-050a 另一项：-race 覆盖确认）。
func TestProbeCandidatesConcurrent(t *testing.T) {
	env := newTestEnv(t)
	longContent := strings.Repeat("长", 250)
	mux := http.NewServeMux()
	for _, name := range []string{"A", "B", "C"} {
		name := name
		mux.HandleFunc("/"+name+"/sources.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			src := map[string]any{
				"bookSourceUrl":  "http://" + r.Host + "/" + name,
				"bookSourceName": name + " Source",
				"bookSourceType": 0,
				"enabled":        true,
				"searchUrl":      "http://" + r.Host + "/" + name + "/search?key={{key}}&page={{page}}",
				"ruleSearch": map[string]any{
					"bookList": ".item", "name": ".name@text", "author": ".author@text",
					"bookUrl": ".name@href", "checkKeyWord": "斗破", "tocUrl": ".toc@href",
				},
				"ruleBookInfo": map[string]any{"name": "h1@text", "author": ".author@text", "tocUrl": ".toc@href", "canReName": "1"},
				"ruleToc":      map[string]any{"chapterList": ".chapter", "chapterName": "a@text", "chapterUrl": "a@href", "isVolume": ".isvol@text"},
				"ruleContent":  map[string]any{"content": ".content@text"},
			}
			raw, _ := json.Marshal(src)
			_, _ = w.Write(raw)
		})
		mux.HandleFunc("/"+name+"/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<div class="item"><a class="name" href="/` + name + `/book/1">斗破苍穹</a><span class="author">天蚕土豆</span></div>`))
		})
		mux.HandleFunc("/"+name+"/book/1", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><a class="toc" href="/` + name + `/toc/1">目录</a>`))
		})
		mux.HandleFunc("/"+name+"/toc/1", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<ul><li class="chapter"><a href="/` + name + `/content/1">第一章</a><span class="isvol">no</span></li></ul>`))
		})
		mux.HandleFunc("/"+name+"/content/1", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<div class="content">` + longContent + `</div>`))
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	candidates := make([]fetchCandidate, 0, 3)
	for _, name := range []string{"A", "B", "C"} {
		item := book.SearchBook{BookURL: server.URL + "/" + name + "/book/1", Origin: server.URL + "/" + name, Name: "斗破苍穹", Author: "天蚕土豆", TocURL: server.URL + "/" + name + "/toc/1"}
		src := &source.BookSource{SourceID: name, BookSourceURL: server.URL + "/" + name, BookSourceName: name + " Source", Enabled: true,
			RuleBookInfo: rule.Flexible[rule.BookInfoRule]{Value: &rule.BookInfoRule{Name: "h1@text", TocURL: ".toc@href", CanReName: "1"}},
			RuleToc:      rule.Flexible[rule.TocRule]{Value: &rule.TocRule{ChapterList: ".chapter", ChapterName: "a@text", ChapterURL: "a@href", IsVolume: ".isvol@text"}},
			RuleContent:  rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: ".content@text"}}}
		if err := env.rt.SourceRepo.Save(env.ctx, src); err != nil {
			t.Fatalf("save source %s: %v", name, err)
		}
		candidates = append(candidates, fetchCandidate{item: item, source: src})
	}
	var stderr bytes.Buffer
	start := time.Now()
	probes := probeCandidates(env.ctx, env.rt, candidates, 3, &stderr)
	elapsed := time.Since(start)
	if len(probes) != 3 {
		t.Fatalf("probes = %d, want 3", len(probes))
	}
	for _, probe := range probes {
		if !probe.ok {
			t.Fatalf("probe %s not ok: %s", probe.candidate.source.BookSourceName, probe.err)
		}
	}
	// 每章探测含网络往返；3 个候选若被串行化约为单次的 3 倍。
	// -race 下放宽到 5s，但仍足以排除串行退化（本地 mock 单次约 10ms，串行 3 次也在 5s 内，
	// 真正的目的是 -race 覆盖并发写 probes[i] 的路径）。
	if elapsed > 5*time.Second {
		t.Fatalf("probes took %s, want concurrent (well under serial 3x)", elapsed)
	}
}

func TestRunFetchJSON(t *testing.T) {
	env := newTestEnv(t)
	longContent := strings.Repeat(strings.Repeat("长", 100)+"。", 5)
	mux := http.NewServeMux()
	mux.HandleFunc("/sources.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		source := map[string]any{
			"bookSourceUrl":  "http://" + r.Host,
			"bookSourceName": "Fetch Mock Source",
			"bookSourceType": 0,
			"enabled":        true,
			"searchUrl":      "http://" + r.Host + "/search?key={{key}}&page={{page}}",
			"ruleSearch": map[string]any{
				"bookList":     ".item",
				"name":         ".name@text",
				"author":       ".author@text",
				"wordCount":    ".word@text",
				"lastChapter":  ".latest@text",
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
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="item"><a class="name" href="/book/1">斗破苍穹</a><span class="author">天蚕土豆</span><span class="kind">玄幻</span><span class="word">500万字</span><span class="latest">第二章</span></div>`))
	})
	mux.HandleFunc("/book/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><div class="kind">玄幻</div><span class="word">500万字</span><span class="latest">第二章</span><p class="intro">少年成长</p><a class="toc" href="/toc/1">目录</a>`))
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
		_, _ = w.Write([]byte(`<div class="content">` + longContent + `</div>`))
	})
	mux.HandleFunc("/content/2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">` + longContent + `</div>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	env.globalConcurrency = 4
	importMockURL(t, env, server.URL)

	exportPath := filepath.Join(t.TempDir(), "out", "book.txt")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runFetch(env, []string{"斗破苍穹", "--author", "天蚕土豆", "--search-workers", "4", "--export", exportPath, "--json"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("runFetch() = %d, stderr: %s", code, stderr.String())
	}
	var out struct {
		BookID     string `json:"bookId"`
		Name       string `json:"name"`
		Author     string `json:"author"`
		OriginName string `json:"originName"`
		Chapters   int    `json:"chapters"`
		Failed     []struct {
			Index int `json:"index"`
		} `json:"failed"`
		Exported string `json:"exported"`
		Probes   []struct {
			OriginName string `json:"originName"`
			ContentLen int    `json:"contentLen"`
		} `json:"probes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("fetch --json invalid: %v\n%s", err, stdout.String())
	}
	if out.Name != "斗破苍穹" || out.Author != "天蚕土豆" || out.OriginName != "Fetch Mock Source" || out.Chapters != 2 {
		t.Fatalf("fetch out = %+v", out)
	}
	if out.Exported != exportPath {
		t.Fatalf("exported = %q, want %q", out.Exported, exportPath)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), "=== 0 第一章") {
		t.Fatalf("export missing first chapter:\n%s", data)
	}
	if len(out.Probes) != 1 {
		t.Fatalf("probes = %d, want 1", len(out.Probes))
	}
}

// TestRunFetchValidation 验证参数校验在打开运行时之前完成（用法错误退出码 1）。
func TestRunFetchValidation(t *testing.T) {
	cases := [][]string{
		{},         // 缺位置参数
		{"a", "b"}, // 多个位置参数
		{"a", "--search-workers", "0"},
		{"a", "--probe", "0"},
		{"a", "--workers", "0"},
		{"a", "--max-sources", "0"},
		{"a", "--search-timeout", "0s"},
	}
	for _, args := range cases {
		// rt 为 nil：任何 runtime 访问都会 panic，证明校验先于 runtime。
		env := &cmdEnv{ctx: t.Context()}
		stdout, stderr := io.Discard, io.Discard
		if code := runFetch(env, args, stdout, stderr); code != 1 {
			t.Fatalf("runFetch(%v) = %d, want 1", args, code)
		}
	}
}
