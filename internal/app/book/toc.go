package bookapp

import (
	"context"
	"errors"
	"strconv"
	"strings"

	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var (
	ErrTocRuleMissing = errors.New("ruleToc is required")
	ErrTocEmpty       = errors.New("toc_empty")
)

type TocInput struct {
	Source  *source.BookSource
	Book    book.Book
	TocHTML string
}

type TocService struct {
	chapters book.ChapterRepository
	fetcher  fetch.Fetcher
	script   *script.Runtime
}

func NewTocService(chapters book.ChapterRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *TocService {
	return &TocService{chapters: chapters, fetcher: fetcher, script: scriptRuntime}
}

func (s *TocService) RefreshToc(ctx context.Context, input TocInput) ([]*book.BookChapter, error) {
	src := input.Source
	if src == nil || src.RuleToc.Value == nil {
		return nil, ErrTocRuleMissing
	}
	b := input.Book
	if strings.TrimSpace(src.RuleToc.Value.PreUpdateJS) != "" && s.script != nil {
		if _, err := s.script.EvalWithBindings(ctx, src.RuleToc.Value.PreUpdateJS, script.Bindings{"source": script.SourceBindings(src), "__source": src, "book": &b}); err != nil {
			return nil, err
		}
	}
	body := input.TocHTML
	baseURL := b.TocURL
	redirectURL := b.TocURL
	if body == "" {
		req, err := prepareBookFetchRequest(ctx, b.TocURL, src, &b, s.script)
		if err != nil {
			return nil, err
		}
		resp, err := s.fetcher.Fetch(ctx, req)
		if err != nil {
			return nil, err
		}
		if s.script != nil {
			resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
			if err != nil {
				return nil, err
			}
		}
		body = string(resp.Body)
		redirectURL = resp.URL
	}
	chapters, err := s.parseAllPages(ctx, src, &b, baseURL, redirectURL, body)
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, ErrTocEmpty
	}
	chapters = dedupeChapters(chapters)
	for i, chapter := range chapters {
		chapter.Index = i
	}
	if err := s.formatChapters(ctx, src, chapters); err != nil {
		return nil, err
	}
	if s.chapters != nil {
		if err := s.chapters.ReplaceByBook(ctx, book.IDFromURL(b.BookURL), chapters); err != nil {
			return nil, err
		}
	}
	return chapters, nil
}

func (s *TocService) parseAllPages(ctx context.Context, src *source.BookSource, b *book.Book, baseURL string, redirectURL string, body string) ([]*book.BookChapter, error) {
	tocRule := src.RuleToc.Value
	listRule, reverse := normalizeTocListRule(tocRule.ChapterList)
	chapters, nextURLs, err := parseTocPage(ctx, src, b, baseURL, redirectURL, body, listRule, true, s.script)
	if err != nil {
		return nil, err
	}
	seenPages := map[string]struct{}{redirectURL: {}}
	for len(nextURLs) == 1 {
		next := nextURLs[0]
		if next == "" {
			break
		}
		if _, ok := seenPages[next]; ok {
			break
		}
		seenPages[next] = struct{}{}
		req, err := buildBookFetchRequest(ctx, next, src, s.script)
		if err != nil {
			return nil, err
		}
		resp, err := s.fetcher.Fetch(ctx, req)
		if err != nil {
			return nil, err
		}
		if s.script != nil {
			resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
			if err != nil {
				return nil, err
			}
		}
		pageChapters, pageNext, err := parseTocPage(ctx, src, b, next, resp.URL, string(resp.Body), listRule, true, s.script)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, pageChapters...)
		nextURLs = pageNext
	}
	if len(nextURLs) > 1 {
		for _, next := range nextURLs {
			if next == "" {
				continue
			}
			if _, ok := seenPages[next]; ok {
				continue
			}
			req, err := buildBookFetchRequest(ctx, next, src, s.script)
			if err != nil {
				return nil, err
			}
			resp, err := s.fetcher.Fetch(ctx, req)
			if err != nil {
				return nil, err
			}
			if s.script != nil {
				resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
				if err != nil {
					return nil, err
				}
			}
			pageChapters, _, err := parseTocPage(ctx, src, b, next, resp.URL, string(resp.Body), listRule, false, s.script)
			if err != nil {
				return nil, err
			}
			chapters = append(chapters, pageChapters...)
		}
	}
	if reverse {
		reverseChapters(chapters)
	}
	return chapters, nil
}

