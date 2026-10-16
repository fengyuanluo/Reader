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

// R-064：对齐 Legado AnalyzeRule.replaceRegex 的无匹配与非法正则语义。
func TestReplaceMatchesLegadoReplaceRegexSemantics(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		pattern     string
		replacement string
		firstOnly   bool
		want        string
	}{
		{name: "first no match returns empty", input: "普通标题", pattern: `【.*】`, firstOnly: true, want: ""},
		{name: "first returns replaced first match", input: "前【卷一】后【卷二】", pattern: `【(.*?)】`, replacement: "$1", firstOnly: true, want: "卷一"},
		{name: "first with regexp2 no match returns empty", input: "abc", pattern: `x(?=y)`, firstOnly: true, want: ""},
		{name: "all no match keeps input", input: "普通标题", pattern: `【.*】`, want: "普通标题"},
		{name: "invalid regex all falls back to literal", input: "a(b(c", pattern: `(`, replacement: "-", want: "a-b-c"},
		{name: "invalid regex first returns replacement", input: "a(b", pattern: `(`, replacement: "R", firstOnly: true, want: "R"},
		// Legado 在匹配片段上再次 replaceFirst，环视上下文不在片段内时片段保持原样。
		{name: "first lookaround keeps matched fragment like Legado", input: "前缀hello世界后缀", pattern: `(?<=前缀)hello(?=世界)`, replacement: "REPL", firstOnly: true, want: "hello"},
		{name: "all lookaround replaces in context", input: "前缀hello世界后缀", pattern: `(?<=前缀)hello(?=世界)`, replacement: "REPL", want: "前缀REPL世界后缀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Replace(tt.input, tt.pattern, tt.replacement, tt.firstOnly)
			if err != nil {
				t.Fatalf("Replace() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Replace() = %q, want %q", got, tt.want)
			}
		})
	}
}

// R-085：同一 pattern 复用编译结果（含非法 pattern 的失败结果）。
func TestCompilePatternIsCached(t *testing.T) {
	first, err := compilePattern(`cache-\d+`)
	if err != nil {
		t.Fatalf("compilePattern() error = %v", err)
	}
	second, _ := compilePattern(`cache-\d+`)
	if first.std == nil || first.std != second.std {
		t.Fatalf("compiled pattern not reused: %p vs %p", first.std, second.std)
	}
	lookaround, _ := compilePattern(`cache(?=x)`)
	again, _ := compilePattern(`cache(?=x)`)
	if lookaround.re2 == nil || lookaround.re2 != again.re2 {
		t.Fatal("regexp2 pattern not reused")
	}
	if _, err := compilePattern(`cache-[`); !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("invalid error = %v", err)
	}
	if matched, err := MatchString(`cha?pter`, "chpter-1"); err != nil || !matched {
		t.Fatalf("MatchString() = %v, %v", matched, err)
	}
}
