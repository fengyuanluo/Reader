package discovery

import (
	"context"
	"fmt"
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

func TestCategoriesParsesTextExploreURL(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "s1", BookSourceURL: "https://source.example.test", BookSourceName: "S1", ExploreURL: "玄幻::/xuanhuan&&都市::/dushi", EnabledExplore: true},
	}}
	service := NewService(repo, fakeFetcher{}, nil)

	categories, err := service.Categories(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Categories() error = %v", err)
	}
	if len(categories) != 2 || categories[0].Title != "玄幻" || categories[0].URL != "/xuanhuan" {
		t.Fatalf("categories = %#v", categories)
	}
}

func TestCategoriesCachesJSExploreKinds(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "S1",
		ExploreURL:     `@js:(function(){ var n = parseInt(source.getVariable() || "0", 10) || 0; source.setVariable(String(n + 1)); return '[{"title":"玄幻","url":"/xuanhuan"}]'; })()`,
		EnabledExplore: true,
	}
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, runtime)

	first, err := service.Categories(context.Background(), "s1")
	if err != nil {
		t.Fatalf("first Categories() error = %v", err)
	}
	second, err := service.Categories(context.Background(), "s1")
	if err != nil {
		t.Fatalf("second Categories() error = %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].Title != "玄幻" || second[0].URL != "/xuanhuan" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	got := runtime.SourceBindings(context.Background(), src)["getVariable"].(func(...any) string)()
	if got != "1" {
		t.Fatalf("script count = %#v", got)
	}
}

func TestDiscoverUsesExploreRuleAndFetchesMockSource(t *testing.T) {
	src := &source.BookSource{
		SourceID:         "s1",
		BookSourceURL:    "https://source.example.test/base/",
		BookSourceName:   "S1",
		ExploreURL:       "玄幻::/list?page={page}",
		EnabledExplore:   true,
		EnabledCookieJar: true,
		Header:           `{"X-Token":"abc"}`,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: ".item",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
			CoverURL: ".cover@src",
		}},
	}
	repo := &fakeSourceRepo{items: []*source.BookSource{src}}
	lastURL := ""
	lastReq := fetch.Request{}
	fetcher := fakeFetcher{body: `<div class="item"><a class="name" href="/book/1">发现书</a><span class="author">作者</span><img class="cover" src="/cover.jpg"/></div>`, url: "https://source.example.test/list?page=2", lastURL: &lastURL, lastReq: &lastReq}
	service := NewService(repo, fetcher, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 2})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if lastURL != "https://source.example.test/list?page=2" {
		t.Fatalf("fetched URL = %q", lastURL)
	}
	if !lastReq.UseCookieJar || lastReq.Headers["X-Token"] != "abc" {
		t.Fatalf("request = %#v", lastReq)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	item := result.Items[0]
	if item.Name != "发现书" || item.Author != "作者" || item.BookURL != "https://source.example.test/book/1" || item.CoverURL != "https://source.example.test/cover.jpg" {
		t.Fatalf("item = %#v", item)
	}
}

