package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	bookapp "reader/internal/app/book"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var (
	ErrEmptyExploreURL = errors.New("exploreUrl is required")
	ErrEmptyDiscovery  = errors.New("discovery category is required")
)

type Category struct {
	Title string
	URL   string
}

type Input struct {
	SourceIDs    []string
	Group        string
	DiscoveryURL string
	Page         int
}

type Result struct {
	Items    []book.SearchBook
	Warnings []Warning
}

type Warning struct {
	SourceID       string
	BookSourceURL  string
	BookSourceName string
	Code           string
	Message        string
}

type Service struct {
	repo    source.BookSourceRepository
	fetcher fetch.Fetcher
	script  *script.Runtime
	cache   sync.Map
}

func NewService(repo source.BookSourceRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *Service {
	return &Service{repo: repo, fetcher: fetcher, script: scriptRuntime}
}

func (s *Service) Categories(ctx context.Context, sourceID string) ([]Category, error) {
	src, err := s.repo.FindByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	return s.categoriesForSource(ctx, src)
}

func (s *Service) Discover(ctx context.Context, input Input) (Result, error) {
	sources, err := s.resolveSources(ctx, input)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, src := range sources {
		items, err := s.discoverSource(ctx, src, input)
		if err != nil {
			result.Warnings = append(result.Warnings, Warning{
				SourceID:       src.SourceID,
				BookSourceURL:  src.BookSourceURL,
				BookSourceName: src.BookSourceName,
				Code:           "discovery_failed",
				Message:        err.Error(),
			})
			continue
		}
		result.Items = append(result.Items, items...)
	}
	return result, nil
}

func (s *Service) resolveSources(ctx context.Context, input Input) ([]*source.BookSource, error) {
	if len(input.SourceIDs) > 0 {
		out := []*source.BookSource{}
		for _, id := range input.SourceIDs {
			src, err := s.repo.FindByID(ctx, id)
			if err != nil {
				return nil, err
			}
			if !src.EnabledExplore {
				continue
			}
			out = append(out, src)
		}
		return out, nil
	}
	enabled := true
	filter := source.BookSourceFilter{Page: 1, PageSize: 10000, EnabledExplore: &enabled}
	if input.Group != "" {
		filter.Group = input.Group
	}
	page, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Service) discoverSource(ctx context.Context, src *source.BookSource, input Input) ([]book.SearchBook, error) {
	discoveryURL := strings.TrimSpace(input.DiscoveryURL)
	if discoveryURL == "" {
		categories, err := s.categoriesForSource(ctx, src)
		if err != nil {
			return nil, err
		}
		if len(categories) == 0 {
			return nil, ErrEmptyDiscovery
		}
		discoveryURL = categories[0].URL
		if strings.TrimSpace(discoveryURL) == "" {
			discoveryURL = categories[0].Title
		}
	}
	discoveryURL = buildPageURL(discoveryURL, input.Page)
	discoveryURL = urlrule.Absolute(src.BookSourceURL, discoveryURL)
	req := fetch.Request{URL: discoveryURL, UseCookieJar: src.EnabledCookieJar}
	headers, err := sourceapp.NewScriptIntegration(s.script).HeaderMap(ctx, src)
	if err != nil {
		return nil, err
	}
	req.Headers = headers
	sourceapp.ApplyNetworkOptions(&req, nil)
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
	listRule, err := listRuleForSource(src)
	if err != nil {
		return nil, err
	}
	return parseBookList(ctx, src, listRule, resp.URL, string(resp.Body), s.script)
}

func (s *Service) categoriesForSource(ctx context.Context, src *source.BookSource) ([]Category, error) {
	raw := strings.TrimSpace(src.ExploreURL)
	if raw == "" {
		return nil, ErrEmptyExploreURL
	}
	if strings.HasPrefix(strings.ToLower(raw), "@js:") || strings.HasPrefix(strings.ToLower(raw), "<js>") {
		if cached, ok := s.loadCategoryCache(src, raw); ok {
			return cached, nil
		}
		if s.script == nil {
			return nil, ruleinfra.ErrScriptUnavailable
		}
		scriptText := raw
		if strings.HasPrefix(strings.ToLower(raw), "@js:") {
			scriptText = raw[4:]
		} else if end := strings.LastIndex(strings.ToLower(raw), "</js>"); end > 4 {
			scriptText = raw[4:end]
		}
		value, err := s.script.EvalWithBindings(ctx, scriptText, script.Bindings{"source": script.SourceBindings(src), "__source": src})
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(toString(value))
	}
	if strings.HasPrefix(raw, "[") {
		var values []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
			}
			out := make([]Category, 0, len(values))
			for _, value := range values {
				if strings.TrimSpace(value.Title) == "" && strings.TrimSpace(value.URL) == "" {
					continue
			}
					out = append(out, Category{Title: strings.TrimSpace(value.Title), URL: strings.TrimSpace(value.URL)})
				}
			s.storeCategoryCache(src, strings.TrimSpace(src.ExploreURL), out)
			return out, nil
		}
		parts := splitExploreKinds(raw)
		out := make([]Category, 0, len(parts))
	for _, part := range parts {
		cfg := strings.SplitN(part, "::", 2)
		category := Category{Title: strings.TrimSpace(cfg[0])}
		if len(cfg) > 1 {
			category.URL = strings.TrimSpace(cfg[1])
		}
			if category.Title != "" || category.URL != "" {
				out = append(out, category)
			}
		}
		s.storeCategoryCache(src, strings.TrimSpace(src.ExploreURL), out)
		return out, nil
}

