package contentapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
)

func TestImageProxyFetchesAllowedImage(t *testing.T) {
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.png",
		Headers:    http.Header{"Content-Type": []string{"image/png; charset=binary"}},
		Body:       []byte{0x89, 'P', 'N', 'G'},
		StatusCode: http.StatusOK,
	}}, nil, nil, nil)

	got, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.png"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got.ContentType != "image/png" || string(got.Body) != string([]byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("result = %#v", got)
	}
}

func TestImageProxyRejectsNonImageContentType(t *testing.T) {
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/not-image",
		Headers:    http.Header{"Content-Type": []string{"text/html"}},
		Body:       []byte("<html></html>"),
		StatusCode: http.StatusOK,
	}}, nil, nil, nil)

	_, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/not-image"})
	if !errors.Is(err, ErrInvalidImageType) {
		t.Fatalf("err = %v", err)
	}
}

func TestImageProxyPropagatesFetcherSecurityErrors(t *testing.T) {
	proxy := NewImageProxy(&fakeImageFetcher{err: fetch.ErrBlocked}, nil, nil, nil)

	_, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "http://127.0.0.1/private.png"})
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestImageProxyUsesCacheBeforeFetching(t *testing.T) {
	cache := &fakeImageCache{entry: fetch.ImageCacheEntry{Body: []byte("cached"), ContentType: "image/jpeg"}, ok: true}
	fetcher := &fakeImageFetcher{}
	proxy := NewImageProxy(fetcher, nil, nil, nil).WithCache(cache)

	got, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.jpg"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(got.Body) != "cached" || got.ContentType != "image/jpeg" {
		t.Fatalf("got = %#v", got)
	}
	if fetcher.request.URL != "" {
		t.Fatalf("fetcher was called: %#v", fetcher.request)
	}
}

func TestImageProxySavesFetchedImageToCache(t *testing.T) {
	cache := &fakeImageCache{}
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.png",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte("png"),
		StatusCode: http.StatusOK,
	}}, nil, nil, nil).WithCache(cache)

	if _, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.png"}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if cache.savedURL != "https://img.example.test/cover.png|||" || string(cache.saved.Body) != "png" || cache.saved.ContentType != "image/png" {
		t.Fatalf("cache = %#v url=%q", cache.saved, cache.savedURL)
	}
}

func TestImageProxyRequiresSourceIDWhenDecodeModeIsSet(t *testing.T) {
	proxy := NewImageProxy(&fakeImageFetcher{}, nil, nil, nil)

	_, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.png", DecodeMode: DecodeModeCover})
	if !errors.Is(err, ErrImageSourceRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestImageProxyAppliesCoverDecodeJS(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "src-1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "Source",
		CoverDecodeJS:  `result.reverse()`,
	}
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.bin",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{1, 2, 3},
		StatusCode: http.StatusOK,
	}}, &fakeImageSourceRepo{byID: map[string]*source.BookSource{src.SourceID: src}}, nil, script.NewRuntime(script.Policy{}))

	got, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.bin", SourceID: src.SourceID, DecodeMode: DecodeModeCover})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(got.Body) != string([]byte{3, 2, 1}) {
		t.Fatalf("body = %v", got.Body)
	}
}

func TestImageProxyAppliesContentImageDecodeWithBookContext(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "src-1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "Source",
		RuleContent:    rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{ImageDecode: `book.getVariable("prefix").split("").map(function(ch){ return ch.charCodeAt(0); }).concat(result)`}},
	}
	bookValue := &book.Book{BookURL: "https://book.example.test/1", Variable: `{"prefix":"A"}`}
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/chapter.bin",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{1, 2},
		StatusCode: http.StatusOK,
	}}, &fakeImageSourceRepo{byID: map[string]*source.BookSource{src.SourceID: src}}, &fakeImageBookRepo{byID: map[string]*book.Book{"book-1": bookValue}}, script.NewRuntime(script.Policy{}))

	got, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/chapter.bin", SourceID: src.SourceID, BookID: "book-1", DecodeMode: DecodeModeContent})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(got.Body) != string([]byte{'A', 1, 2}) {
		t.Fatalf("body = %v", got.Body)
	}
}

func TestImageProxyReturnsSourceNotFoundForDecodeContext(t *testing.T) {
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.bin",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{1, 2, 3},
		StatusCode: http.StatusOK,
	}}, &fakeImageSourceRepo{}, nil, script.NewRuntime(script.Policy{}))

	_, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.bin", SourceID: "missing", DecodeMode: DecodeModeCover})
	if !errors.Is(err, ErrImageSourceNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// 书籍记录不存在（book.ErrNotFound）时按无 book 绑定继续解码；其他仓储错误必须上抛，不能靠错误文本判断。
func TestImageProxyToleratesMissingBookButPropagatesOtherErrors(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "src-1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "Source",
		RuleContent:    rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{ImageDecode: `result`}},
	}
	response := &fetch.Response{
		URL:        "https://img.example.test/chapter.bin",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{9, 9, 9},
		StatusCode: http.StatusOK,
	}

	missingBook := NewImageProxy(&fakeImageFetcher{response: response}, &fakeImageSourceRepo{byID: map[string]*source.BookSource{src.SourceID: src}}, &fakeImageBookRepo{}, script.NewRuntime(script.Policy{}))
	got, err := missingBook.Fetch(context.Background(), ImageProxyInput{URL: response.URL, SourceID: src.SourceID, BookID: "missing-book", DecodeMode: DecodeModeContent})
	if err != nil {
		t.Fatalf("missing book should not fail decode: %v", err)
	}
	if len(got.Body) != 3 {
		t.Fatalf("body = %v", got.Body)
	}

	brokenBook := NewImageProxy(&fakeImageFetcher{response: response}, &fakeImageSourceRepo{byID: map[string]*source.BookSource{src.SourceID: src}}, &brokenImageBookRepo{}, script.NewRuntime(script.Policy{}))
	if _, err := brokenBook.Fetch(context.Background(), ImageProxyInput{URL: response.URL, SourceID: src.SourceID, BookID: "book-1", DecodeMode: DecodeModeContent}); !errors.Is(err, errBrokenBookRepo) {
		t.Fatalf("err = %v, want repository error propagated", err)
	}
}