func parseTocPage(ctx context.Context, src *source.BookSource, b *book.Book, baseURL string, redirectURL string, body string, listRule string, getNextURL bool, scriptRuntime *script.Runtime) ([]*book.BookChapter, []string, error) {
	tocRule := src.RuleToc.Value
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, book: b, src: body},
		Book:        b,
		Source:      script.SourceBindings(src),
	})
	analyzer.LoadVariables(b.Variable)
	elements, err := analyzer.GetElements(ctx, listRule, body)
	if err != nil {
		return nil, nil, err
	}
	nextURLs := []string{}
	if getNextURL && strings.TrimSpace(tocRule.NextTocURL) != "" {
		values, err := analyzer.GetStringList(ctx, tocRule.NextTocURL, body)
		if err != nil {
			return nil, nil, err
		}
		for _, value := range values {
			value = strings.TrimSpace(urlrule.Absolute(redirectURL, value))
			if value != "" && value != redirectURL {
				nextURLs = append(nextURLs, value)
			}
		}
	}
	out := []*book.BookChapter{}
	baseVars := analyzer.Variables()
	for index, element := range elements {
		chapter := &book.BookChapter{
			BaseURL: redirectURL,
			BookURL: b.BookURL,
		}
		itemAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
			RedirectURL: redirectURL,
			SourceURL:   src.BookSourceURL,
			Script:      scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, book: b, src: body},
			Chapter:     chapter,
			Book:        b,
			Source:      script.SourceBindings(src),
		})
		itemAnalyzer.SetVariables(baseVars)
		title, _ := itemAnalyzer.GetString(ctx, tocRule.ChapterName, element)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		rawURL := firstNonEmptyChapterURL(ctx, itemAnalyzer, tocRule.ChapterURL, element)
		tag, _ := itemAnalyzer.GetString(ctx, tocRule.UpdateTime, element)
		isVolumeText, _ := itemAnalyzer.GetString(ctx, tocRule.IsVolume, element)
		isVIPText, _ := itemAnalyzer.GetString(ctx, tocRule.IsVIP, element)
		isPayText, _ := itemAnalyzer.GetString(ctx, tocRule.IsPay, element)
		chapter.URL = rawURL
		chapter.Title = title
		chapter.IsVolume = isTruthy(isVolumeText)
		chapter.IsVIP = isTruthy(isVIPText)
		chapter.IsPay = isTruthy(isPayText)
		chapter.Tag = strings.TrimSpace(tag)
		if chapter.URL == "" {
			if chapter.IsVolume {
				chapter.URL = chapter.Title + strconv.Itoa(index)
			} else {
				chapter.URL = baseURL
			}
		}
		if !chapter.IsVolume {
			chapter.URL = urlrule.Absolute(redirectURL, chapter.URL)
		}
		out = append(out, chapter)
	}
	return out, nextURLs, nil
}

func firstNonEmptyChapterURL(ctx context.Context, analyzer *ruleinfra.Analyzer, rule string, input any) string {
	values, err := analyzer.GetStringList(ctx, rule, input)
	if err != nil {
		return ""
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *TocService) formatChapters(ctx context.Context, src *source.BookSource, chapters []*book.BookChapter) error {
	formatJS := strings.TrimSpace(src.RuleToc.Value.FormatJS)
	if formatJS == "" || s.script == nil {
		return nil
	}
	for i, chapter := range chapters {
		value, err := s.script.EvalWithBindings(ctx, formatJS, script.Bindings{
			"source":  src,
			"chapter": chapter,
			"title":   chapter.Title,
			"index":   i + 1,
			"gInt":    0,
		})
		if err != nil {
			return err
		}
		if title, ok := value.(string); ok && strings.TrimSpace(title) != "" {
			chapter.Title = title
		}
	}
	return nil
}

func dedupeChapters(chapters []*book.BookChapter) []*book.BookChapter {
	out := []*book.BookChapter{}
	seen := map[string]struct{}{}
	for _, chapter := range chapters {
		key := chapter.URL
		if key == "" {
			key = chapter.Title
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, chapter)
	}
	return out
}

func normalizeTocListRule(rule string) (string, bool) {
	rule = strings.TrimSpace(rule)
	reverse := false
	if strings.HasPrefix(rule, "-") {
		reverse = true
		rule = strings.TrimSpace(rule[1:])
	} else if strings.HasPrefix(rule, "+") {
		rule = strings.TrimSpace(rule[1:])
	}
	return rule, reverse
}

func reverseChapters(chapters []*book.BookChapter) {
	for i, j := 0, len(chapters)-1; i < j; i, j = i+1, j-1 {
		chapters[i], chapters[j] = chapters[j], chapters[i]
	}
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		return true
	default:
		return false
	}
}