func listRuleForSource(src *source.BookSource) (rule.SearchRule, error) {
	if src.RuleExplore.Value != nil && strings.TrimSpace(src.RuleExplore.Value.BookList) != "" {
		explore := src.RuleExplore.Value
		return rule.SearchRule{
			BookList:    explore.BookList,
			Name:        explore.Name,
			Author:      explore.Author,
			Intro:       explore.Intro,
			Kind:        explore.Kind,
			LastChapter: explore.LastChapter,
			UpdateTime:  explore.UpdateTime,
			BookURL:     explore.BookURL,
			CoverURL:    explore.CoverURL,
			WordCount:   explore.WordCount,
		}, nil
	}
	if src.RuleSearch.Value != nil && strings.TrimSpace(src.RuleSearch.Value.BookList) != "" {
		return *src.RuleSearch.Value, nil
	}
	return rule.SearchRule{}, errors.New("ruleExplore.bookList or ruleSearch.bookList is required")
}

func parseBookList(ctx context.Context, src *source.BookSource, listRule rule.SearchRule, redirectURL string, body string, scriptRuntime *script.Runtime) ([]book.SearchBook, error) {
	ruleText, reverse := normalizeDiscoveryListRule(listRule.BookList)
		analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
			RedirectURL: redirectURL,
			SourceURL:   src.BookSourceURL,
			Script:      scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, src: body},
			Source:      script.SourceBindings(src),
		})
		elements, err := analyzer.GetElements(ctx, ruleText, body)
		if err != nil {
			return nil, err
		}
		out := []book.SearchBook{}
		seen := map[string]struct{}{}
		baseVars := analyzer.Variables()
		for _, element := range elements {
			searchBook := book.SearchBook{
				Origin:      src.BookSourceURL,
				OriginName:  src.BookSourceName,
				Type:        src.BookSourceType,
				OriginOrder: src.CustomOrder,
				Origins:     []string{src.BookSourceURL},
				Variable:    encodeDiscoveryVariables(baseVars),
			}
			itemAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
				RedirectURL: redirectURL,
				SourceURL:   src.BookSourceURL,
				Script:      scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, src: body, book: &searchBook},
				Book:        &searchBook,
				Source:      script.SourceBindings(src),
			})
			itemAnalyzer.SetVariables(baseVars)
			name, _ := itemAnalyzer.GetString(ctx, listRule.Name, element)
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			author, _ := itemAnalyzer.GetString(ctx, listRule.Author, element)
			bookURL, _ := itemAnalyzer.GetURL(ctx, listRule.BookURL, element)
			if bookURL == "" {
				bookURL = redirectURL
			}
			if _, ok := seen[bookURL]; ok {
				continue
			}
			seen[bookURL] = struct{}{}
			kind, _ := itemAnalyzer.GetString(ctx, listRule.Kind, element)
			intro, _ := itemAnalyzer.GetString(ctx, listRule.Intro, element)
			cover, _ := itemAnalyzer.GetURL(ctx, listRule.CoverURL, element)
			wordCount, _ := itemAnalyzer.GetString(ctx, listRule.WordCount, element)
			lastChapter, _ := itemAnalyzer.GetString(ctx, listRule.LastChapter, element)
			searchBook.BookURL = bookURL
			searchBook.Name = name
			searchBook.Author = strings.TrimSpace(author)
			searchBook.Kind = strings.TrimSpace(kind)
			searchBook.CoverURL = cover
			searchBook.Intro = strings.TrimSpace(intro)
			searchBook.WordCount = strings.TrimSpace(wordCount)
			searchBook.LatestChapterTitle = strings.TrimSpace(lastChapter)
			if variable := itemAnalyzer.VariablesJSON(); variable != "" {
				searchBook.Variable = variable
			}
			out = append(out, searchBook)
		}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if len(out) == 0 {
		preview, err := bookapp.PreviewSearchBookFromInfo(ctx, src, redirectURL, body, book.SearchBook{
			BookURL:     redirectURL,
			Origin:      src.BookSourceURL,
			OriginName:  src.BookSourceName,
			OriginOrder: src.CustomOrder,
			Type:        src.BookSourceType,
		}, scriptRuntime)
		if err == nil && strings.TrimSpace(preview.Name) != "" {
			return []book.SearchBook{preview}, nil
		}
	}
	return out, nil
}

