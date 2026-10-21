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
		// S07-04：未闭合引号容错继续（a[href="broken]@text 引号内 ] 不参与平衡，引号未闭合
		// 不再报语法错误，按字面量消费——该用例从硬失败改为容错）。
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

// S07-04：未闭合引号（含引号内不匹配的 ]）容错继续，不再整链失败。
func TestSplitLenientQuoteWithMismatchedBracket(t *testing.T) {
	got, err := Split(`a[href="broken]@text`, "@")
	if err != nil {
		t.Fatalf("Split() error = %v, want lenient nil", err)
	}
	if len(got.Steps) == 0 || got.Steps[0].Raw != `a[href="broken]` {
		t.Fatalf("steps = %#v", got.Steps)
	}
}

// S06-11：Split 主循环对引号外 `\` 与 validateBalanced 对齐（跳过下一 rune），`a\&&b` 不切分。
func TestSplitSkipsEscapedSeparator(t *testing.T) {
	got, err := Split(`a\&&b`, "&&", "||")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if len(got.Steps) != 1 || got.Steps[0].Raw != "a\\&&b" {
		t.Fatalf("steps = %#v, want single step", got.Steps)
	}
}

// S07-04：未闭合单引号降级为字面量继续（对齐 Legado RuleAnalyzer 容错），不再硬失败。
func TestSplitLenientUnclosedQuote(t *testing.T) {
	got, err := Split(`div:contains(it's)@text`, "@")
	if err != nil {
		t.Fatalf("Split() error = %v, want lenient nil", err)
	}
	gotSteps := make([]string, 0, len(got.Steps))
	for _, step := range got.Steps {
		gotSteps = append(gotSteps, step.Raw)
	}
	want := []string{`div:contains(it's)`, "text"}
	if !reflect.DeepEqual(gotSteps, want) {
		t.Fatalf("steps = %#v, want %#v", gotSteps, want)
	}
}

// 未平衡的闭合括号仍报语法错误（`a[href="broken]@text` 引号内 ] 不参与平衡，引号未闭合）。
func TestSplitStillErrorsForUnexpectedClose(t *testing.T) {
	if _, err := Split(`div)text`, "@"); !errors.Is(err, ErrSyntax) {
		t.Fatalf("error = %v, want ErrSyntax", err)
	}
}
