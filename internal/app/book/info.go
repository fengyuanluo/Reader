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

// ErrDownloadURLsEmpty 对应 Legado BookInfo.kt:160 的 NoStackTraceException("下载链接为空")。
var ErrDownloadURLsEmpty = errors.New("下载链接为空")

// bookSourceTypeWebFile 是 Legado BookType.webFile：文件类书源只提供下载链接。
const bookSourceTypeWebFile = 3

type InfoInput struct {
	Source    *source.BookSource
	Book      book.Book
	InfoHTML  string
	CanRename bool
	// SkipSave 为真时只抓取并解析详情，不写 books 表；
	// 对应 Legado CheckSource / Debug 的 needSave=false 语义。
	SkipSave bool
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
	ctx = script.WithStage(ctx, "info")
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
	if s.books != nil && !input.SkipSave {
		// 信息刷新只写信息列，避免整行写覆盖阅读进度（dur_*）等无关列。
		if err := s.books.UpdateInfo(ctx, &b); err != nil {
			return book.Book{}, err
		}
	}
	return b, nil
}

func parseInfo(ctx context.Context, src *source.BookSource, b *book.Book, baseURL string, redirectURL string, body string, canRename bool, scriptRuntime *script.Runtime) error {
	// 书源未声明 ruleBookInfo 时走兜底预览路径（PreviewSearchBookFromInfo），
	// 这里必须返回明确错误，不能解引用空规则导致 HTTP 500。
	if src == nil || src.RuleBookInfo.Value == nil {
		return ErrBookInfoRuleMissing
	}
	infoRule := src.RuleBookInfo.Value
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Book: b, Src: body},
		Book:        b,
		Source:      scriptRuntime.SourceBindings(ctx, src),
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
	name = FormatBookName(name)
	if name != "" && (allowRename || b.Name == "") {
		b.Name = name
	}
	author, _ := analyzer.GetString(ctx, infoRule.Author, content)
	author = FormatBookAuthor(author)
	if author != "" && (allowRename || b.Author == "") {
		b.Author = author
	}
	if values, err := analyzer.GetStringList(ctx, infoRule.Kind, content); err == nil && len(values) > 0 {
		b.Kind = strings.TrimSpace(strings.Join(values, ","))
	}
	if value, err := analyzer.GetString(ctx, infoRule.WordCount, content); err == nil {
		if formatted := FormatWordCount(value); formatted != "" {
			b.WordCount = formatted
		}
	}
	if value, err := analyzer.GetString(ctx, infoRule.LastChapter, content); err == nil && strings.TrimSpace(value) != "" {
		b.LatestChapterTitle = strings.TrimSpace(value)
	}
	if value, err := analyzer.GetString(ctx, infoRule.Intro, content); err == nil && strings.TrimSpace(value) != "" {
		b.Intro = FormatIntro(value)
	}
	if value, err := analyzer.GetURL(ctx, infoRule.CoverURL, content); err == nil && value != "" {
		b.CoverURL = value
	}
	// 对齐 Legado BookInfo.kt:148-171：文件类书源（type=3）只解析下载链接，不解析 tocUrl；
	// 下载链接为空时直接报错，问题在详情阶段暴露而不是拖到下载链路。
	if src.BookSourceType == bookSourceTypeWebFile {
		b.DownloadURLs = nil
		if strings.TrimSpace(infoRule.DownloadURLs) != "" {
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
		if len(b.DownloadURLs) == 0 {
			return ErrDownloadURLsEmpty
		}
	} else {
		if value, err := analyzer.GetURL(ctx, infoRule.TocURL, content); err == nil && value != "" {
			b.TocURL = value
		}
		if b.TocURL == "" {
			b.TocURL = baseURL
		}
		if b.TocURL == baseURL {
			b.TocHTML = body
		}
	}
	if variable := analyzer.VariablesJSON(); variable != "" {
		b.Variable = variable
	}
	b.InfoHTML = body
	return nil
}

func PreviewSearchBookFromInfo(ctx context.Context, src *source.BookSource, redirectURL string, body string, seed book.SearchBook, scriptRuntime *script.Runtime) (book.SearchBook, error) {
	ctx = script.WithStage(ctx, "info")
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
