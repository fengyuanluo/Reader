package discovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
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

	sources, err := service.resolveSources(context.Background(), Input{})
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
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
		LoginCheckJS:   `result.replace("old", "")`,
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