func TestDiscoverFallsBackToSearchRule(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "https://source.example.test/list",
		EnabledExplore: true,
		RuleSearch: rule.Flexible[rule.SearchRule]{Value: &rule.SearchRule{
			BookList: ".item",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
		}},
	}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{body: `<div class="item"><a class="name" href="/book/1">回退书</a><span class="author">作者</span></div>`, url: "https://source.example.test/list"}, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "回退书" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDiscoverJSCanWriteSearchBookVariables(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "https://source.example.test/list",
		EnabledExplore: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: "$.data[*]",
			Name:     `@js:book.putVariable("bid", result.id); result.name`,
			Author:   "$.author",
			BookURL:  `https://source.example.test/book/{{java.get('bid')}}`,
		}},
	}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{
		body: `{"data":[{"id":"88","name":"发现书","author":"作者"}]}`,
		url:  "https://source.example.test/list",
	}, script.NewRuntime(script.Policy{Timeout: time.Second}))

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	if result.Items[0].BookURL != "https://source.example.test/book/88" || result.Items[0].Variable == "" || !strings.Contains(result.Items[0].Variable, `"bid":"88"`) {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDiscoverReversesBookListWhenRuleStartsWithMinus(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "https://source.example.test/list",
		EnabledExplore: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: "-.item",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
		}},
	}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{body: `
<div class="item"><a class="name" href="/book/1">第一本</a><span class="author">甲</span></div>
<div class="item"><a class="name" href="/book/2">第二本</a><span class="author">乙</span></div>`, url: "https://source.example.test/list"}, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].Name != "第二本" || result.Items[1].Name != "第一本" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDiscoverReturnsClearErrorWithoutExploreURL(t *testing.T) {
	src := &source.BookSource{SourceID: "s1", BookSourceURL: "https://source.example.test", BookSourceName: "S1", EnabledExplore: true}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "discovery_failed" || result.Warnings[0].Message != "exploreUrl is required" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestResolveSourcesSkipsDisabledExploreByDefault(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "enabled", BookSourceURL: "https://a.test", BookSourceName: "A", ExploreURL: "https://a.test/list", EnabledExplore: true},
		{SourceID: "disabled", BookSourceURL: "https://b.test", BookSourceName: "B", ExploreURL: "https://b.test/list", EnabledExplore: false},
	}}
	service := NewService(repo, fakeFetcher{}, nil)

	sources, warnings, err := service.resolveSources(context.Background(), Input{})
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(sources) != 1 || sources[0].SourceID != "enabled" {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestDiscoverAppliesLoginCheckJS(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "玄幻::/list?page={page}",
		EnabledExplore: true,
		LoginCheckJS:   `result.body.replace("old", "")`,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: ".item",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
		}},
	}
	repo := &fakeSourceRepo{items: []*source.BookSource{src}}
	fetcher := fakeFetcher{body: `old<div class="item"><a class="name" href="/book/1">发现书</a><span class="author">作者</span></div>`, url: "https://source.example.test/list?page=1"}
	service := NewService(repo, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL}))

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "发现书" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDiscoverFallsBackToInfoWhenBookListEmpty(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "https://source.example.test/list",
		EnabledExplore: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: ".missing",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
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
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{body: `<html><body><main class="detail"><h1>发现详情书</h1><span class="author">详情作者</span><p class="intro">简介</p><img class="cover" src="/cover.jpg"/><a class="toc" href="/toc/1">目录</a></main></body></html>`, url: "https://source.example.test/detail/1"}, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "发现详情书" || result.Items[0].Author != "详情作者" {
		t.Fatalf("items = %#v", result.Items)
	}
}

type fakeFetcher struct {
	body    string
	url     string
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
	if f.url == "" {
		f.url = req.URL
	}
	return &fetch.Response{URL: f.url, Body: []byte(f.body)}, nil
}

type fakeSourceRepo struct {
	items []*source.BookSource
}

func (r *fakeSourceRepo) Save(ctx context.Context, source *source.BookSource) error { return nil }
func (r *fakeSourceRepo) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	return nil
}
func (r *fakeSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	for _, item := range r.items {
		if item.SourceID == id {
			return item, nil
		}
	}
	return nil, source.ErrNotFound
}
func (r *fakeSourceRepo) FindByURL(ctx context.Context, url string) (*source.BookSource, error) {
	return nil, source.ErrNotFound
}
func (r *fakeSourceRepo) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	out := []*source.BookSource{}
	for _, item := range r.items {
		if filter.EnabledExplore != nil && *filter.EnabledExplore != item.EnabledExplore {
			continue
		}
		if filter.Group != "" && item.BookSourceGroup != filter.Group {
			continue
		}
		out = append(out, item)
	}
	return source.Page[*source.BookSource]{Items: out, Total: len(out)}, nil
}
func (r *fakeSourceRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *fakeSourceRepo) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *fakeSourceRepo) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *fakeSourceRepo) Reorder(ctx context.Context, orders []source.SourceOrder) error { return nil }

func TestBuildPageURLReplacesPageListPlaceholder(t *testing.T) {
	tests := []struct {
		page int
		want string
	}{
		{1, "https://x.test/list/1/index.html?p=4"},
		{2, "https://x.test/list/2/index.html?p=5"},
		{9, "https://x.test/list/3/index.html?p=5"},
	}
	for _, tt := range tests {
		got := buildPageURL("https://x.test/list/<1,2,3>/index.html?p=<4,5>", tt.page)
		if got != tt.want {
			t.Fatalf("buildPageURL(page=%d) = %q, want %q", tt.page, got, tt.want)
		}
	}
}

func TestCategoriesCacheExpiresAfterTTL(t *testing.T) {
	src := countingExploreSource()
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, runtime)
	now := time.Unix(1_700_000_000, 0)
	service.cache.now = func() time.Time { return now }

	if _, err := service.Categories(context.Background(), "s1"); err != nil {
		t.Fatalf("first Categories() error = %v", err)
	}
	if _, err := service.Categories(context.Background(), "s1"); err != nil {
		t.Fatalf("second Categories() error = %v", err)
	}
	if got := exploreRunCount(runtime, src); got != "1" {
		t.Fatalf("script runs = %s, want 1 (second call served from cache)", got)
	}

	now = now.Add(defaultCategoryCacheTTL + time.Minute)
	if _, err := service.Categories(context.Background(), "s1"); err != nil {
		t.Fatalf("third Categories() error = %v", err)
	}
	if got := exploreRunCount(runtime, src); got != "2" {
		t.Fatalf("script runs = %s, want 2 after TTL expiry", got)
	}
}

