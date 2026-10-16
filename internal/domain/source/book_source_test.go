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

func TestParseBookSourceAppliesLegadoDefaults(t *testing.T) {
	src, err := ParseBookSource([]byte(`{"bookSourceUrl":"https://example.test","bookSourceName":"Example"}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if !src.Enabled || !src.EnabledExplore || !src.EnabledCookieJar {
		t.Fatalf("defaults = enabled:%v explore:%v cookieJar:%v, want all true", src.Enabled, src.EnabledExplore, src.EnabledCookieJar)
	}
	if src.RespondTime != DefaultRespondTime {
		t.Fatalf("RespondTime = %d, want %d", src.RespondTime, DefaultRespondTime)
	}
}

func TestParseBookSourceKeepsExplicitValues(t *testing.T) {
	src, err := ParseBookSource([]byte(`{
		"bookSourceUrl":"https://example.test",
		"bookSourceName":"Example",
		"enabled":false,
		"enabledExplore":false,
		"enabledCookieJar":false,
		"respondTime":123
	}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if src.Enabled || src.EnabledExplore || src.EnabledCookieJar {
		t.Fatalf("explicit false was overwritten: %#v", src)
	}
	if src.RespondTime != 123 {
		t.Fatalf("RespondTime = %d, want 123", src.RespondTime)
	}
}

func TestParseBookSourceTreatsNullAsMissing(t *testing.T) {
	src, err := ParseBookSource([]byte(`{
		"bookSourceUrl":"https://example.test",
		"bookSourceName":"Example",
		"enabled":null,
		"enabledCookieJar":null,
		"respondTime":null
	}`))
	if err != nil {
		t.Fatalf("ParseBookSource() error = %v", err)
	}
	if !src.Enabled || !src.EnabledCookieJar {
		t.Fatalf("null should fall back to defaults: %#v", src)
	}
	if src.RespondTime != DefaultRespondTime {
		t.Fatalf("RespondTime = %d, want %d", src.RespondTime, DefaultRespondTime)
	}
}

func TestNormalizeDefaultsRespondTime(t *testing.T) {
	src := &BookSource{BookSourceURL: "https://example.test", BookSourceName: "Example"}
	if err := src.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if src.RespondTime != DefaultRespondTime {
		t.Fatalf("RespondTime = %d, want %d", src.RespondTime, DefaultRespondTime)
	}
	if src.SourceID == "" {
		t.Fatal("SourceID is empty after Normalize")
	}
}
