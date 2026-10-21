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
// S14-01：输入超过 maxRegexInputBytes 与规则合法性无关，不得永久禁用规则
// （旧实现把它折叠加进 ErrRegexLimitExceeded 后统一禁用并持久化）。
func TestProcessorDoesNotDisableRuleForOversizedInput(t *testing.T) {
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
	if repo.saved != nil {
		t.Fatalf("oversized input must not disable rules, saved = %#v", repo.saved)
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
	// 对齐 Legado ContentHelp.kt：Go 版不再额外做 QuoteNarrationCut/QuoteAfterNarrator 切分（S14-04），
	// 对话断行仅来自 findNewLines 的引号配对逻辑。
	if !strings.Contains(result.Content, "药老回答：“不错。”\n　　然后两人继续前行。") {
		t.Fatalf("content = %q", result.Content)
	}
	if strings.Contains(result.Content, "“你好。”\n") {
		t.Fatalf("content unexpectedly split narration: %q", result.Content)
	}
}

func TestProcessorReSegmentsDialogueWhenSpeakerCueStartsNewParagraph(t *testing.T) {
	processor := NewProcessor(&fakeReplaceRepo{}).WithOptions(ProcessorOptions{ReSegmentEnabled: true})

	result, err := processor.Process(context.Background(), ProcessInput{
		Book:    book.Book{Name: "斗破苍穹", Origin: "source"},
		Chapter: book.BookChapter{Title: "第一章"},
		Content: "第一章\n“你好。”“不错。”",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	// 对齐 Legado ContentHelp.kt:58 预分段：相邻引号对被拆为 ”\n“。
	if !strings.Contains(result.Content, "　　“你好。”\n　　“不错。”") {
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
		Content: "第一章\n“第一句。第二句。第三句。第四句。第五句。”他终于停下。\n“下一句。”“再下一句。”",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	// 对齐 Legado：相邻引号对在预分段即拆行；不在叙述语后强插段（S14-04 已删除该变换）。
	if !strings.Contains(result.Content, "“下一句。”\n　　“再下一句。”") {
		t.Fatalf("content = %q", result.Content)
	}
	if strings.Contains(result.Content, "“第一句。第二句。第三句。第四句。第五句。”\n") {
		t.Fatalf("content unexpectedly split narration: %q", result.Content)
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

// fakeReplaceRepo 模仿仓储层语义：FindEnabledBy* 返回全部已启用（IsEnabled=true）规则，
// 禁用过滤与 scope 匹配由 SQL 完成；Save 记录被持久化的规则拷贝，供断言禁用是否落库。
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

// 规则快照缓存：书籍/源变时失效，用户改规则后下次调用生效（C-012）。
func TestProcessorRuleCacheInvalidatesOnDifferentScope(t *testing.T) {
	repo := &fakeReplaceRepo{
		content: []*replace.ReplaceRule{{ID: 1, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: true, ScopeContent: true}},
	}
	processor := NewProcessor(repo)
	ctx := context.Background()
	in := func(name, origin string) ProcessInput {
		return ProcessInput{Book: book.Book{Name: name, Origin: origin}, Chapter: book.BookChapter{Title: "第一章"}, Content: "第一章\n广告正文"}
	}
	// 第一次：应用规则。
	first, err := processor.Process(ctx, in("斗破苍穹", "source"))
	if err != nil {
		t.Fatalf("first Process() error = %v", err)
	}
	if strings.Contains(first.Content, "广告") {
		t.Fatalf("first content = %q, want rule applied", first.Content)
	}
	// 不同 scope：必须重新读库，不能复用前一个 scope 的空规则。
	repo.content = nil
	second, err := processor.Process(ctx, in("凡人修仙传", "other"))
	if err != nil {
		t.Fatalf("second Process() error = %v", err)
	}
	if !strings.Contains(second.Content, "广告") {
		t.Fatalf("second content = %q, want scope change to re-read rules", second.Content)
	}
	// 同 scope：缓存命中时规则改动不生效（快照语义），经 InvalidateRuleCache 才生效
	//（用户改规则后下次命令生效）。
	repo.content = []*replace.ReplaceRule{{ID: 2, Name: "ad2", Pattern: "正文", Replacement: "故事", IsEnabled: true, ScopeContent: true}}
	third, err := processor.Process(ctx, in("凡人修仙传", "other"))
	if err != nil {
		t.Fatalf("third Process() error = %v", err)
	}
	if strings.Contains(third.Content, "故事") {
		t.Fatalf("third content = %q, want cached (old) rules before invalidation", third.Content)
	}
	processor.InvalidateRuleCache()
	fourth, err := processor.Process(ctx, in("凡人修仙传", "other"))
	if err != nil {
		t.Fatalf("fourth Process() error = %v", err)
	}
	if !strings.Contains(fourth.Content, "故事") || strings.Contains(fourth.Content, "正文") {
		t.Fatalf("fourth content = %q, want InvalidateRuleCache to pick up new rules", fourth.Content)
	}
}
