package bookapp

import (
	"context"
	"strings"
	"testing"
)

func TestTocServiceScopesChapterVariablesWithoutPollutingBook(t *testing.T) {
	src := testTocSource()
	src.RuleToc.Value.ChapterList = "$.chapters[*]"
	src.RuleToc.Value.ChapterName = "$.title@put:{chapterId:cid}"
	src.RuleToc.Value.ChapterURL = `https://site.example.test/content?session=@get:{session}&cid=@get:{chapterId}`
	book := testTocBook()
	book.Variable = `{"session":"sess-1"}`

	service := NewTocService(&fakeChapterRepo{}, &fakeTocFetcher{}, nil)
	got, err := service.RefreshToc(context.Background(), TocInput{
		Source:  src,
		Book:    book,
		TocHTML: `{"chapters":[{"title":"第一章","cid":"c1"},{"title":"第二章","cid":"c2"}]}`,
	})
	if err != nil {
		t.Fatalf("RefreshToc() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %#v", got)
	}
	if strings.Contains(book.Variable, "chapterId") {
		t.Fatalf("book variable polluted by chapter rule: %q", book.Variable)
	}
	if got[0].URL != "https://site.example.test/content?session=sess-1&cid=c1" || got[1].URL != "https://site.example.test/content?session=sess-1&cid=c2" {
		t.Fatalf("chapter urls = %#v", got)
	}
	if !strings.Contains(got[0].Variable, `"chapterId":"c1"`) || strings.Contains(got[0].Variable, `"session"`) {
		t.Fatalf("chapter0 variable = %q", got[0].Variable)
	}
	if !strings.Contains(got[1].Variable, `"chapterId":"c2"`) || strings.Contains(got[1].Variable, `"session"`) {
		t.Fatalf("chapter1 variable = %q", got[1].Variable)
	}
}
