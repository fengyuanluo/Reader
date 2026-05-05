package regex

import (
	"errors"
	"reflect"
	"testing"
)

func TestAdapterExtractsRegexElementAndElements(t *testing.T) {
	adapter := New(0)
	input := `<a href="/b/1">第一本</a><a href="/b/2">第二本</a>`

	element, err := adapter.Element(input, `<a href="([^"]+)">([^<]+)</a>`)
	if err != nil {
		t.Fatalf("Element() error = %v", err)
	}
	if !reflect.DeepEqual(element, []string{`<a href="/b/1">第一本</a>`, "/b/1", "第一本"}) {
		t.Fatalf("element = %#v", element)
	}

	elements, err := adapter.Elements(input, `<a href="([^"]+)">([^<]+)</a>`)
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	if len(elements) != 2 || elements[1][1] != "/b/2" || elements[1][2] != "第二本" {
		t.Fatalf("elements = %#v", elements)
	}
}

func TestAdapterSupportsProgressivePatterns(t *testing.T) {
	adapter := New(0)
	input := `<div class="list"><a href="/b/1">第一本</a></div><p>ignore</p>`
	got, err := adapter.Element(input, `<div class="list">.*?</div>`, `<a href="([^"]+)">([^<]+)</a>`)
	if err != nil {
		t.Fatalf("Element() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{`<a href="/b/1">第一本</a>`, "/b/1", "第一本"}) {
		t.Fatalf("element = %#v", got)
	}
}

func TestReplaceSupportsAllAndFirst(t *testing.T) {
	all, err := Replace("a1 b2", `\d`, "X", false)
	if err != nil {
		t.Fatalf("Replace all error = %v", err)
	}
	if all != "aX bX" {
		t.Fatalf("all = %q", all)
	}
	first, err := Replace("a1 b2", `\d`, "X", true)
	if err != nil {
		t.Fatalf("Replace first error = %v", err)
	}
	if first != "X" {
		t.Fatalf("first = %q", first)
	}
}

func TestReplaceSupportsJavaStyleLookaheadFallback(t *testing.T) {
	got, err := Replace("00123", `^0+\s*(\d+)(?!\s*章)`, `$1章`, false)
	if err != nil {
		t.Fatalf("Replace error = %v", err)
	}
	if got != "123章" {
		t.Fatalf("got = %q", got)
	}
	unchanged, err := Replace("第二章 斗气大陆", `^0+\s*(\d+)(?!\s*章)`, `$1章`, false)
	if err != nil {
		t.Fatalf("Replace unmatched error = %v", err)
	}
	if unchanged != "第二章 斗气大陆" {
		t.Fatalf("unchanged = %q", unchanged)
	}
}

func TestAdapterReturnsInvalidPattern(t *testing.T) {
	adapter := New(0)
	_, err := adapter.Element("x", `[`)
	if !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("error = %v, want ErrInvalidPattern", err)
	}
}
