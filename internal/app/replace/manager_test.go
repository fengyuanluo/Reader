package replaceapp

import (
	"context"
	"errors"
	"testing"

	"reader/internal/domain/replace"
)

func TestManagerTestPlainReplace(t *testing.T) {
	got, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "foo", Replacement: "bar", IsRegex: false}, "foo foo")
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if got != "bar bar" {
		t.Fatalf("got = %q", got)
	}
}

func TestManagerTestRegexReplace(t *testing.T) {
	got, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: `foo(\d+)`, Replacement: "bar$1", IsRegex: true}, "foo12")
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if got != "bar12" {
		t.Fatalf("got = %q", got)
	}
}

func TestManagerTestInvalidRegex(t *testing.T) {
	_, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "(", IsRegex: true}, "text")
	if !errors.Is(err, replace.ErrInvalidRegex) {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerTestRegexRejectsOversizedInput(t *testing.T) {
	text := make([]byte, maxRegexInputBytes+1)
	for i := range text {
		text[i] = 'a'
	}
	_, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "a", Replacement: "b", IsRegex: true}, string(text))
	if !errors.Is(err, ErrRegexLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerTestRegexRejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Manager{}).Test(ctx, replace.ReplaceRule{Pattern: "a", Replacement: "b", IsRegex: true}, "a")
	if !errors.Is(err, ErrRegexLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
}
