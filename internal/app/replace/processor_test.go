package replaceapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"reader/internal/domain/book"
	"reader/internal/domain/replace"
)

func TestProcessorRemovesRepeatedTitle(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{})
	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n第一段",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !result.SameTitleRemoved || result.Content != "　　第一段" {
		t.Fatalf("result = %#v", result)
	}
}

func TestProcessorAppliesTitleAndContentRules(t *testing.T) {
	repo := &fakeReplaceRepo{
		title: []*replace.ReplaceRule{{ID: 1, Pattern: "章", Replacement: "节", IsEnabled: true, ScopeTitle: true}},
		content: []*replace.ReplaceRule{
			{ID: 2, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: true, ScopeContent: true},
			{ID: 3, Name: "regex", Pattern: `坏(\d+)`, Replacement: "好$1", IsRegex: true, IsEnabled: true, ScopeContent: true},
		},
	}
	processor := NewProcessor(repo)

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:         book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter:      book.BookChapter{Title: "第一章"},
		Content:      "第一章\n广告\n坏12",
		IncludeTitle: true,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Content != "第一节\n　　好12" {
		t.Fatalf("content = %q", result.Content)
	}
	if len(result.EffectiveRuleIDs) != 2 || result.EffectiveRuleIDs[0] != 2 || result.EffectiveRuleIDs[1] != 3 {
		t.Fatalf("effective rules = %#v", result.EffectiveRuleIDs)
	}
}

// 对齐 Legado ContentProcessor.kt:160-180：单条规则正则超限时禁用该规则并继续，
// 不中止整章净化（旧实现返回错误，让一次正文请求整体失败）。
func TestProcessorDisablesRegexRuleAndContinuesWhenRegexLimitExceeded(t *testing.T) {
	rule := &replace.ReplaceRule{ID: 3, Name: "regex", Pattern: `坏(\d+)`, Replacement: "好$1", IsRegex: true, IsEnabled: true, ScopeContent: true}
	repo := &fakeReplaceRepo{
		content: []*replace.ReplaceRule{rule},
	}
	processor := NewProcessor(repo)
	large := strings.Repeat("坏123\n", 600000)

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: large,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if repo.saved == nil || repo.saved.ID != rule.ID || repo.saved.IsEnabled {
		t.Fatalf("saved rule = %#v", repo.saved)
	}
	if len(result.EffectiveRuleIDs) != 0 {
		t.Fatalf("effective rules = %#v", result.EffectiveRuleIDs)
	}
	if !strings.Contains(result.Content, "坏123") {
		t.Fatalf("content should keep original text, got %q", result.Content[:40])
	}
}

// 一条规则失败后，后续规则仍要生效（旧实现在第一条失败时就 return，整章净化被中止）。
func TestProcessorContinuesWithNextRulesAfterRuleFailure(t *testing.T) {
	// 超长正文触发第 1 条正则规则的输入上限，第 2 条普通替换规则仍应生效。
	first := &replace.ReplaceRule{ID: 1, Name: "huge", Pattern: `x`, Replacement: "y", IsRegex: true, IsEnabled: true, ScopeContent: true}
	second := &replace.ReplaceRule{ID: 2, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: true, ScopeContent: true}
	repo := &fakeReplaceRepo{content: []*replace.ReplaceRule{first, second}}
	processor := NewProcessor(repo)

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: strings.Repeat("x", maxRegexInputBytes+1) + "\n广告",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if strings.Contains(result.Content, "广告") {
		t.Fatalf("rule after the failing rule was not applied: %q", result.Content[len(result.Content)-20:])
	}
	if len(result.EffectiveRuleIDs) != 1 || result.EffectiveRuleIDs[0] != second.ID {
		t.Fatalf("effective rules = %#v", result.EffectiveRuleIDs)
	}
}