func TestInvalidateExploreKindsClearsCache(t *testing.T) {
	src := countingExploreSource()
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, runtime)

	if _, err := service.Categories(context.Background(), "s1"); err != nil {
		t.Fatalf("first Categories() error = %v", err)
	}
	service.InvalidateExploreKinds(src.BookSourceURL)
	if _, err := service.Categories(context.Background(), "s1"); err != nil {
		t.Fatalf("second Categories() error = %v", err)
	}
	if got := exploreRunCount(runtime, src); got != "2" {
		t.Fatalf("script runs = %s, want 2 after invalidation", got)
	}
}

func TestCategoryCacheEvictsWhenOverLimit(t *testing.T) {
	cache := newCategoryCache(time.Hour, 2)
	now := time.Unix(1_700_000_000, 0)
	cache.now = func() time.Time { return now }

	cache.put(categoryCacheKey{sourceURL: "a", exploreURL: "x"}, []Category{{Title: "A"}})
	now = now.Add(time.Second)
	cache.put(categoryCacheKey{sourceURL: "b", exploreURL: "x"}, []Category{{Title: "B"}})
	now = now.Add(time.Second)
	cache.put(categoryCacheKey{sourceURL: "c", exploreURL: "x"}, []Category{{Title: "C"}})

	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size != 2 {
		t.Fatalf("cache size = %d, want 2", size)
	}
	if _, ok := cache.get(categoryCacheKey{sourceURL: "a", exploreURL: "x"}); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := cache.get(categoryCacheKey{sourceURL: "c", exploreURL: "x"}); !ok {
		t.Fatal("newest entry should still be cached")
	}
}

func TestCategoryCacheDropsExpiredEntries(t *testing.T) {
	cache := newCategoryCache(time.Minute, 8)
	now := time.Unix(1_700_000_000, 0)
	cache.now = func() time.Time { return now }

	key := categoryCacheKey{sourceURL: "a", exploreURL: "x"}
	cache.put(key, []Category{{Title: "A"}})
	now = now.Add(2 * time.Minute)

	if _, ok := cache.get(key); ok {
		t.Fatal("expired entry should not be returned")
	}
	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size != 0 {
		t.Fatalf("cache size = %d, want 0 after expiry", size)
	}
}

func TestDiscoverRunsSourcesConcurrentlyWithLimit(t *testing.T) {
	sources := []*source.BookSource{}
	for i := range 3 {
		sources = append(sources, &source.BookSource{
			SourceID:       fmt.Sprintf("s%d", i),
			BookSourceURL:  fmt.Sprintf("https://s%d.test/base/", i),
			BookSourceName: fmt.Sprintf("S%d", i),
			ExploreURL:     "https://s.test/list",
			EnabledExplore: true,
			RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
				BookList: ".item",
				Name:     ".name@text",
				Author:   ".author@text",
				BookURL:  ".name@href",
			}},
		})
	}
	fetcher := &blockingFetcher{body: `<div class="item"><a class="name" href="/book/1">发现书</a><span class="author">作者</span></div>`}
	service := NewService(&fakeSourceRepo{items: sources}, fetcher, nil).WithLimiter(ratelimit.New(ratelimit.Options{Global: 1}))

	result, err := service.Discover(context.Background(), Input{DiscoveryURL: "https://s.test/list"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items = %#v", result.Items)
	}
	if max := fetcher.maxConcurrent(); max > 1 {
		t.Fatalf("max concurrent fetches = %d, want <= 1", max)
	}
}

func TestDiscoverSkipsInvalidSourceIDWithWarning(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "https://source.example.test/list",
		EnabledExplore: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: ".item",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
		}},
	}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{body: `<div class="item"><a class="name" href="/book/1">发现书</a><span class="author">作者</span></div>`, url: "https://source.example.test/list"}, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"missing", "s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].SourceID != "missing" || result.Warnings[0].Code != "source_unavailable" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

// countingExploreSource 返回一个每次执行 JS 都会自增 source 变量的书源，用于观察缓存是否命中。
func countingExploreSource() *source.BookSource {
	return &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "S1",
		ExploreURL:     `@js:(function(){ var n = parseInt(source.getVariable() || "0", 10) || 0; source.setVariable(String(n + 1)); return '[{"title":"玄幻","url":"/xuanhuan"}]'; })()`,
		EnabledExplore: true,
	}
}

