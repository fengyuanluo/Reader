package replaceapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reader/internal/domain/book"
	"reader/internal/domain/replace"
)

// classifyRegexFailure 区分三种限流类错误，超时与输入超大/取消互不混淆（S14-01）。
func TestClassifyRegexFailureDistinguishesTimeout(t *testing.T) {
	input := strings.Repeat("a", 4096) + strings.Repeat("bc", 2048) + "c"
	timeoutErr := errors.Join(ErrRegexTimeout, ErrRegexLimitExceeded)
	bigErr := errors.Join(ErrRegexInputTooLarge, ErrRegexLimitExceeded)
	cancelErr := errors.Join(ErrRegexCancelled, ErrRegexLimitExceeded)
	otherErr := errors.New("replace regex is invalid")

	if got := classifyRegexFailure(timeoutErr); got != regexFailureTimeout {
		t.Fatalf("timeout class = %v, want %v", got, regexFailureTimeout)
	}
	if got := classifyRegexFailure(bigErr); got != regexFailureInputTooLarge {
		t.Fatalf("input-too-large class = %v, want %v", got, regexFailureInputTooLarge)
	}
	if got := classifyRegexFailure(cancelErr); got != regexFailureCancelled {
		t.Fatalf("cancelled class = %v, want %v", got, regexFailureCancelled)
	}
	if got := classifyRegexFailure(otherErr); got != regexFailureOther {
		t.Fatalf("other class = %v, want %v", got, regexFailureOther)
	}
	_ = input
}

// 仅真正则超时才禁用并持久化规则；输入超大与取消均不得触碰规则状态（S14-01）。
func TestProcessorDisablesRuleOnlyOnTimeout(t *testing.T) {
	slow := &replace.ReplaceRule{ID: 1, Name: "slow", Pattern: `(a+)\1b`, Replacement: "x", IsRegex: true, IsEnabled: true, ScopeContent: true}
	big := &replace.ReplaceRule{ID: 2, Name: "big", Pattern: `x`, Replacement: "y", IsRegex: true, IsEnabled: true, ScopeContent: true}
	repo := &fakeReplaceRepo{content: []*replace.ReplaceRule{slow, big}}
	processor := NewProcessor(repo)

	// 输入超大：规则保持启用。
	if _, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "书", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: strings.Repeat("x", maxRegexInputBytes+1) + "\n正文",
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if repo.saved != nil {
		t.Fatalf("oversized input must not disable rules, saved = %#v", repo.saved)
	}

	// 正则超时（对齐 Legado RegexTimeoutException）：禁用并持久化。
	processor.handleRuleFailure(context.Background(), slow, errors.Join(ErrRegexTimeout, ErrRegexLimitExceeded))
	if repo.saved == nil || repo.saved.ID != slow.ID || repo.saved.IsEnabled {
		t.Fatalf("timeout should disable rule, saved = %#v", repo.saved)
	}

	// 取消（对齐 Legado CancellationException）：禁用后规则不得再次被触碰。
	enabled := &replace.ReplaceRule{ID: 3, Name: "c", Pattern: `坏(\d+)`, Replacement: "好$1", IsRegex: true, IsEnabled: true, ScopeContent: true}
	repo2 := &fakeReplaceRepo{content: []*replace.ReplaceRule{enabled}}
	processor2 := NewProcessor(repo2)
	processor2.handleRuleFailure(context.Background(), enabled, errors.Join(ErrRegexCancelled, ErrRegexLimitExceeded))
	if repo2.saved != nil {
		t.Fatalf("cancelled must not disable rules, saved = %#v", repo2.saved)
	}
}

// S14-05：被禁用的规则在下一次 Process 不再参与替换（不重放、不再超时）。
func TestProcessorSkipsDisabledRule(t *testing.T) {
	repo := &fakeReplaceRepo{
		content: []*replace.ReplaceRule{
			{ID: 1, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: false, ScopeContent: true},
			{ID: 2, Name: "keep", Pattern: "正文", Replacement: "故事", IsEnabled: true, ScopeContent: true},
		},
	}
	processor := NewProcessor(repo)
	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "书", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n广告正文",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	// “广告”原样保留证明禁用规则未参与替换；“正文→故事”证明启用规则仍生效。
	if !strings.Contains(result.Content, "广告") {
		t.Fatalf("disabled rule unexpectedly ran: %q", result.Content)
	}
	if !strings.Contains(result.Content, "故事") || len(result.EffectiveRuleIDs) != 1 || result.EffectiveRuleIDs[0] != 2 {
		t.Fatalf("enabled rule should still apply: %#v", result)
	}
}

// S14-06：IncludeTitle=true 时标题与正文使用同一简繁转换（对齐 Legado getDisplayTitle 默认 chineseConvert=true）。
func TestProcessorConvertsTitleChineseWhenIncludeTitle(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ChineseConverterType: 1})
	result, err := processor.Process(context.Background(), ProcessInput{
		Book:         book.Book{Name: "輕小說", Origin: "source"},
		Chapter:      book.BookChapter{Title: "第一章"},
		Content:      "第一章\n輕小說內容",
		IncludeTitle: true,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !strings.HasPrefix(result.Content, "第一章\n") {
		t.Fatalf("content = %q, want converted title kept", result.Content)
	}
}

// S14-06：IncludeTitle=false 时跳过标题域规则处理（对齐 Legado includeTitle=false 完全跳过标题）。
func TestProcessorSkipsTitleRulesWhenIncludeTitleFalse(t *testing.T) {
	repo := &fakeReplaceRepo{
		title: []*replace.ReplaceRule{{ID: 1, Pattern: "章", Replacement: "节", IsEnabled: true, ScopeTitle: true}},
	}
	processor := NewProcessor(repo)
	result, err := processor.Process(context.Background(), ProcessInput{
		Book:         book.Book{Name: "书", Origin: "source"},
		Chapter:      book.BookChapter{Title: "第一章"},
		Content:      "第一章\n正文",
		IncludeTitle: false,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if strings.Contains(result.Content, "第一节") {
		t.Fatalf("title rules must be skipped when IncludeTitle=false: %q", result.Content)
	}
}

// S14-12：replaceText 的编译检查走共享 LRU；空 pattern 与非法正则仍要被拒绝（不放宽校验）。
func TestReplaceTextStillValidatesPatterns(t *testing.T) {
	// 空 pattern。
	if _, err := replaceText(context.Background(), replace.ReplaceRule{Name: "empty", IsRegex: true}, "x"); !errors.Is(err, replace.ErrEmptyPattern) {
		t.Fatalf("empty pattern err = %v, want ErrEmptyPattern", err)
	}
	// 非法正则。
	if _, err := replaceText(context.Background(), replace.ReplaceRule{Name: "bad", Pattern: "(", IsRegex: true}, "x"); !errors.Is(err, replace.ErrInvalidRegex) {
		t.Fatalf("invalid regex err = %v, want ErrInvalidRegex", err)
	}
	// 平文本空 pattern。
	if _, err := replaceText(context.Background(), replace.ReplaceRule{Name: "empty-plain"}, "x"); !errors.Is(err, replace.ErrEmptyPattern) {
		t.Fatalf("empty plain pattern err = %v, want ErrEmptyPattern", err)
	}
}
