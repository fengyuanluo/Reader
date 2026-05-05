package book

import (
	"errors"
	"testing"
)

func TestSearchBookToBookPreservesCoreFields(t *testing.T) {
	search := SearchBook{
		BookURL:            "https://example.test/book/1",
		TocURL:             "https://example.test/book/1/toc",
		Origin:             "https://example.test",
		OriginName:         "Example Source",
		Type:               0,
		Name:               "Example Book",
		Author:             "Example Author",
		Kind:               "玄幻",
		CoverURL:           "https://example.test/cover.jpg",
		Intro:              "intro",
		WordCount:          "100万",
		LatestChapterTitle: "Chapter 100",
		OriginOrder:        7,
		Variable:           `{"a":"b"}`,
	}

	book := search.ToBook()

	if book.BookURL != search.BookURL ||
		book.TocURL != search.TocURL ||
		book.Origin != search.Origin ||
		book.OriginName != search.OriginName ||
		book.Type != search.Type ||
		book.Name != search.Name ||
		book.Author != search.Author ||
		book.WordCount != search.WordCount ||
		book.LatestChapterTitle != search.LatestChapterTitle ||
		book.CoverURL != search.CoverURL ||
		book.Intro != search.Intro ||
		book.OriginOrder != search.OriginOrder ||
		book.Variable != search.Variable {
		t.Fatalf("converted book did not preserve fields: %#v", book)
	}
}

func TestBookChapterValidateRejectsNegativeIndex(t *testing.T) {
	chapter := BookChapter{Index: -1}
	if err := chapter.Validate(); !errors.Is(err, ErrInvalidChapterIndex) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidChapterIndex)
	}
}

func TestBookChapterAbsoluteURL(t *testing.T) {
	chapter := BookChapter{
		URL:     "../chapter/1.html",
		BaseURL: "https://example.test/book/toc/index.html",
		Index:   1,
	}
	if got := chapter.AbsoluteURL(); got != "https://example.test/book/chapter/1.html" {
		t.Fatalf("AbsoluteURL() = %q", got)
	}
}

func TestBookChapterVolumeAbsoluteURLUsesBaseURL(t *testing.T) {
	chapter := BookChapter{
		URL:      "第一卷",
		Title:    "第一卷",
		BaseURL:  "https://example.test/book/toc",
		IsVolume: true,
	}
	if got := chapter.AbsoluteURL(); got != "https://example.test/book/toc" {
		t.Fatalf("AbsoluteURL() = %q", got)
	}
}
