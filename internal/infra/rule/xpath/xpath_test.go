package xpath

import (
	"errors"
	"reflect"
	"testing"
)

const tocHTML = `
<html><body>
  <ul id="toc">
    <li><a href="/c/1">第一章</a></li>
    <li><a href="/c/2">第二章</a></li>
  </ul>
</body></html>`

func TestAdapterExtractsTOCFixture(t *testing.T) {
	adapter, err := New(tocHTML)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	titles, err := adapter.Strings(`//ul[@id="toc"]/li/a/text()`)
	if err != nil {
		t.Fatalf("titles error = %v", err)
	}
	if !reflect.DeepEqual(titles, []string{"第一章", "第二章"}) {
		t.Fatalf("titles = %#v", titles)
	}
	urls, err := adapter.Strings(`//ul[@id="toc"]/li/a/@href`)
	if err != nil {
		t.Fatalf("urls error = %v", err)
	}
	if !reflect.DeepEqual(urls, []string{"/c/1", "/c/2"}) {
		t.Fatalf("urls = %#v", urls)
	}
}

func TestAdapterSupportsFallbackAndInterleave(t *testing.T) {
	adapter, err := New(tocHTML)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fallback, err := adapter.Strings(`//missing/text()||//ul[@id="toc"]/li/a/text()`)
	if err != nil {
		t.Fatalf("fallback error = %v", err)
	}
	if !reflect.DeepEqual(fallback, []string{"第一章", "第二章"}) {
		t.Fatalf("fallback = %#v", fallback)
	}
	interleaved, err := adapter.Strings(`//ul[@id="toc"]/li/a/text()%%//ul[@id="toc"]/li/a/@href`)
	if err != nil {
		t.Fatalf("interleaved error = %v", err)
	}
	want := []string{"第一章", "/c/1", "第二章", "/c/2"}
	if !reflect.DeepEqual(interleaved, want) {
		t.Fatalf("interleaved = %#v, want %#v", interleaved, want)
	}
}

func TestAdapterReturnsRuleParseFailedForInvalidXPath(t *testing.T) {
	adapter, err := New(tocHTML)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = adapter.Strings(`//*[`)
	if !errors.Is(err, ErrRuleParse) {
		t.Fatalf("error = %v, want ErrRuleParse", err)
	}
}
