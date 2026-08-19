package source

import (
	"errors"
	"testing"
)

func TestParseBookSourceAcceptsRuleObject(t *testing.T) {
	src, err := ParseBookSource([]byte(`{
		"bookSourceUrl":"https://example.test",
		"bookSourceName":"Example",
		"ruleSearch":{"bookList":".item","name":".name@text"},
		"ruleToc":{"chapterList":".chapter"}
	}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if src.SourceID == "" {
		t.Fatal("SourceID is empty")
	}
	if src.RuleSearch.Value == nil || src.RuleSearch.Value.BookList != ".item" {
		t.Fatalf("RuleSearch = %#v", src.RuleSearch.Value)
	}
	if src.RuleToc.Value == nil || src.RuleToc.Value.ChapterList != ".chapter" {
		t.Fatalf("RuleToc = %#v", src.RuleToc.Value)
	}
}

func TestParseBookSourceAcceptsRuleString(t *testing.T) {
	src, err := ParseBookSource([]byte(`{
		"bookSourceUrl":"https://example.test",
		"bookSourceName":"Example",
		"ruleSearch":"{\"bookList\":\".item\",\"name\":\".name@text\"}"
	}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if src.RuleSearch.Value == nil || src.RuleSearch.Value.Name != ".name@text" {
		t.Fatalf("RuleSearch = %#v", src.RuleSearch.Value)
	}
}

func TestParseBookSourceAppliesLegadoDefaultsOnlyWhenMissing(t *testing.T) {
	src, err := ParseBookSource([]byte(`{"bookSourceUrl":"https://example.test","bookSourceName":"Example"}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if !src.Enabled || !src.EnabledExplore || !src.EnabledCookieJar || src.RespondTime != 180000 {
		t.Fatalf("defaults = %#v", src)
	}

	explicit, err := ParseBookSource([]byte(`{"bookSourceUrl":"https://example.test","bookSourceName":"Example","enabled":false,"enabledExplore":false,"enabledCookieJar":false,"respondTime":0}`))
	if err != nil {
		t.Fatalf("ParseBookSource(explicit) error = %v", err)
	}
	if explicit.Enabled || explicit.EnabledExplore || explicit.EnabledCookieJar || explicit.RespondTime != 0 {
		t.Fatalf("explicit values = %#v", explicit)
	}
}

func TestParseBookSourcePreservesCurrentLegadoCompatibilityFields(t *testing.T) {
	src, err := ParseBookSource([]byte(`{
		"bookSourceUrl":"https://example.test",
		"bookSourceName":"Example",
		"eventListener":true,
		"customButton":true,
		"ruleContent":{"content":".content@text","subContent":".note@text","callBackJs":"result"}
	}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if !src.EventListener || !src.CustomButton {
		t.Fatalf("source fields = %#v", src)
	}
	if src.RuleContent.Value == nil || src.RuleContent.Value.SubContent != ".note@text" || src.RuleContent.Value.CallBackJS != "result" {
		t.Fatalf("content rule = %#v", src.RuleContent.Value)
	}
}

func TestBookSourceValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		json string
		want error
	}{
		{name: "missing url", json: `{"bookSourceName":"Example"}`, want: ErrMissingBookSourceURL},
		{name: "missing name", json: `{"bookSourceUrl":"https://example.test"}`, want: ErrMissingBookSourceName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBookSource([]byte(tt.json))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseBookSource() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSourceIDFromURLIsStable(t *testing.T) {
	first := SourceIDFromURL("https://example.test")
	second := SourceIDFromURL("https://example.test")
	other := SourceIDFromURL("https://other.example.test")
	if first != second {
		t.Fatalf("SourceIDFromURL not stable: %s != %s", first, second)
	}
	if first == other {
		t.Fatal("different URLs produced same source id")
	}
}
