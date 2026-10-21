package contentapp

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"reader/internal/domain/book"
)

func TestAppServiceExportTXTWritesMetadataVolumesAndChapters(t *testing.T) {
	bookValue := &book.Book{
		BookURL: "https://site.example.test/book/export",
		Name:    "斗破苍穹",
		Author:  "天蚕土豆",
		Intro:   "少年成长",
	}
	chapters := &exportChapterRepo{
		items: []*book.BookChapter{
			{BookURL: bookValue.BookURL, Title: "第一卷", IsVolume: true, Index: 0},
			{BookURL: bookValue.BookURL, Title: "第一章", Index: 1},
			{BookURL: bookValue.BookURL, Title: "第二章", Index: 2},
		},
		cached:  map[int]string{1: "第一段\n正文", 2: "第二段\n"},
		cacheOK: map[int]bool{1: true, 2: true},
	}
	app := NewAppService(&exportBookRepo{value: bookValue}, chapters, nil, nil)

	var got bytes.Buffer
	stats, err := app.ExportTXT(context.Background(), book.IDFromURL(bookValue.BookURL), &got)
	if err != nil {
		t.Fatalf("ExportTXT() error = %v", err)
	}
	want := "斗破苍穹\n作者：天蚕土豆\n简介：少年成长\n\n第一卷\n\n第一章\n第一段\n正文\n\n第二章\n第二段\n\n"
	if got.String() != want {
		t.Fatalf("export = %q, want %q", got.String(), want)
	}
	if stats.ChapterCount != 3 || stats.ContentChapterCount != 2 || stats.Bytes != int64(got.Len()) {
		t.Fatalf("stats = %#v, bytes=%d", stats, got.Len())
	}
}

func TestAppServiceExportTXTRejectsEmptyOrFileBooks(t *testing.T) {
	bookValue := &book.Book{BookURL: "https://site.example.test/book/export", Name: "文件书", Type: 3}
	app := NewAppService(&exportBookRepo{value: bookValue}, &exportChapterRepo{}, nil, nil)
	if _, err := app.ExportTXT(context.Background(), book.IDFromURL(bookValue.BookURL), &bytes.Buffer{}); !errors.Is(err, ErrExportFileSource) {
		t.Fatalf("file source error = %v", err)
	}

	bookValue.Type = 0
	if _, err := app.ExportTXT(context.Background(), book.IDFromURL(bookValue.BookURL), &bytes.Buffer{}); !errors.Is(err, ErrExportTocEmpty) {
		t.Fatalf("empty toc error = %v", err)
	}
}

func TestAppServiceExportTXTFailsOnEmptyChapter(t *testing.T) {
	bookValue := &book.Book{BookURL: "https://site.example.test/book/export", Name: "书"}
	chapters := &exportChapterRepo{
		items:   []*book.BookChapter{{BookURL: bookValue.BookURL, Title: "第一章", Index: 0}},
		cached:  map[int]string{0: "\n"},
		cacheOK: map[int]bool{0: true},
	}
	app := NewAppService(&exportBookRepo{value: bookValue}, chapters, nil, nil)

	var partial bytes.Buffer
	if _, err := app.ExportTXT(context.Background(), book.IDFromURL(bookValue.BookURL), &partial); !errors.Is(err, ErrContentEmpty) {
		t.Fatalf("empty chapter error = %v", err)
	}
	if partial.Len() == 0 {
		t.Fatal("expected caller-owned writer to contain discarded partial data")
	}
}

type exportBookRepo struct {
	value *book.Book
}

func (r *exportBookRepo) Save(context.Context, *book.Book) error { return nil }

// UpdateInfo 满足 book.BookRepository 接口：导出路径不刷新信息。
func (r *exportBookRepo) UpdateInfo(context.Context, *book.Book) error { return nil }

// UpdateTocStats 满足 book.BookRepository 接口：导出路径不刷新目录元数据。
func (r *exportBookRepo) UpdateTocStats(context.Context, string, book.TocStats) error { return nil }
func (r *exportBookRepo) FindByID(context.Context, string) (*book.Book, error) {
	if r.value == nil {
		return nil, errors.New("book missing")
	}
	copy := *r.value
	return &copy, nil
}
func (r *exportBookRepo) FindByURL(context.Context, string) (*book.Book, error) {
	return r.FindByID(context.Background(), "")
}
func (r *exportBookRepo) List(context.Context, book.BookFilter) ([]*book.Book, int, error) {
	if r.value == nil {
		return nil, 0, nil
	}
	return []*book.Book{r.value}, 1, nil
}
func (r *exportBookRepo) Delete(context.Context, string) error { return nil }
func (r *exportBookRepo) SaveProgress(context.Context, string, book.BookProgress) error {
	return nil
}

type exportChapterRepo struct {
	items   []*book.BookChapter
	cached  map[int]string
	cacheOK map[int]bool
}

func (r *exportChapterRepo) ReplaceByBook(context.Context, string, []*book.BookChapter) error {
	return nil
}
func (r *exportChapterRepo) SaveTitle(context.Context, string, int, string) error { return nil }
func (r *exportChapterRepo) ListByBook(context.Context, string) ([]*book.BookChapter, error) {
	return r.items, nil
}
func (r *exportChapterRepo) FindByIndex(_ context.Context, _ string, index int) (*book.BookChapter, error) {
	for _, item := range r.items {
		if item.Index == index {
			return item, nil
		}
	}
	return nil, errors.New("chapter missing")
}
func (r *exportChapterRepo) SaveContent(_ context.Context, _ string, index int, content string) error {
	if r.cached == nil {
		r.cached = map[int]string{}
	}
	r.cached[index] = content
	r.cacheOK[index] = true
	return nil
}
func (r *exportChapterRepo) GetContent(_ context.Context, _ string, index int) (string, bool, error) {
	return r.cached[index], r.cacheOK[index], nil
}