// ctx 已取消时仍要中止，避免请求取消后继续做无意义的净化。
func TestProcessorAbortsWhenContextCancelled(t *testing.T) {
	rule := &replace.ReplaceRule{ID: 3, Name: "regex", Pattern: `坏(\d+)`, Replacement: "好$1", IsRegex: true, IsEnabled: true, ScopeContent: true}
	repo := &fakeReplaceRepo{content: []*replace.ReplaceRule{rule}}
	processor := NewProcessor(repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := processor.Process(ctx, ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "坏12",
	}); !errors.Is(err, ErrRegexLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
	if repo.saved != nil {
		t.Fatalf("cancelled request must not disable rules, saved = %#v", repo.saved)
	}
}

func TestProcessorReSegmentsDialogueContent(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ReSegmentEnabled: true})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:         book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter:      book.BookChapter{Title: "第一章"},
		Content:      "第一章\n萧炎说：“你好。”药老回答：“不错。”然后两人继续前行。",
		IncludeTitle: true,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !strings.Contains(result.Content, "第一章\n　　萧炎说：“你好。”\n　　药老回答：“不错。”") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestProcessorReSegmentsDialogueWhenSpeakerCueStartsNewParagraph(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ReSegmentEnabled: true})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n“你好。”药老回答：“不错。”",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !strings.Contains(result.Content, "　　“你好。”\n　　药老回答：“不错。”") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestProcessorReSegmentsEnumerationQuotesWithoutBreakingDictionaryPhrase(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ReSegmentEnabled: true})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n“锅”、“碗”被整齐摆好。萧炎说：“开始吧。”",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if strings.Contains(result.Content, "锅”\n　　“碗") {
		t.Fatalf("content unexpectedly split enumeration quotes: %q", result.Content)
	}
	if !strings.Contains(result.Content, "　　“锅”、“碗”被整齐摆好。") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestProcessorReSegmentsLongDialogueWithTrailingQuoteSplit(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ReSegmentEnabled: true})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n“第一句。第二句。第三句。第四句。第五句。”他终于停下。",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !strings.Contains(result.Content, "第五句。”\n　　他终于停下。") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestProcessorReSegmentIsDeterministicForSameInput(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ReSegmentEnabled: true})
	input := ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n“第一句。第二句。第三句。第四句。第五句。第六句。”他说完之后又补了一句。众人这才回过神来。",
	}

	first, err := processor.Process(context.Background(), input)
	if err != nil {
		t.Fatalf("first Process() error = %v", err)
	}
	second, err := processor.Process(context.Background(), input)
	if err != nil {
		t.Fatalf("second Process() error = %v", err)
	}
	if first.Content != second.Content {
		t.Fatalf("content differs between repeated runs:\nfirst=%q\nsecond=%q", first.Content, second.Content)
	}
}

func TestProcessorConvertsTraditionalChineseWhenConfigured(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ChineseConverterType: 1})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "輕小說", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "輕小說內容",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Content != "　　轻小说内容" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestProcessorConvertsSimplifiedChineseWhenConfigured(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ChineseConverterType: 2})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "轻小说", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "轻小说内容",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Content != "　　輕小說內容" {
		t.Fatalf("content = %q", result.Content)
	}
}

type fakeReplaceRepo struct {
	title   []*replace.ReplaceRule
	content []*replace.ReplaceRule
	saved   *replace.ReplaceRule
}

func (r *fakeReplaceRepo) Save(ctx context.Context, rule *replace.ReplaceRule) error {
	copy := *rule
	r.saved = &copy
	return nil
}
func (r *fakeReplaceRepo) FindByID(ctx context.Context, id int64) (*replace.ReplaceRule, error) {
	return nil, nil
}
func (r *fakeReplaceRepo) List(ctx context.Context, filter replace.ReplaceRuleFilter) ([]*replace.ReplaceRule, int, error) {
	return nil, 0, nil
}
func (r *fakeReplaceRepo) Delete(ctx context.Context, id int64) error { return nil }
func (r *fakeReplaceRepo) FindEnabledByTitleScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return r.title, nil
}
func (r *fakeReplaceRepo) FindEnabledByContentScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return r.content, nil
}

// Processor 是进程级共享实例，繁简转换的词典懒加载必须并发安全（旧实现无锁写指针字段，-race 必报）。
func TestProcessorConvertsChineseConcurrently(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ChineseConverterType: 1})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := processor.Process(context.Background(), ProcessInput{
				Book:    book.Book{Name: "輕小說", Origin: "source"},
				Chapter: book.BookChapter{Title: "第一章"},
				Content: "輕小說內容",
			})
			if err != nil {
				t.Errorf("Process() error = %v", err)
				return
			}
			if result.Content != "　　轻小说内容" {
				t.Errorf("content = %q", result.Content)
			}
		}()
	}
	wg.Wait()
}
