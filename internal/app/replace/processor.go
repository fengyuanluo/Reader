package replaceapp

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/longbridgeapp/opencc"

	"reader/internal/domain/book"
	"reader/internal/domain/replace"
)

const paragraphIndent = "　　"

type Processor struct {
	repo      replace.ReplaceRuleRepository
	options   ProcessorOptions
	openccT2S *opencc.OpenCC
	openccS2T *opencc.OpenCC
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
	return &Processor{repo: repo}
}

func (p *Processor) WithOptions(options ProcessorOptions) *Processor {
	p.options = options
	return p
}

func (p *Processor) Process(ctx context.Context, input ProcessInput) (ProcessResult, error) {
	titleRules, err := p.repo.FindEnabledByTitleScope(ctx, input.Book.Name, input.Book.Origin)
	if err != nil {
		return ProcessResult{}, err
	}
	contentRules, err := p.repo.FindEnabledByContentScope(ctx, input.Book.Name, input.Book.Origin)
	if err != nil {
		return ProcessResult{}, err
	}

	displayTitle, err := applyTitleRules(ctx, input.Chapter.Title, titleRules)
	if err != nil {
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
		if rule == nil || rule.Pattern == "" {
			continue
		}
		next, err := applyRule(ctx, content, *rule)
		if err != nil {
			if errors.Is(err, ErrRegexLimitExceeded) && rule.IsRegex {
				rule.IsEnabled = false
				_ = p.repo.Save(ctx, rule)
			}
			return ProcessResult{}, err
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

func applyTitleRules(ctx context.Context, title string, rules []*replace.ReplaceRule) (string, error) {
	displayTitle := strings.ReplaceAll(strings.ReplaceAll(title, "\r", ""), "\n", "")
	for _, rule := range rules {
		if rule == nil || rule.Pattern == "" {
			continue
		}
		next, err := applyRule(ctx, displayTitle, *rule)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(next) != "" {
			displayTitle = next
		}
	}
	return displayTitle, nil
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
	var (
		converter *opencc.OpenCC
		err       error
	)
	switch mode {
	case "t2s":
		if p.openccT2S == nil {
			p.openccT2S, err = opencc.New("t2s")
			if err != nil {
				return content
			}
		}
		converter = p.openccT2S
	case "s2t":
		if p.openccS2T == nil {
			p.openccS2T, err = opencc.New("s2t")
			if err != nil {
				return content
			}
		}
		converter = p.openccS2T
	default:
		return content
	}
	converted, err := converter.Convert(content)
	if err != nil {
		return content
	}
	return converted
}
