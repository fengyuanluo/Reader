package replace

import (
	"errors"
	"testing"
)

func TestReplaceRuleValidateRejectsEmptyPattern(t *testing.T) {
	rule := ReplaceRule{}
	if err := rule.Validate(); !errors.Is(err, ErrEmptyPattern) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrEmptyPattern)
	}
}

func TestReplaceRuleValidateRejectsInvalidRegex(t *testing.T) {
	rule := ReplaceRule{Pattern: "(", IsRegex: true}
	if err := rule.Validate(); !errors.Is(err, ErrInvalidRegex) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidRegex)
	}
}

func TestReplaceRuleValidateRejectsTrailingRegexPipe(t *testing.T) {
	rule := ReplaceRule{Pattern: "foo|", IsRegex: true}
	if err := rule.Validate(); !errors.Is(err, ErrInvalidRegex) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidRegex)
	}
}

func TestReplaceRuleValidateAllowsPlainString(t *testing.T) {
	rule := ReplaceRule{Pattern: "(", IsRegex: false}
	if err := rule.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestReplaceRuleValidTimeoutMillisecondUsesDefault(t *testing.T) {
	rule := ReplaceRule{TimeoutMillisecond: 0}
	if got := rule.ValidTimeoutMillisecond(); got != DefaultTimeoutMillisecond {
		t.Fatalf("ValidTimeoutMillisecond() = %d, want %d", got, DefaultTimeoutMillisecond)
	}
}