func buildPageURL(template string, page int) string {
	if page < 1 {
		page = 1
	}
	pageText := strconv.Itoa(page)
	out := strings.ReplaceAll(template, "{{page}}", pageText)
	out = strings.ReplaceAll(out, "{page}", pageText)
	return out
}

func splitExploreKinds(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' })
	out := []string{}
	for _, field := range fields {
		for _, part := range strings.Split(field, "&&") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func (s *Service) loadCategoryCache(src *source.BookSource, rawExploreURL string) ([]Category, bool) {
	key := categoryCacheKey(src, rawExploreURL)
	if key == "" {
		return nil, false
	}
	value, ok := s.cache.Load(key)
	if !ok {
		return nil, false
	}
	categories, ok := value.([]Category)
	if !ok {
		return nil, false
	}
	return cloneCategories(categories), true
}

func (s *Service) storeCategoryCache(src *source.BookSource, rawExploreURL string, categories []Category) {
	key := categoryCacheKey(src, rawExploreURL)
	if key == "" {
		return
	}
	s.cache.Store(key, cloneCategories(categories))
}

func categoryCacheKey(src *source.BookSource, rawExploreURL string) string {
	if src == nil {
		return ""
	}
	return strings.TrimSpace(src.BookSourceURL) + "\n" + strings.TrimSpace(rawExploreURL)
}

func cloneCategories(categories []Category) []Category {
	if len(categories) == 0 {
		return nil
	}
	out := make([]Category, len(categories))
	copy(out, categories)
	return out
}

func normalizeDiscoveryListRule(rule string) (string, bool) {
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

type scriptAdapter struct {
	runtime *script.Runtime
	baseURL string
	source  *source.BookSource
	src     any
	book    *book.SearchBook
}

func (s scriptAdapter) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	if s.runtime == nil {
		return nil, ruleinfra.ErrScriptUnavailable
	}
	return s.runtime.EvalWithBindings(ctx, scriptText, script.Bindings{
		"result":   input,
		"src":      s.src,
		"baseUrl":  s.baseURL,
		"source":   script.SourceBindings(s.source),
		"__source": s.source,
		"book":     s.book,
	})
}

func encodeDiscoveryVariables(vars map[string]string) string {
	if len(vars) == 0 {
		return ""
	}
	raw, err := json.Marshal(vars)
	if err != nil {
		return ""
	}
	return string(raw)
}
