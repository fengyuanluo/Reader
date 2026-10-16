package bookapp

import (
	"context"
	"testing"

	"reader/internal/domain/source"
)

// 对齐 Legado AnalyzeUrl.kt:235-237 entry.value.toString()：URL 选项 headers 里的数字、布尔值
// 也要转成字符串发出去，不能因为不是 string 就静默丢弃。
func TestBuildBookFetchRequestWidensNonStringHeaderValues(t *testing.T) {
	src := &source.BookSource{SourceID: "s1", BookSourceURL: "https://site.example.test/source", BookSourceName: "Source"}

	req, err := buildBookFetchRequest(context.Background(), `https://site.example.test/api,{"headers":{"X-Count":3,"X-Flag":true,"X-Text":"v"}}`, src, nil)
	if err != nil {
		t.Fatalf("buildBookFetchRequest() error = %v", err)
	}
	if req.Headers["X-Count"] != "3" || req.Headers["X-Flag"] != "true" || req.Headers["X-Text"] != "v" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}
