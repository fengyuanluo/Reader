package jsonpath

import (
	"errors"
	"reflect"
	"testing"
)

const searchJSON = `{
  "books": [
    {"name":"第一本","author":"作者甲","url":"/b/1"},
    {"name":"第二本","author":"作者乙","url":"/b/2"}
  ]
}`

func TestAdapterExtractsSearchFixture(t *testing.T) {
	adapter, err := New(searchJSON)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	names, err := adapter.Strings("$.books[*].name")
	if err != nil {
		t.Fatalf("names error = %v", err)
	}
	if !reflect.DeepEqual(names, []string{"第一本", "第二本"}) {
		t.Fatalf("names = %#v", names)
	}
	authors, err := adapter.Strings("$.books[*].author")
	if err != nil {
		t.Fatalf("authors error = %v", err)
	}
	if !reflect.DeepEqual(authors, []string{"作者甲", "作者乙"}) {
		t.Fatalf("authors = %#v", authors)
	}
	urls, err := adapter.Strings("$.books[*].url")
	if err != nil {
		t.Fatalf("urls error = %v", err)
	}
	if !reflect.DeepEqual(urls, []string{"/b/1", "/b/2"}) {
		t.Fatalf("urls = %#v", urls)
	}
}

func TestAdapterSupportsFallbackAndInterleave(t *testing.T) {
	adapter, err := New(searchJSON)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fallback, err := adapter.Strings("$.missing[*].name||$.books[*].name")
	if err != nil {
		t.Fatalf("fallback error = %v", err)
	}
	if !reflect.DeepEqual(fallback, []string{"第一本", "第二本"}) {
		t.Fatalf("fallback = %#v", fallback)
	}
	interleaved, err := adapter.Strings("$.books[*].name%%$.books[*].author")
	if err != nil {
		t.Fatalf("interleaved error = %v", err)
	}
	want := []string{"第一本", "作者甲", "第二本", "作者乙"}
	if !reflect.DeepEqual(interleaved, want) {
		t.Fatalf("interleaved = %#v, want %#v", interleaved, want)
	}
}

func TestAdapterAcceptsLegacyRootArrayWildcard(t *testing.T) {
	adapter, err := New(`[{"name":"第一本"},{"name":"第二本"}]`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.Strings("$.[*].name")
	if err != nil {
		t.Fatalf("Strings() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"第一本", "第二本"}) {
		t.Fatalf("got = %#v", got)
	}
}

func TestAdapterTreatsScalarTraversalAsEmptyResult(t *testing.T) {
	adapter, err := New(`{"code":1,"msg":"请输入搜索关键字","data":""}`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.Strings("$.data.search")
	if err != nil {
		t.Fatalf("Strings() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %#v, want empty", got)
	}
}

func TestAdapterReturnsInvalidJSON(t *testing.T) {
	_, err := New(`{"books":`)
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("error = %v, want ErrInvalidJSON", err)
	}
}

func TestAdapterReturnsRuleParseFailedForInvalidJSONPath(t *testing.T) {
	adapter, err := New(searchJSON)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = adapter.Strings("$.books[")
	if !errors.Is(err, ErrRuleParse) {
		t.Fatalf("error = %v, want ErrRuleParse", err)
	}
}

// R-011：在数值/布尔/字符串标量上继续取子路径时容错返回空，而不是 rule_parse_failed。
func TestAdapterScalarTraversalReturnsEmpty(t *testing.T) {
	adapter, err := New(`{"x":1,"b":true,"s":"text","n":null}`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, rule := range []string{"$.x.a", "$.b.a", "$.s.a"} {
		t.Run(rule, func(t *testing.T) {
			got, err := adapter.Strings(rule)
			if err != nil {
				t.Fatalf("Strings(%q) error = %v", rule, err)
			}
			if len(got) != 0 {
				t.Fatalf("Strings(%q) = %#v, want empty", rule, got)
			}
		})
	}
}

// R-012：过滤表达式支持比较与逻辑运算符。
func TestAdapterSupportsFilterOperators(t *testing.T) {
	adapter, err := New(`{"list":[{"n":1,"t":"a","title":"一"},{"n":2,"t":"b","title":"二"},{"n":3,"t":"c","title":"三"}]}`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		rule string
		want []string
	}{
		{rule: `$.list[?(@.n>1)].title`, want: []string{"二", "三"}},
		{rule: `$.list[?(@.n<2)].title`, want: []string{"一"}},
		{rule: `$.list[?(@.n>=2)].title`, want: []string{"二", "三"}},
		{rule: `$.list[?(@.n<=2)].title`, want: []string{"一", "二"}},
		{rule: `$.list[?(@.n==1 || @.t=="c")].title`, want: []string{"一", "三"}},
		{rule: `$.list[?(@.n>1 && @.t!="c")].title`, want: []string{"二"}},
		{rule: `$.list[?(@.t=="b")].title`, want: []string{"二"}},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			got, err := adapter.Strings(tt.rule)
			if err != nil {
				t.Fatalf("Strings() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Strings() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
