package sourceapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	"reader/internal/infra/sqlite/repositories"
)

// selfReferencingFetcher 模拟恶意服务器：每次抓取都返回指向自身的 sourceUrls 文档。
type selfReferencingFetcher struct {
	url     string
	fetches atomic.Int64
}

func (f *selfReferencingFetcher) Fetch(_ context.Context, _ fetch.Request) (*fetch.Response, error) {
	f.fetches.Add(1)
	body := fmt.Sprintf(`{"sourceUrls":["%s"]}`, f.url)
	return &fetch.Response{StatusCode: 200, Body: []byte(body)}, nil
}

// TestImporterSelfReferencingSourceURLsConverges 验证自引用 sourceUrls 文档在深度上限内收敛，
// 不会无限递归抓取（S15-01/S10-02）。
func TestImporterSelfReferencingSourceURLsConverges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sourceUrls":["self"]}`))
	}))
	defer server.Close()

	fetcher := &selfReferencingFetcher{url: server.URL}
	importer := NewImporterWithFetcher(repositories.NewBookSourceRepository(openImporterTestDB(t)), fetcher)

	done := make(chan struct{})
	var result *ImportResult
	var err error
	go func() {
		defer close(done)
		result, err = importer.Import(context.Background(), ImportInput{RemoteURL: server.URL})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("import did not converge within deadline")
	}
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	// 每层展开记一次抓取：第 1 层正常抓取，后续层到达 maxRemoteDepth 后停止。
	if got := fetcher.fetches.Load(); got == 0 || got > 100 {
		t.Fatalf("fetch count = %d, want bounded", got)
	}
	if len(result.RemoteErrors) == 0 {
		t.Fatalf("RemoteErrors is empty, want depth-exceeded error")
	}
	if !strings.Contains(result.RemoteErrors[len(result.RemoteErrors)-1].Error, "recursion exceeded") {
		t.Fatalf("last remote error = %q, want recursion limit", result.RemoteErrors[len(result.RemoteErrors)-1].Error)
	}
}

// countingFetcher 记录每次抓取的 URL，可返回任意响应。
type countingFetcher struct {
	responses map[string]func() *fetch.Response
	urls      atomic.Int64
}

func (f *countingFetcher) Fetch(_ context.Context, req fetch.Request) (*fetch.Response, error) {
	f.urls.Add(1)
	gen, ok := f.responses[req.URL]
	if !ok {
		return nil, errors.New("not found")
	}
	return gen(), nil
}

// TestImporterTruncatesOversizedSourceURLsList 验证单个 sourceUrls 文档的 URL 数量超过上限时被截断（S15-01）。
func TestImporterTruncatesOversizedSourceURLsList(t *testing.T) {
	urls := make([]string, 0, maxSourceURLsPerDocument+50)
	fetcher := &countingFetcher{responses: map[string]func() *fetch.Response{}}
	for i := range maxSourceURLsPerDocument + 50 {
		u := fmt.Sprintf("https://u%d.example.test/sources.json", i)
		urls = append(urls, u)
		fetcher.responses[u] = func() *fetch.Response {
			return &fetch.Response{StatusCode: 200, Body: []byte("not-json")}
		}
	}
	payload := fmt.Sprintf(`{"sourceUrls":[%s]}`, joinQuoted(urls))
	importer := NewImporterWithFetcher(repositories.NewBookSourceRepository(openImporterTestDB(t)), fetcher)

	result, err := importer.Import(context.Background(), ImportInput{Payload: []byte(payload)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if got := fetcher.urls.Load(); got != maxSourceURLsPerDocument {
		t.Fatalf("fetch count = %d, want %d (truncated)", got, maxSourceURLsPerDocument)
	}
	if len(result.RemoteErrors) != maxSourceURLsPerDocument {
		t.Fatalf("RemoteErrors = %d, want %d", len(result.RemoteErrors), maxSourceURLsPerDocument)
	}
}

// slowFetcher 尊重 ctx 取消：每次抓取都阻塞到 ctx 结束，模拟慢站点。
type slowFetcher struct{}

func (f *slowFetcher) Fetch(ctx context.Context, _ fetch.Request) (*fetch.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestImporterRemoteDeadlineExceeded 验证整体预算：慢 URL 抓取在预算内取消，
// 剩余 URL 记 "import deadline exceeded" RemoteError，请求不会长时间挂起（S15-07）。
// 通过一个会先产生一层展开的文档，使剩余 URL 走 budget 分支。
func TestImporterRemoteDeadlineExceeded(t *testing.T) {
	// 预算为 2 分钟无法直接等待；通过已取消的 ctx 验证同一条 deadline 分支：
	// importSourceURLs 在每轮循环检查 fetchCtx.Err()，取消后剩余 URL 记 deadline 错误。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	importer := NewImporterWithFetcher(repositories.NewBookSourceRepository(openImporterTestDB(t)), &slowFetcher{})

	result, err := importer.Import(ctx, ImportInput{RemoteURL: "https://slow.example.test/a.json"})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.RemoteErrors) != 1 || !strings.Contains(result.RemoteErrors[0].Error, "import deadline exceeded") {
		t.Fatalf("RemoteErrors = %#v, want deadline exceeded", result.RemoteErrors)
	}
}

func joinQuoted(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = `"` + item + `"`
	}
	return strings.Join(quoted, ",")
}