var errBrokenBookRepo = errors.New("book repository unavailable")

type brokenImageBookRepo struct{}

func (r *brokenImageBookRepo) Save(ctx context.Context, item *book.Book) error { return nil }
func (r *brokenImageBookRepo) FindByID(ctx context.Context, id string) (*book.Book, error) {
	return nil, errBrokenBookRepo
}
func (r *brokenImageBookRepo) FindByURL(ctx context.Context, bookURL string) (*book.Book, error) {
	return nil, errBrokenBookRepo
}
func (r *brokenImageBookRepo) List(ctx context.Context, filter book.BookFilter) ([]*book.Book, int, error) {
	return nil, 0, nil
}
func (r *brokenImageBookRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *brokenImageBookRepo) SaveProgress(ctx context.Context, id string, progress book.BookProgress) error {
	return nil
}

func TestImageProxyReturnsDecodeFailureWhenScriptErrors(t *testing.T) {
	src := &source.BookSource{
		SourceID:       "src-1",
		BookSourceURL:  "https://source.example.test",
		BookSourceName: "Source",
		CoverDecodeJS:  `throw new Error("boom")`,
	}
	proxy := NewImageProxy(&fakeImageFetcher{response: &fetch.Response{
		URL:        "https://img.example.test/cover.bin",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
		Body:       []byte{1, 2, 3},
		StatusCode: http.StatusOK,
	}}, &fakeImageSourceRepo{byID: map[string]*source.BookSource{src.SourceID: src}}, nil, script.NewRuntime(script.Policy{}))

	_, err := proxy.Fetch(context.Background(), ImageProxyInput{URL: "https://img.example.test/cover.bin", SourceID: src.SourceID, DecodeMode: DecodeModeCover})
	if !errors.Is(err, ErrImageDecodeFailed) {
		t.Fatalf("err = %v", err)
	}
}

type fakeImageFetcher struct {
	response *fetch.Response
	err      error
	request  fetch.Request
}

func (f *fakeImageFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.request = req
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

type fakeImageCache struct {
	entry    fetch.ImageCacheEntry
	ok       bool
	savedURL string
	saved    fetch.ImageCacheEntry
}

func (c *fakeImageCache) GetImage(ctx context.Context, imageURL string) (fetch.ImageCacheEntry, bool, error) {
	return c.entry, c.ok, nil
}

func (c *fakeImageCache) SaveImage(ctx context.Context, imageURL string, entry fetch.ImageCacheEntry) error {
	c.savedURL = imageURL
	c.saved = entry
	return nil
}

type fakeImageSourceRepo struct {
	byID map[string]*source.BookSource
}

func (r *fakeImageSourceRepo) Save(ctx context.Context, source *source.BookSource) error { return nil }
func (r *fakeImageSourceRepo) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	return nil
}
func (r *fakeImageSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	if src, ok := r.byID[id]; ok {
		return src, nil
	}
	return nil, source.ErrNotFound
}
func (r *fakeImageSourceRepo) FindByURL(ctx context.Context, url string) (*source.BookSource, error) {
	return nil, source.ErrNotFound
}
func (r *fakeImageSourceRepo) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	return source.Page[*source.BookSource]{}, nil
}
func (r *fakeImageSourceRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *fakeImageSourceRepo) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *fakeImageSourceRepo) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *fakeImageSourceRepo) Reorder(ctx context.Context, orders []source.SourceOrder) error {
	return nil
}

// errFakeRepoNotFound 模拟仓储层通用 NotFound 哨兵：领域哨兵 book.ErrNotFound 与之一起被包裹。
var errFakeRepoNotFound = errors.New("record not found")

type fakeImageBookRepo struct {
	byID map[string]*book.Book
}

func (r *fakeImageBookRepo) Save(ctx context.Context, item *book.Book) error { return nil }
func (r *fakeImageBookRepo) FindByID(ctx context.Context, id string) (*book.Book, error) {
	if item, ok := r.byID[id]; ok {
		return item, nil
	}
	return nil, fmt.Errorf("%w: %w", book.ErrNotFound, errFakeRepoNotFound)
}
func (r *fakeImageBookRepo) FindByURL(ctx context.Context, bookURL string) (*book.Book, error) {
	return nil, fmt.Errorf("%w: %w", book.ErrNotFound, errFakeRepoNotFound)
}
func (r *fakeImageBookRepo) List(ctx context.Context, filter book.BookFilter) ([]*book.Book, int, error) {
	return nil, 0, nil
}
func (r *fakeImageBookRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *fakeImageBookRepo) SaveProgress(ctx context.Context, id string, progress book.BookProgress) error {
	return nil
}
