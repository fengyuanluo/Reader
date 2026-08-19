package bookapp

import (
	"context"
	"errors"
	"strings"

	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var ErrBookInfoRuleMissing = errors.New("ruleBookInfo is required")

type InfoInput struct {
	Source    *source.BookSource
	Book      book.Book
	InfoHTML  string
	CanRename bool
}

type InfoService struct {
	books   book.BookRepository
	fetcher fetch.Fetcher
	script  *script.Runtime
}

func NewInfoService(books book.BookRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *InfoService {
	return &InfoService{books: books, fetcher: fetcher, script: scriptRuntime}
}

func (s *InfoService) RefreshInfo(ctx context.Context, input InfoInput) (book.Book, error) {
	src := input.Source
	if src == nil || src.RuleBookInfo.Value == nil {
		return book.Book{}, ErrBookInfoRuleMissing
	}
	b := input.Book
	if b.Origin == "" {
		b.Origin = src.BookSourceURL
	}
	if b.OriginName == "" {
		b.OriginName = src.BookSourceName
	}
	b.Type = src.BookSourceType
	if b.OriginOrder == 0 {
		b.OriginOrder = src.CustomOrder
	}
	body := input.InfoHTML
	redirectURL := b.BookURL
	if body == "" {
		req, err := prepareBookFetchRequest(ctx, b.BookURL, src, &b, s.script)
		if err != nil {
			return book.Book{}, err
		}
		resp, err := executeBookFetchRequest(ctx, s.fetcher, req, src, &b, s.script)
		if err != nil {
			return book.Book{}, err
		}
		body = string(resp.Body)
		redirectURL = resp.URL
		if s.script != nil {
			resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
			if err != nil {
				return book.Book{}, err
			}
			body = string(resp.Body)
		}
	}
	if err := parseInfo(ctx, src, &b, b.BookURL, redirectURL, body, input.CanRename, s.script); err != nil {
		return book.Book{}, err
	}
	if s.books != nil {
		if err := s.books.Save(ctx, &b); err != nil {
			return book.Book{}, err
		}
	}
	return b, nil
}

func parseInfo(ctx context.Context, src *source.BookSource, b *book.Book, baseURL string, redirectURL string, body string, canRename bool, scriptRuntime *script.Runtime) error {
	infoRule := src.RuleBookInfo.Value
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, book: b, src: body},
		Book:        b,
		Source:      script.SourceBindings(src),
	})
	analyzer.LoadVariables(b.Variable)
	var content any = body
	if strings.TrimSpace(infoRule.Init) != "" {
		elements, err := analyzer.GetElements(ctx, infoRule.Init, body)
		if err != nil {
			return err
		}
		if len(elements) > 0 {
			content = elements[0]
		}
	}
	allowRename := canRename && strings.TrimSpace(infoRule.CanReName) != ""
	name, _ := analyzer.GetString(ctx, infoRule.Name, content)
	name = strings.TrimSpace(name)
	if name != "" && (allowRename || b.Name == "") {
		b.Name = name
	}
	author, _ := analyzer.GetString(ctx, infoRule.Author, content)
	author = strings.TrimSpace(author)
	if author != "" && (allowRename || b.Author == "") {
		b.Author = author
	}
	if values, err := analyzer.GetStringList(ctx, infoRule.Kind, content); err == nil && len(values) > 0 {
		b.Kind = strings.TrimSpace(strings.Join(values, ","))
	}
	if value, err := analyzer.GetString(ctx, infoRule.WordCount, content); err == nil && strings.TrimSpace(value) != "" {
		b.WordCount = strings.TrimSpace(value)
	}
	if value, err := analyzer.GetString(ctx, infoRule.LastChapter, content); err == nil && strings.TrimSpace(value) != "" {
		b.LatestChapterTitle = strings.TrimSpace(value)
	}
	if value, err := analyzer.GetString(ctx, infoRule.Intro, content); err == nil && strings.TrimSpace(value) != "" {
		b.Intro = strings.TrimSpace(value)
	}
	if value := firstNonEmptyURLValue(ctx, analyzer, infoRule.CoverURL, content); value != "" {
		value = strings.TrimSpace(value)
		if absolute := urlrule.Absolute(redirectURL, value); absolute != "" {
			value = absolute
		}
		b.CoverURL = value
	}
	if value := firstNonEmptyURLValue(ctx, analyzer, infoRule.TocURL, content); value != "" {
		value = strings.TrimSpace(value)
		if absolute := urlrule.Absolute(redirectURL, value); absolute != "" {
			value = absolute
		}
		b.TocURL = value
	}
	if b.TocURL == "" {
		b.TocURL = baseURL
	}
	if b.TocURL == baseURL {
		b.TocHTML = body
	}
	if src.BookSourceType == 3 && strings.TrimSpace(infoRule.DownloadURLs) != "" {
		if values, err := analyzer.GetStringListRaw(ctx, infoRule.DownloadURLs, content); err == nil {
			downloads := make([]string, 0, len(values))
			for _, value := range values {
				value = strings.TrimSpace(value)
				if value == "" {
					continue
				}
				if absolute := urlrule.AbsolutePreservingOptions(redirectURL, value); absolute != "" {
					value = absolute
				}
				downloads = append(downloads, value)
			}
			b.DownloadURLs = downloads
		}
	}
	if variable := analyzer.VariablesJSON(); variable != "" {
		b.Variable = variable
	}
	b.InfoHTML = body
	return nil
}

func firstNonEmptyURLValue(ctx context.Context, analyzer *ruleinfra.Analyzer, rule string, input any) string {
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

func PreviewSearchBookFromInfo(ctx context.Context, src *source.BookSource, redirectURL string, body string, seed book.SearchBook, scriptRuntime *script.Runtime) (book.SearchBook, error) {
	if src == nil || src.RuleBookInfo.Value == nil {
		return seed, ErrBookInfoRuleMissing
	}
	b := seed.ToBook()
	if b.BookURL == "" {
		b.BookURL = redirectURL
	}
	if b.Origin == "" {
		b.Origin = src.BookSourceURL
	}
	if b.OriginName == "" {
		b.OriginName = src.BookSourceName
	}
	if err := parseInfo(ctx, src, &b, b.BookURL, redirectURL, body, true, scriptRuntime); err != nil {
		return seed, err
	}
	seed.BookURL = b.BookURL
	seed.TocURL = b.TocURL
	seed.Origin = b.Origin
	seed.OriginName = b.OriginName
	seed.Type = b.Type
	seed.Name = b.Name
	seed.Author = b.Author
	seed.Kind = b.Kind
	seed.CoverURL = b.CoverURL
	seed.Intro = b.Intro
	seed.WordCount = b.WordCount
	seed.LatestChapterTitle = b.LatestChapterTitle
	seed.OriginOrder = b.OriginOrder
	seed.Variable = b.Variable
	seed.InfoHTML = body
	seed.TocHTML = b.TocHTML
	return seed, nil
}

type scriptAdapter struct {
	runtime *script.Runtime
	baseURL string
	source  *source.BookSource
	book    *book.Book
	src     any
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
