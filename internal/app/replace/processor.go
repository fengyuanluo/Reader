package replaceapp

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"reader/internal/domain/book"
	"reader/internal/domain/replace"
	"reader/internal/infra/chinese"
)

const paragraphIndent = "　　"

// ruleSnapshot 是替换规则集的进程内快照缓存；Processor 为进程级共享实例，
// 规则改动时失效，保证“用户改规则后下次命令生效”。
type ruleSnapshot struct {
	titleRules   []*replace.ReplaceRule
	contentRules []*replace.ReplaceRule
}

type Processor struct {
	repo    replace.ReplaceRuleRepository
	options ProcessorOptions
	cache   *sync.Map // map[replaceRuleScope]ruleSnapshot，进程生命周期内有效
}

// replaceRuleScope 是规则快照缓存键：规则集随书籍名称与来源变化。
type replaceRuleScope struct {
	bookName string
	origin   string
}

// InvalidateRuleCache 清空规则快照缓存：规则增删改后调用，保证下次 Process 重新读库。
func (p *Processor) InvalidateRuleCache() {
	p.cache.Range(func(key, _ any) bool {
		p.cache.Delete(key)
		return true
	})
}

type ProcessorOptions struct {
	ReSegmentEnabled     bool
	ChineseConverterType int
}

type ProcessInput struct {
	Book         book.Book
	Chapter      book.BookChapter
	Content      string
	IncludeTitle bool
}

type ProcessResult struct {
	Content          string
	SameTitleRemoved bool
	EffectiveRuleIDs []int64
}

func NewProcessor(repo replace.ReplaceRuleRepository) *Processor {
	return &Processor{repo: repo, cache: &sync.Map{}}
}

func (p *Processor) WithOptions(options ProcessorOptions) *Processor {
	p.options = options
	return p
}

// ruleSet 读取（带进程内快照缓存）指定 scope 的启用规则集；规则缓存命中时不访问仓库。
func (p *Processor) ruleSet(ctx context.Context, scope replaceRuleScope) ([]*replace.ReplaceRule, []*replace.ReplaceRule, error) {
	if cached, ok := p.cache.Load(scope); ok {
		snapshot := cached.(ruleSnapshot)
		return snapshot.titleRules, snapshot.contentRules, nil
	}
	titleRules, err := p.repo.FindEnabledByTitleScope(ctx, scope.bookName, scope.origin)
	if err != nil {
		return nil, nil, err
	}
	contentRules, err := p.repo.FindEnabledByContentScope(ctx, scope.bookName, scope.origin)
	if err != nil {
		return nil, nil, err
	}
	p.cache.Store(scope, ruleSnapshot{titleRules: titleRules, contentRules: contentRules})
	return titleRules, contentRules, nil
}

func (p *Processor) Process(ctx context.Context, input ProcessInput) (ProcessResult, error) {
	scope := replaceRuleScope{bookName: input.Book.Name, origin: input.Book.Origin}
	titleRules, contentRules, err := p.ruleSet(ctx, scope)
	if err != nil {
		return ProcessResult{}, err
	}

	displayTitle := input.Chapter.Title
	// 对齐 Legado ContentProcessor.kt:186-192 与 BookChapter.getDisplayTitle 默认 chineseConvert=true：
	// includeTitle 重加标题时标题与正文使用同一简繁转换设置，保证文字体系一致。
	if input.IncludeTitle {
		displayTitle, err = p.applyTitleRules(ctx, p.convertChinese(displayTitle), titleRules)
		if err != nil {
			return ProcessResult{}, err
		}
	} else if displayTitle, _, err = p.applyTitleRulesForDedup(ctx, input.Chapter.Title, titleRules); err != nil {
		// includeTitle=false 时 Legado 完全跳过标题处理；这里只为 sameTitle 去重计算 displayTitle。
		return ProcessResult{}, err
	}

	content := input.Content
	sameTitleRemoved := false
	if strings.TrimSpace(content) != "null" {
		var removed bool
		content, removed = removeRepeatedTitle(content, input.Book.Name, input.Chapter.Title)
		sameTitleRemoved = removed
		if !sameTitleRemoved && displayTitle != input.Chapter.Title {
			content, sameTitleRemoved = removeRepeatedTitle(content, input.Book.Name, displayTitle)
		}
		if p.options.ReSegmentEnabled {
			content = reSegment(content, input.Chapter.Title)
		}
		content = p.convertChinese(content)
	}

	content = trimLines(content)
	effectiveRuleIDs := []int64{}
	for _, rule := range contentRules {
		if rule == nil || rule.Pattern == "" || !rule.IsEnabled {
			continue
		}
		next, err := applyRule(ctx, content, *rule)
		if err != nil {
			// 对齐 Legado ContentProcessor.kt:160-180：单条规则出错只影响该条规则，不中止整章净化。
			if ctx.Err() != nil {
				return ProcessResult{}, err
			}
			p.handleRuleFailure(ctx, rule, err)
			continue
		}
		if next != content {
			effectiveRuleIDs = append(effectiveRuleIDs, rule.ID)
			content = next
		}
	}

	lines := []string{}
	if input.IncludeTitle {
		lines = append(lines, displayTitle)
	}
	lines = append(lines, strings.Split(content, "\n")...)
	return ProcessResult{
		Content:          formatParagraphs(lines, input.IncludeTitle),
		SameTitleRemoved: sameTitleRemoved,
		EffectiveRuleIDs: effectiveRuleIDs,
	}, nil
}

