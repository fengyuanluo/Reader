package book

import (
	"errors"
	"net/url"
	"strings"
)

var (
	ErrInvalidChapterIndex = errors.New("chapter index must be >= 0")
	// ErrNotFound 表示书籍记录不存在。仓储实现必须返回（或包裹）该哨兵错误，
	// 调用方用 errors.Is 判定，禁止比较错误文本。
	ErrNotFound = errors.New("book not found")
)

// Book 对齐 Legado Book.kt 的书籍元数据；用户自定义字段（customTag/customCoverUrl/customIntro）与
// 本地书籍 charset、书架 group/order/canUpdate、检查与同步时间戳都需要能往返 API。
type Book struct {
	BookURL            string `json:"bookUrl"`
	TocURL             string `json:"tocUrl,omitempty"`
	Origin             string `json:"origin"`
	OriginName         string `json:"originName"`
	Name               string `json:"name"`
	Author             string `json:"author"`
	Kind               string `json:"kind,omitempty"`
	CustomTag          string `json:"customTag,omitempty"`
	CoverURL           string `json:"coverUrl,omitempty"`
	CustomCoverURL     string `json:"customCoverUrl,omitempty"`
	Intro              string `json:"intro,omitempty"`
	CustomIntro        string `json:"customIntro,omitempty"`
	Charset            string `json:"charset,omitempty"`
	Type               int    `json:"type"`
	Group              int64  `json:"group"`
	WordCount          string `json:"wordCount,omitempty"`
	LatestChapterTitle string `json:"latestChapterTitle,omitempty"`
	LatestChapterTime  int64  `json:"latestChapterTime,omitempty"`
	LastCheckTime      int64  `json:"lastCheckTime,omitempty"`
	LastCheckCount     int    `json:"lastCheckCount,omitempty"`
	TotalChapterNum    int    `json:"totalChapterNum,omitempty"`
	CanUpdate          bool   `json:"canUpdate"`
	Order              int    `json:"order,omitempty"`
	OriginOrder        int    `json:"originOrder"`
	Variable           string `json:"variable,omitempty"`
	SyncTime           int64  `json:"syncTime,omitempty"`
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
		// Legado Book.canUpdate 默认 true（刷新书架时更新书籍信息）。
		CanUpdate: true,
		InfoHTML:  b.InfoHTML,
		TocHTML:   b.TocHTML,
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
