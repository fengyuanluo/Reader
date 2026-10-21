package repositories

import (
	"context"
	"testing"

	"reader/internal/domain/book"
)

// SaveTitle 把正文规则解析出的标题写回章节表（R-046：重启后不再退回目录标题）。
func TestChapterRepositorySaveTitle(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	chapters := NewChapterRepository(db)
	b := testBook("https://example.test/book/1")
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	bookID := bookIDFromURL(b.BookURL)
	if err := chapters.ReplaceByBook(ctx, bookID, []*book.BookChapter{
		{BookURL: b.BookURL, URL: "https://example.test/c/1", Title: "第一章", Index: 0},
	}); err != nil {
		t.Fatalf("ReplaceByBook() error = %v", err)
	}

	if err := chapters.SaveTitle(ctx, bookID, 0, "第一章 正文标题"); err != nil {
		t.Fatalf("SaveTitle() error = %v", err)
	}
	got, err := chapters.FindByIndex(ctx, bookID, 0)
	if err != nil {
		t.Fatalf("FindByIndex() error = %v", err)
	}
	if got.Title != "第一章 正文标题" {
		t.Fatalf("title = %q", got.Title)
	}

	// 章节不存在时不应报错（标题写回事后重跑是幂等的）。
	if err := chapters.SaveTitle(ctx, bookID, 9, "不存在的章节"); err != nil {
		t.Fatalf("SaveTitle() for missing chapter error = %v", err)
	}
}

// TestDeleteContentByOwner 覆盖 S16-01：删除某 bookID 的全部章节正文缓存，
// fetch 换源后清理被淘汰源的缓存。
func TestDeleteContentByOwner(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	chapters := NewChapterRepository(db)
	bookID := "book_abc"
	if err := chapters.SaveContent(ctx, bookID, 0, "第一章内容"); err != nil {
		t.Fatalf("SaveContent() error = %v", err)
	}
	if err := chapters.SaveContent(ctx, bookID, 1, "第二章内容"); err != nil {
		t.Fatalf("SaveContent() error = %v", err)
	}
	if _, ok, err := chapters.GetContent(ctx, bookID, 0); err != nil || !ok {
		t.Fatalf("GetContent() = %v, %v, want hit", ok, err)
	}
	deleted, err := chapters.DeleteContentByOwner(ctx, bookID)
	if err != nil {
		t.Fatalf("DeleteContentByOwner() error = %v", err)
	}
	if deleted != 2 {
		t.Fatalf("DeleteContentByOwner() = %d rows, want 2", deleted)
	}
	if _, ok, err := chapters.GetContent(ctx, bookID, 0); err != nil || ok {
		t.Fatalf("GetContent() after delete = %v, %v, want miss", ok, err)
	}
}

// TestReplaceByBookClearsContentCache 覆盖 S12-03：章节重排后按旧索引缓存的正文
// 会错位命中，ReplaceByBook 必须清掉该书全部正文缓存。
func TestReplaceByBookClearsContentCache(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	chapters := NewChapterRepository(db)
	b := testBook("https://example.test/book/reorder")
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	bookID := bookIDFromURL(b.BookURL)
	if err := chapters.ReplaceByBook(ctx, bookID, []*book.BookChapter{
		{BookURL: b.BookURL, URL: "https://example.test/c/1", Title: "第一章", Index: 0},
		{BookURL: b.BookURL, URL: "https://example.test/c/2", Title: "第二章", Index: 1},
	}); err != nil {
		t.Fatalf("ReplaceByBook() error = %v", err)
	}
	if err := chapters.SaveContent(ctx, bookID, 0, "第一章正文"); err != nil {
		t.Fatalf("SaveContent() error = %v", err)
	}
	// 头部插入一章的新目录：章节重排。
	if err := chapters.ReplaceByBook(ctx, bookID, []*book.BookChapter{
		{BookURL: b.BookURL, URL: "https://example.test/c/0", Title: "序章", Index: 0},
		{BookURL: b.BookURL, URL: "https://example.test/c/1", Title: "第一章", Index: 1},
		{BookURL: b.BookURL, URL: "https://example.test/c/2", Title: "第二章", Index: 2},
	}); err != nil {
		t.Fatalf("ReplaceByBook(new) error = %v", err)
	}
	if _, ok, err := chapters.GetContent(ctx, bookID, 0); err != nil || ok {
		t.Fatalf("GetContent() after reorder = %v, %v, want miss", ok, err)
	}
}