// applyTitleRulesForDedup 为 sameTitle 去重计算 displayTitle：仅应用 titleRules，不做简繁转换
// （对齐 Legado ContentProcessor.kt:110-116 中 chineseConvert=false 的 getDisplayTitle 调用），
// IncludeTitle=false 时不对外展示标题，规则处理结果只用于去重判断。
func (p *Processor) applyTitleRulesForDedup(ctx context.Context, title string, rules []*replace.ReplaceRule) (string, bool, error) {
	next, err := p.applyTitleRules(ctx, title, rules)
	return next, next != title, err
}

// applyTitleRules 应用标题域替换规则；与 Legado BookChapter.getDisplayTitle 一致，单条规则出错不中止。
func (p *Processor) applyTitleRules(ctx context.Context, title string, rules []*replace.ReplaceRule) (string, error) {
	displayTitle := strings.ReplaceAll(strings.ReplaceAll(title, "\r", ""), "\n", "")
	for _, rule := range rules {
		if rule == nil || rule.Pattern == "" || !rule.IsEnabled {
			continue
		}
		next, err := applyRule(ctx, displayTitle, *rule)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			p.handleRuleFailure(ctx, rule, err)
			continue
		}
		if strings.TrimSpace(next) != "" {
			displayTitle = next
		}
	}
	return displayTitle, nil
}

// handleRuleFailure 处理单条替换规则失败：仅正则执行超时（ErrRegexTimeout，对应 Legado 的
// RegexTimeoutException 分支）才禁用规则并持久化；输入过大（ErrRegexInputTooLarge）与请求
// 取消（ErrRegexCancelled，对应 CancellationException 分支）不属于该分支，只告警不禁用。
// 禁用走仓储更新（通过规则值拷贝触发 Save），不改快照共享指针，避免与其他 goroutine 构成数据竞争。
func (p *Processor) handleRuleFailure(ctx context.Context, rule *replace.ReplaceRule, err error) {
	switch classifyRegexFailure(err) {
	case regexFailureTimeout:
		if rule.IsRegex {
			// rule 是快照内共享指针的元素；禁用走值拷贝触发仓储 Save，不改共享指针，
			// 避免与其他 goroutine 构成数据竞争（S14-05）。
			disabled := *rule
			disabled.IsEnabled = false
			if saveErr := p.repo.Save(ctx, &disabled); saveErr != nil {
				slog.Warn("disable replace rule failed", "ruleId", rule.ID, "ruleName", rule.Name, "error", saveErr)
			} else {
				// 禁用已持久化，失效快照缓存，避免后续章节仍重放该禁用规则（S14-05）。
				p.InvalidateRuleCache()
			}
		}
		slog.Warn("replace rule timed out, disabled", "ruleId", rule.ID, "ruleName", rule.Name)
	case regexFailureCancelled:
		// 请求取消：静默跳过（对齐 Legado CancellationException 分支），不写日志噪音。
	default:
		slog.Warn("replace rule failed", "ruleId", rule.ID, "ruleName", rule.Name, "error", err)
	}
}

func applyRule(ctx context.Context, text string, rule replace.ReplaceRule) (string, error) {
	return replaceText(ctx, rule, text)
}

func removeRepeatedTitle(content string, bookName string, title string) (string, bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return content, false
	}
	pattern := `^(\s|\p{P}|` + regexp.QuoteMeta(bookName) + `)*` + titlePattern(title) + `(\s)*`
	// pattern 内嵌逐章唯一的标题，命中率≈0：直接编译，避免冲刷 infra/rule/regex 的共享 LRU。
	re, err := regexp.Compile(pattern)
	if err != nil {
		return content, false
	}
	loc := re.FindStringIndex(content)
	if loc == nil || loc[0] != 0 {
		return content, false
	}
	return content[loc[1]:], true
}

func titlePattern(title string) string {
	fields := strings.Fields(title)
	if len(fields) == 0 {
		return regexp.QuoteMeta(title)
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, regexp.QuoteMeta(field))
	}
	return strings.Join(parts, `\s*`)
}

func trimLines(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

func formatParagraphs(lines []string, includeTitle bool) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		paragraph := strings.Trim(line, " \t\r\n　")
		if paragraph == "" {
			continue
		}
		if len(out) == 0 && includeTitle {
			out = append(out, paragraph)
			continue
		}
		out = append(out, paragraphIndent+paragraph)
	}
	return strings.Join(out, "\n")
}

func (p *Processor) convertChinese(content string) string {
	switch p.options.ChineseConverterType {
	case 1:
		return p.convertWithMode(content, "t2s")
	case 2:
		return p.convertWithMode(content, "s2t")
	default:
		return content
	}
}

func (p *Processor) convertWithMode(content string, mode string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	// 词典加载与转换由 infra/chinese 用 sync.Once 保护：Processor 为进程级共享实例，不能在此做无锁懒初始化。
	switch mode {
	case "t2s":
		return chinese.T2S(content)
	case "s2t":
		return chinese.S2T(content)
	default:
		return content
	}
}
