package repositories

import (
	"context"
	"errors"
	"testing"

	"reader/internal/domain/book"
)

func TestBookRepositoryLifecycleAndProgress(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	b := testBook("https://example.test/book/1")

	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := books.FindByURL(ctx, b.BookURL)
	if err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
	bookID := bookIDFromURL(b.BookURL)
	if got.Name != b.Name || got.Author != b.Author {
		t.Fatalf("book = %#v", got)
	}

	if err := books.SaveProgress(ctx, bookID, book.BookProgress{DurChapterTitle: "c2", DurChapterIndex: 2, DurChapterPos: 30, DurChapterTime: 99}); err != nil {
		t.Fatalf("SaveProgress() error = %v", err)
	}
	got, err = books.FindByID(ctx, bookID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.DurChapterIndex != 2 || got.DurChapterPos != 30 || got.DurChapterTime != 99 {
		t.Fatalf("progress = %#v", got)
	}

	list, total, err := books.List(ctx, book.BookFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("list total=%d len=%d", total, len(list))
	}

	if err := books.Delete(ctx, bookID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := books.FindByID(ctx, bookID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByID after delete error = %v, want ErrNotFound", err)
	}
}

func TestBookRepositoryPersistsRuntimeHTMLAndDownloadURLs(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	b := testBook("https://example.test/book/file")
	b.InfoHTML = "<html>info</html>"
	b.TocHTML = "<html>toc</html>"
	b.DownloadURLs = []string{"https://cdn.example.test/a.epub", "https://cdn.example.test/b.epub"}

	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := books.FindByURL(ctx, b.BookURL)
	if err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
	if got.InfoHTML != b.InfoHTML || got.TocHTML != b.TocHTML {
		t.Fatalf("book html = %#v", got)
	}
	if len(got.DownloadURLs) != 2 || got.DownloadURLs[0] != b.DownloadURLs[0] || got.DownloadURLs[1] != b.DownloadURLs[1] {
		t.Fatalf("download urls = %#v", got.DownloadURLs)
	}
}

func TestChapterRepositoryReplaceListFindAndContent(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	chapters := NewChapterRepository(db)
	b := testBook("https://example.test/book/1")
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	bookID := bookIDFromURL(b.BookURL)

	input := []*book.BookChapter{
		{BookURL: b.BookURL, URL: "https://example.test/c/1", Title: "c1", Index: 0},
		{BookURL: b.BookURL, URL: "https://example.test/c/2", Title: "c2", Index: 1, IsVIP: true},
	}
	if err := chapters.ReplaceByBook(ctx, bookID, input); err != nil {
		t.Fatalf("ReplaceByBook() error = %v", err)
	}
	list, err := chapters.ListByBook(ctx, bookID)
	if err != nil {
		t.Fatalf("ListByBook() error = %v", err)
	}
	if len(list) != 2 || list[1].Title != "c2" || !list[1].IsVIP {
		t.Fatalf("chapters = %#v", list)
	}
	found, err := chapters.FindByIndex(ctx, bookID, 1)
	if err != nil {
		t.Fatalf("FindByIndex() error = %v", err)
	}
	if found.URL != "https://example.test/c/2" {
		t.Fatalf("found = %#v", found)
	}

	if err := chapters.SaveContent(ctx, bookID, 1, "hello"); err != nil {
		t.Fatalf("SaveContent() error = %v", err)
	}
	content, ok, err := chapters.GetContent(ctx, bookID, 1)
	if err != nil {
		t.Fatalf("GetContent() error = %v", err)
	}
	if !ok || content != "hello" {
		t.Fatalf("content=%q ok=%v", content, ok)
	}
}

func TestDeletingBookCleansChaptersAndContentCache(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	chapters := NewChapterRepository(db)
	b := testBook("https://example.test/book/1")
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	bookID := bookIDFromURL(b.BookURL)
	if err := chapters.ReplaceByBook(ctx, bookID, []*book.BookChapter{{BookURL: b.BookURL, URL: "https://example.test/c/1", Title: "c1", Index: 0}}); err != nil {
		t.Fatalf("ReplaceByBook() error = %v", err)
	}
	if err := chapters.SaveContent(ctx, bookID, 0, "hello"); err != nil {
		t.Fatalf("SaveContent() error = %v", err)
	}

	if err := books.Delete(ctx, bookID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertCount(t, db, `SELECT count(*) FROM chapters WHERE book_id = ?`, 0, bookID)
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE owner_type = 'book' AND owner_id = ?`, 0, bookID)
}

func testBook(bookURL string) *book.Book {
	return &book.Book{
		BookURL:            bookURL,
		TocURL:             bookURL + "/toc",
		Origin:             "https://example.test",
		OriginName:         "Example Source",
		Name:               "Example Book",
		Author:             "Example Author",
		Type:               0,
		WordCount:          "100万",
		LatestChapterTitle: "c1",
	}
}
