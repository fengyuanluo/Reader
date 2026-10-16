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

	req, err := buildBookFetchRequest(context.Background(), `https://site.example.test/api,{"headers":{"X-Count":3,"X-Flag":true,"X-Text":"v"}}`, src, nil, nil)
	if err != nil {
		t.Fatalf("buildBookFetchRequest() error = %v", err)
	}
	if req.Headers["X-Count"] != "3" || req.Headers["X-Flag"] != "true" || req.Headers["X-Text"] != "v" {
		t.Fatalf("headers = %#v", req.Headers)
	}
}

// 对齐 Legado AnalyzeUrl.kt:262-283：表单正文只按 charset 编码一次；旧实现先在 body 分支按空 charset
// 编码一次，再到末尾按解析出的 charset 编码一次，含中文的正文被二次 percent 编码，服务端解析失败。
func TestBuildBookFetchRequestEncodesFormBodyOnce(t *testing.T) {
	req, err := buildBookFetchRequest(context.Background(), `https://site.example.test/api,{"method":"POST","body":"a=1&b=中文"}`, &source.BookSource{SourceID: "s1", BookSourceURL: "https://site.example.test/source"}, nil, nil)
	if err != nil {
		t.Fatalf("buildBookFetchRequest() error = %v", err)
	}
	if got := string(req.Body); got != "a=1&b=%E4%B8%AD%E6%96%87" {
		t.Fatalf("body = %q, want %q", got, "a=1&b=%E4%B8%AD%E6%96%87")
	}
	if req.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q", req.Headers["Content-Type"])
	}
}

// 显式设置了 Content-Type 时正文按原样发送（Legado: post(body, contentType)），不再做表单编码。
func TestBuildBookFetchRequestKeepsBodyWhenContentTypeSet(t *testing.T) {
	req, err := buildBookFetchRequest(context.Background(), `https://site.example.test/api,{"method":"POST","headers":{"Content-Type":"text/plain; charset=utf-8"},"body":"a=1&b=中文"}`, &source.BookSource{SourceID: "s1", BookSourceURL: "https://site.example.test/source"}, nil, nil)
	if err != nil {
		t.Fatalf("buildBookFetchRequest() error = %v", err)
	}
	if got := string(req.Body); got != "a=1&b=中文" {
		t.Fatalf("body = %q, want 原样正文", got)
	}
	if req.Headers["Content-Type"] != "text/plain; charset=utf-8" {
		t.Fatalf("content-type = %q", req.Headers["Content-Type"])
	}
}
