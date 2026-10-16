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
