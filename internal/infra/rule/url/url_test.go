package urlrule

import "testing"

func TestAbsoluteURL(t *testing.T) {
	got := Absolute("https://example.test/book/toc/index.html", "../chapter/1.html")
	if got != "https://example.test/book/chapter/1.html" {
		t.Fatalf("Absolute() = %q", got)
	}
}

func TestAbsoluteKeepsAbsoluteAndDataURL(t *testing.T) {
	if got := Absolute("https://example.test/base", "https://cdn.example.test/a.jpg"); got != "https://cdn.example.test/a.jpg" {
		t.Fatalf("absolute = %q", got)
	}
	if got := Absolute("https://example.test/base", "data:image/png;base64,abc"); got != "data:image/png;base64,abc" {
		t.Fatalf("data = %q", got)
	}
}

func TestAbsoluteRejectsJavascriptURL(t *testing.T) {
	if got := Absolute("https://example.test/base", "javascript:alert(1)"); got != "" {
		t.Fatalf("javascript = %q, want empty", got)
	}
}

func TestCustomURLAttributePreserved(t *testing.T) {
	got := ParseCustom(`chapter/1.html,{"webView":true,"method":"GET"}`)
	if got.URL != "chapter/1.html" || got.Attribute["webView"] != true || got.Attribute["method"] != "GET" {
		t.Fatalf("custom = %#v", got)
	}
	absolute := Absolute("https://example.test/book/", `chapter/1.html,{"method":"GET"}`)
	if absolute != `https://example.test/book/chapter/1.html,{"method":"GET"}` {
		t.Fatalf("absolute custom = %q", absolute)
	}
}

func TestAbsolutePreservingOptionsKeepsOriginalText(t *testing.T) {
	raw := `chapter/1.txt, {"headers": {"X-Test": "ok"}, "type": "txt"}`
	got := AbsolutePreservingOptions("https://example.test/book/", raw)
	want := `https://example.test/book/chapter/1.txt, {"headers": {"X-Test": "ok"}, "type": "txt"}`
	if got != want {
		t.Fatalf("AbsolutePreservingOptions() = %q, want %q", got, want)
	}
}

func TestCustomURLParsesLooseAttributeJSON(t *testing.T) {
	got := ParseCustom(`chapter/1.html,{method:'POST',headers:{'X-Test':'1'},body:{keyword:'斗破'},webView:“true”}`)
	if got.URL != "chapter/1.html" || got.Attribute["method"] != "POST" || got.Attribute["webView"] != "true" {
		t.Fatalf("custom = %#v", got)
	}
	headers, ok := got.Attribute["headers"].(map[string]any)
	if !ok || headers["X-Test"] != "1" {
		t.Fatalf("headers = %#v", got.Attribute["headers"])
	}
	body, ok := got.Attribute["body"].(map[string]any)
	if !ok || body["keyword"] != "斗破" {
		t.Fatalf("body = %#v", got.Attribute["body"])
	}
}

func TestCustomURLIgnoresTrailingSuffixAfterJSONObject(t *testing.T) {
	got := ParseCustom(`https://api.midureader.com/fiction/book/getDetail,{"method":"POST","body":"app=midu&book_id=229221"}##(getBrief||getDetail)选用其中之一`)
	if got.URL != "https://api.midureader.com/fiction/book/getDetail" {
		t.Fatalf("url = %q", got.URL)
	}
	if got.Attribute["method"] != "POST" || got.Attribute["body"] != "app=midu&book_id=229221" {
		t.Fatalf("attr = %#v", got.Attribute)
	}
}

func TestEncodeURIAndRequestStringsSupportCharset(t *testing.T) {
	if got := EncodeURI("民间故事", "gb2312"); got != "%C3%F1%BC%E4%B9%CA%CA%C2" {
		t.Fatalf("EncodeURI(gb2312) = %q", got)
	}
	if got := EncodeURI("中 文", "escape"); got != "%u4e2d%20%u6587" {
		t.Fatalf("EncodeURI(escape) = %q", got)
	}
	if got := EncodeURLQuery("https://example.test/search?keyword=民间故事&page=1", "gb2312"); got != "https://example.test/search?keyword=%C3%F1%BC%E4%B9%CA%CA%C2&page=1" {
		t.Fatalf("EncodeURLQuery() = %q", got)
	}
	if got := EncodeFormString("q=民间故事&searchtype=title", "gb2312"); got != "q=%C3%F1%BC%E4%B9%CA%CA%C2&searchtype=title" {
		t.Fatalf("EncodeFormString() = %q", got)
	}
}
