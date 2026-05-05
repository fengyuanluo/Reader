package book

import (
	"errors"
	"net/url"
	"strings"
)

var ErrInvalidChapterIndex = errors.New("chapter index must be >= 0")

type Book struct {
	BookURL            string `json:"bookUrl"`
	TocURL             string `json:"tocUrl,omitempty"`
	Origin             string `json:"origin"`
	OriginName         string `json:"originName"`
	Name               string `json:"name"`
	Author             string `json:"author"`
	Kind               string `json:"kind,omitempty"`
	CoverURL           string `json:"coverUrl,omitempty"`
	Intro              string `json:"intro,omitempty"`
	Type               int    `json:"type"`
	WordCount          string `json:"wordCount,omitempty"`
	LatestChapterTitle string `json:"latestChapterTitle,omitempty"`
	OriginOrder        int    `json:"originOrder"`
	Variable           string `json:"variable,omitempty"`
	DurChapterTitle    string `json:"durChapterTitle,omitempty"`
	DurChapterIndex    int    `json:"durChapterIndex"`
	DurChapterPos      int    `json:"durChapterPos"`
	DurChapterTime     int64  `json:"durChapterTime"`

	InfoHTML     string   `json:"-"`
	TocHTML      string   `json:"-"`
	DownloadURLs []string `json:"-"`
}

type SearchBook struct {
	BookURL            string   `json:"bookUrl"`
	Origin             string   `json:"origin"`
	OriginName         string   `json:"originName"`
	Type               int      `json:"type"`
	Name               string   `json:"name"`
	Author             string   `json:"author"`
	Kind               string   `json:"kind,omitempty"`
	CoverURL           string   `json:"coverUrl,omitempty"`
	Intro              string   `json:"intro,omitempty"`
	WordCount          string   `json:"wordCount,omitempty"`
	LatestChapterTitle string   `json:"latestChapterTitle,omitempty"`
	TocURL             string   `json:"tocUrl,omitempty"`
	Variable           string   `json:"variable,omitempty"`
	OriginOrder        int      `json:"originOrder"`
	Origins            []string `json:"origins,omitempty"`

	InfoHTML string `json:"-"`
	TocHTML  string `json:"-"`
}

func (b SearchBook) ToBook() Book {
	return Book{
		BookURL:            b.BookURL,
		TocURL:             b.TocURL,
		Origin:             b.Origin,
		OriginName:         b.OriginName,
		Name:               b.Name,
		Author:             b.Author,
		Kind:               b.Kind,
		CoverURL:           b.CoverURL,
		Intro:              b.Intro,
		Type:               b.Type,
		WordCount:          b.WordCount,
			LatestChapterTitle: b.LatestChapterTitle,
			OriginOrder:        b.OriginOrder,
			Variable:           b.Variable,
			InfoHTML:           b.InfoHTML,
			TocHTML:            b.TocHTML,
		}
}

type BookChapter struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	IsVolume    bool   `json:"isVolume"`
	BaseURL     string `json:"baseUrl"`
	BookURL     string `json:"bookUrl"`
	Index       int    `json:"index"`
	IsVIP       bool   `json:"isVip"`
	IsPay       bool   `json:"isPay"`
	ResourceURL string `json:"resourceUrl,omitempty"`
	Tag         string `json:"tag,omitempty"`
	WordCount   string `json:"wordCount,omitempty"`
	Variable    string `json:"variable,omitempty"`
}

func (c BookChapter) Validate() error {
	if c.Index < 0 {
		return ErrInvalidChapterIndex
	}
	return nil
}

func (c BookChapter) AbsoluteURL() string {
	if c.URL == "" {
		return ""
	}
	if c.IsVolume && strings.HasPrefix(c.URL, c.Title) {
		return c.BaseURL
	}
	if c.BaseURL == "" {
		return c.URL
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return c.URL
	}
	ref, err := url.Parse(c.URL)
	if err != nil {
		return c.URL
	}
	return base.ResolveReference(ref).String()
}
