package contentapp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	replaceapp "reader/internal/app/replace"
	"reader/internal/domain/book"
	"reader/internal/domain/replace"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
)

func openContentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := readersqlite.Open(filepath.Join(t.TempDir(), "reader.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// 对齐 Legado BookContent.kt:52-56：index+1 章节不存在时，nextChapterUrl 回退到第 0 章 URL；
// 旧实现直接把 nextChapterUrl 置空，末章里依赖它的正文规则会失效。
func TestAppServiceFallsBackToFirstChapterAsNextChapterURL(t *testing.T) {
	ctx := context.Background()
	db := openContentTestDB(t)
	books := repositories.NewBookRepository(db)
	chapters := repositories.NewChapterRepository(db)
	sources := repositories.NewBookSourceRepository(db)

	src := &source.BookSource{
		SourceID:       "src-content",
		BookSourceURL:  "https://site.example.test/source",
		BookSourceName: "Source",
		Enabled:        true,
		RuleContent: rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{
			Content: `@js:nextChapterUrl`,
		}},
	}
	if err := sources.Save(ctx, src); err != nil {
		t.Fatalf("save source: %v", err)
	}
	bookValue := &book.Book{
		BookURL:    "https://site.example.test/book/1",
		Origin:     src.BookSourceURL,
		OriginName: src.BookSourceName,
		Name:       "斗破苍穹",
	}
	if err := books.Save(ctx, bookValue); err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(bookValue.BookURL)
	chapterList := []*book.BookChapter{
		{BookURL: bookValue.BookURL, URL: "https://site.example.test/c/1", Title: "第一章", BaseURL: "https://site.example.test/toc/1", Index: 0},
		{BookURL: bookValue.BookURL, URL: "https://site.example.test/c/2", Title: "第二章", BaseURL: "https://site.example.test/toc/1", Index: 1},
	}
	if err := chapters.ReplaceByBook(ctx, bookID, chapterList); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}

	fetcher := &fakeContentFetcher{}
	service := NewService(chapters, fetcher, script.NewRuntime(script.Policy{Timeout: time.Second}))
	app := NewAppService(books, chapters, sources, service)

	// 末章：回退到第 0 章 URL。
	got, _, err := app.GetChapterContent(ctx, bookID, 1)
	if err != nil {
		t.Fatalf("GetChapterContent() error = %v", err)
	}
	if got.Content != "https://site.example.test/c/1" {
		t.Fatalf("last chapter nextChapterUrl = %q, want first chapter url", got.Content)
	}

	// 非末章：使用下一章 URL。
	got, _, err = app.GetChapterContent(ctx, bookID, 0)
	if err != nil {
		t.Fatalf("GetChapterContent() error = %v", err)
	}
	if got.Content != "https://site.example.test/c/2" {
		t.Fatalf("nextChapterUrl = %q, want second chapter url", got.Content)
	}
}

// 缓存命中同样要应用用户替换规则（R-045：缓存保存净化前正文）。
func TestAppServiceAppliesProcessorToCachedContent(t *testing.T) {
	ctx := context.Background()
	db := openContentTestDB(t)
	books := repositories.NewBookRepository(db)
	chapters := repositories.NewChapterRepository(db)
	sources := repositories.NewBookSourceRepository(db)

	src := &source.BookSource{
		SourceID:       "src-content",
		BookSourceURL:  "https://site.example.test/source",
		BookSourceName: "Source",
		Enabled:        true,
		RuleContent:    rule.Flexible[rule.ContentRule]{Value: &rule.ContentRule{Content: ".content@text"}},
	}
	if err := sources.Save(ctx, src); err != nil {
		t.Fatalf("save source: %v", err)
	}
	bookValue := &book.Book{BookURL: "https://site.example.test/book/1", Origin: src.BookSourceURL, OriginName: src.BookSourceName, Name: "斗破苍穹"}
	if err := books.Save(ctx, bookValue); err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(bookValue.BookURL)
	if err := chapters.ReplaceByBook(ctx, bookID, []*book.BookChapter{
		{BookURL: bookValue.BookURL, URL: "https://site.example.test/c/1", Title: "第一章", BaseURL: "https://site.example.test/toc/1", Index: 0},
	}); err != nil {
		t.Fatalf("replace chapters: %v", err)
	}
	if err := chapters.SaveContent(ctx, bookID, 0, "第一章\n广告\n正文"); err != nil {
		t.Fatalf("save content: %v", err)
	}

	service := NewService(chapters, &fakeContentFetcher{}, nil).WithProcessor(newTestProcessor())
	app := NewAppService(books, chapters, sources, service)

	got, _, err := app.GetChapterContent(ctx, bookID, 0)
	if err != nil {
		t.Fatalf("GetChapterContent() error = %v", err)
	}
	if got.Content != "　　正文" {
		t.Fatalf("content = %q, want user replace rule applied to cache", got.Content)
	}
}

// newTestProcessor 构造应用一条内容域“广告”替换规则的 Processor（复用 content_test.go 的 fake 仓库）。
func newTestProcessor() *replaceapp.Processor {
	return replaceapp.NewProcessor(&fakeContentReplaceRepo{
		content: []*replace.ReplaceRule{{ID: 10, Name: "ad", Pattern: "广告", Replacement: "", IsEnabled: true, ScopeContent: true}},
	})
}
