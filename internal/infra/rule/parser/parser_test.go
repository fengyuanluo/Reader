package parser

import (
	"errors"
	"reflect"
	"testing"
)

func TestSplitGoldenRules(t *testing.T) {
	tests := []struct {
		name      string
		rule      string
		seps      []string
		wantSep   string
		wantSteps []string
	}{
		{
			name:      "at chain",
			rule:      "div.book@a@href",
			seps:      []string{"@"},
			wantSep:   "@",
			wantSteps: []string{"div.book", "a", "href"},
		},
		{
			name:      "and chain",
			rule:      ".item&&.name&&text",
			seps:      []string{"&&", "||", "%%"},
			wantSep:   "&&",
			wantSteps: []string{".item", ".name", "text"},
		},
		{
			name:      "or chain",
			rule:      ".missing||.fallback",
			seps:      []string{"&&", "||", "%%"},
			wantSep:   "||",
			wantSteps: []string{".missing", ".fallback"},
		},
		{
			name:      "percent chain",
			rule:      ".a%%.b%%.c",
			seps:      []string{"&&", "||", "%%"},
			wantSep:   "%%",
			wantSteps: []string{".a", ".b", ".c"},
		},
		{
			name:      "jsonpath keeps logical operators in brackets",
			rule:      "$.items[?(@.name=='a&&b'||@.id==1)]&&$.title",
			seps:      []string{"&&", "||"},
			wantSep:   "&&",
			wantSteps: []string{"$.items[?(@.name=='a&&b'||@.id==1)]", "$.title"},
		},
		{
			name:      "quoted separators stay inside step",
			rule:      `a[href="x@y"]@text`,
			seps:      []string{"@"},
			wantSep:   "@",
			wantSteps: []string{`a[href="x@y"]`, "text"},
		},
		{
			name:      "nested parentheses",
			rule:      `replace((a||b),"&&")&&text`,
			seps:      []string{"&&", "||"},
			wantSep:   "&&",
			wantSteps: []string{`replace((a||b),"&&")`, "text"},
		},
		{
			name:      "utf8 selector text is preserved",
			rule:      "text.查看更多章节@href",
			seps:      []string{"@"},
			wantSep:   "@",
			wantSteps: []string{"text.查看更多章节", "href"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Split(tt.rule, tt.seps...)
			if err != nil {
				t.Fatalf("Split() error = %v", err)
			}
			if got.Separator != tt.wantSep {
				t.Fatalf("separator = %q, want %q", got.Separator, tt.wantSep)
			}
			gotSteps := make([]string, 0, len(got.Steps))
			for _, step := range got.Steps {
				gotSteps = append(gotSteps, step.Raw)
			}
			if !reflect.DeepEqual(gotSteps, tt.wantSteps) {
				t.Fatalf("steps = %#v, want %#v", gotSteps, tt.wantSteps)
			}
		})
	}
}

func TestSplitReturnsSyntaxErrorForUnbalancedRules(t *testing.T) {
	tests := []string{
		"div[attr='x'@text",
		"$.items[?(@.name=='a']",
		`a[href="broken]@text`,
	}
	for _, rule := range tests {
		t.Run(rule, func(t *testing.T) {
			_, err := Split(rule, "@", "&&", "||", "%%")
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("error = %v, want ErrSyntax", err)
			}
		})
	}
}