func exploreRunCount(runtime *script.Runtime, src *source.BookSource) string {
	return runtime.SourceBindings(context.Background(), src)["getVariable"].(func(...any) string)()
}

// blockingFetcher 记录同时进行的抓取数，用于验证发现并发受限于限流器。
type blockingFetcher struct {
	body string

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
	f.mu.Lock()
	f.running--
	f.mu.Unlock()
	return &fetch.Response{URL: req.URL, Body: []byte(f.body)}, nil
}

func (f *blockingFetcher) maxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.max
}

// S14-09：`<js>` 结束检测不要求完整 </js>，从第 4 字节后最后一个 '<' 截取（对齐 Legado
// BookSourceExtensions.kt:60）；JS 失败返回 ERROR 类目，分类页仍可见错误信息而非源失败。
func TestCategoriesParsesJSScriptWithGarbageTail(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "S1",
		ExploreURL:     `<js>var r = '[{"title":"玄幻","url":"/xuanhuan"}]'; r<//garbage>`,
		EnabledExplore: true,
	}
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, runtime)

	categories, err := service.Categories(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Categories() error = %v", err)
	}
	if len(categories) != 1 || categories[0].Title != "玄幻" || categories[0].URL != "/xuanhuan" {
		t.Fatalf("categories = %#v", categories)
	}
}

// S14-09：JS 抛错的源 Categories 返回非空 ERROR 类目。
func TestCategoriesReturnsErrorCategoryWhenJSFails(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "S1",
		ExploreURL:     `@js:throw new Error("boom")`,
		EnabledExplore: true,
	}
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, runtime)

	categories, err := service.Categories(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Categories() error = %v, want nil (ERROR category instead)", err)
	}
	if len(categories) != 1 || !strings.HasPrefix(categories[0].Title, "ERROR:") {
		t.Fatalf("categories = %#v, want ERROR category", categories)
	}
}

// S14-10：URL 为空的类目不充当 URL，不把中文类目名当 URL 发起 fetch。
func TestDiscoverDoesNotUseCategoryTitleAsURL(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "玄幻::https://source.example.test/list",
		EnabledExplore: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: ".item",
			Name:     ".name@text",
			Author:   ".author@text",
			BookURL:  ".name@href",
		}},
	}
	lastURL := ""
	fetcher := fakeFetcher{body: `<div class="item"><a class="name" href="/book/1">发现书</a><span class="author">作者</span></div>`, url: "https://source.example.test/list", lastURL: &lastURL}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fetcher, nil)

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if lastURL != "https://source.example.test/list" {
		t.Fatalf("fetched URL = %q, want category URL", lastURL)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
}

// S14-10：JS 类目并发首次调用单飞——同一脚本只执行一次（对齐 Legado per-source Mutex）。
func TestCategoriesSingleFlightsJSConcurrentCalls(t *testing.T) {
	src := countingExploreSource()
	runtime := script.NewRuntime(script.Policy{Timeout: time.Second, SourceKey: src.BookSourceURL})
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{}, runtime)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Categories(context.Background(), "s1"); err != nil {
				t.Errorf("Categories() error = %v", err)
			}
		}()
	}
	wg.Wait()
	if got := exploreRunCount(runtime, src); got != "1" {
		t.Fatalf("script runs = %s, want 1 (single-flight)", got)
	}
}

// S14-07：parseBookList 复用单个 itemAnalyzer，逐元素重绑定书籍；JS 规则的 book 绑定
// 与基线变量恢复仍要正确（旧实现在变量规则下逐元素新建 Analyzer）。
func TestDiscoverRebindsBookVariablesPerElement(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://source.example.test/base/",
		BookSourceName: "S1",
		ExploreURL:     "https://source.example.test/list",
		EnabledExplore: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: "$.data[*]",
			Name:     `@js:book.putVariable("bid", result.id); result.name`,
			Author:   "$.author",
			BookURL:  `https://source.example.test/book/{{java.get('bid')}}`,
		}},
	}
	service := NewService(&fakeSourceRepo{items: []*source.BookSource{src}}, fakeFetcher{
		body: `{"data":[{"id":"88","name":"发现书","author":"作者"},{"id":"99","name":"第二书","author":"作者2"}]}`,
		url:  "https://source.example.test/list",
	}, script.NewRuntime(script.Policy{Timeout: time.Second}))

	result, err := service.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %#v", result.Items)
	}
	if result.Items[0].BookURL != "https://source.example.test/book/88" || result.Items[1].BookURL != "https://source.example.test/book/99" {
		t.Fatalf("book variable not rebound per element: %#v", result.Items)
	}
}
