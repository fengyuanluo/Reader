package rule

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type SearchRule struct {
	CheckKeyWord string `json:"checkKeyWord,omitempty"`
	BookList     string `json:"bookList,omitempty"`
	Name         string `json:"name,omitempty"`
	Author       string `json:"author,omitempty"`
	Intro        string `json:"intro,omitempty"`
	Kind         string `json:"kind,omitempty"`
	LastChapter  string `json:"lastChapter,omitempty"`
	UpdateTime   string `json:"updateTime,omitempty"`
	BookURL      string `json:"bookUrl,omitempty"`
	CoverURL     string `json:"coverUrl,omitempty"`
	WordCount    string `json:"wordCount,omitempty"`
}

type ExploreRule struct {
	BookList    string `json:"bookList,omitempty"`
	Name        string `json:"name,omitempty"`
	Author      string `json:"author,omitempty"`
	Intro       string `json:"intro,omitempty"`
	Kind        string `json:"kind,omitempty"`
	LastChapter string `json:"lastChapter,omitempty"`
	UpdateTime  string `json:"updateTime,omitempty"`
	BookURL     string `json:"bookUrl,omitempty"`
	CoverURL    string `json:"coverUrl,omitempty"`
	WordCount   string `json:"wordCount,omitempty"`
}

type BookInfoRule struct {
	Init         string `json:"init,omitempty"`
	Name         string `json:"name,omitempty"`
	Author       string `json:"author,omitempty"`
	Intro        string `json:"intro,omitempty"`
	Kind         string `json:"kind,omitempty"`
	LastChapter  string `json:"lastChapter,omitempty"`
	UpdateTime   string `json:"updateTime,omitempty"`
	CoverURL     string `json:"coverUrl,omitempty"`
	TocURL       string `json:"tocUrl,omitempty"`
	WordCount    string `json:"wordCount,omitempty"`
	CanReName    string `json:"canReName,omitempty"`
	DownloadURLs string `json:"downloadUrls,omitempty"`
}

type TocRule struct {
	PreUpdateJS string `json:"preUpdateJs,omitempty"`
	ChapterList string `json:"chapterList,omitempty"`
	ChapterName string `json:"chapterName,omitempty"`
	ChapterURL  string `json:"chapterUrl,omitempty"`
	FormatJS    string `json:"formatJs,omitempty"`
	IsVolume    string `json:"isVolume,omitempty"`
	IsVIP       string `json:"isVip,omitempty"`
	IsPay       string `json:"isPay,omitempty"`
	UpdateTime  string `json:"updateTime,omitempty"`
	NextTocURL  string `json:"nextTocUrl,omitempty"`
}

type ContentRule struct {
	Content        string `json:"content,omitempty"`
	SubContent     string `json:"subContent,omitempty"`
	Title          string `json:"title,omitempty"`
	NextContentURL string `json:"nextContentUrl,omitempty"`
	WebJS          string `json:"webJs,omitempty"`
	SourceRegex    string `json:"sourceRegex,omitempty"`
	ReplaceRegex   string `json:"replaceRegex,omitempty"`
	ImageStyle     string `json:"imageStyle,omitempty"`
	ImageDecode    string `json:"imageDecode,omitempty"`
	PayAction      string `json:"payAction,omitempty"`
	CallBackJS     string `json:"callBackJs,omitempty"`
}

type Flexible[T any] struct {
	Value *T
	Raw   json.RawMessage
}

func (f *Flexible[T]) UnmarshalJSON(data []byte) error {
	f.Raw = append(f.Raw[:0], data...)
	if bytes.Equal(data, []byte("null")) {
		f.Value = nil
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var encoded string
		if err := json.Unmarshal(data, &encoded); err != nil {
			return err
		}
		data = []byte(encoded)
		f.Raw = append(f.Raw[:0], data...)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode flexible rule: %w", err)
	}
	f.Value = &value
	return nil
}

func (f Flexible[T]) MarshalJSON() ([]byte, error) {
	if len(f.Raw) > 0 {
		return f.Raw, nil
	}
	if f.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(f.Value)
}
