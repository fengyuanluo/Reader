package repositories

import (
	"context"
	"errors"
	"strings"
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

// TestBookRepositoryPersistsLegadoMetadata 覆盖 R-017：Legado Book 字段需要落库并读回。
func TestBookRepositoryPersistsLegadoMetadata(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	b := testBook("https://example.test/book/meta")
	b.CustomTag = "我的标签"
	b.CustomCoverURL = "https://example.test/custom.jpg"
	b.CustomIntro = "自定义简介"
	b.Charset = "GBK"
	b.Group = 7
	b.LatestChapterTime = 1700000000
	b.LastCheckTime = 1700000001
	b.LastCheckCount = 3
	b.TotalChapterNum = 1200
	b.CanUpdate = false
	b.Order = 5
	b.SyncTime = 1700000002

	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := books.FindByURL(ctx, b.BookURL)
	if err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
	if got.CustomTag != b.CustomTag || got.CustomCoverURL != b.CustomCoverURL || got.CustomIntro != b.CustomIntro ||
		got.Charset != b.Charset || got.Group != b.Group || got.LatestChapterTime != b.LatestChapterTime ||
		got.LastCheckTime != b.LastCheckTime || got.LastCheckCount != b.LastCheckCount || got.TotalChapterNum != b.TotalChapterNum ||
		got.CanUpdate != b.CanUpdate || got.Order != b.Order || got.SyncTime != b.SyncTime {
		t.Fatalf("book = %#v", got)
	}

	// 覆盖保存后仍然保持新值（列已加入 ON CONFLICT 更新列表）。
	b.CanUpdate = true
	b.Group = 9
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() update error = %v", err)
	}
	got, err = books.FindByURL(ctx, b.BookURL)
	if err != nil {
		t.Fatalf("FindByURL() after update error = %v", err)
	}
	if !got.CanUpdate || got.Group != 9 {
		t.Fatalf("book after update = %#v", got)
	}
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

// UpdateInfo 只更新信息列：阅读进度（dur_*）与自定义列不被信息刷新覆盖（C-019）。
func TestBookRepositoryUpdateInfoKeepsProgressColumns(t *testing.T) {
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	ctx := context.Background()
	b := &book.Book{BookURL: "https://site.example.test/book/1", TocURL: "https://site.example.test/book/1/toc", Name: "旧名", Author: "甲", Origin: "https://site.example.test/source"}
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := books.SaveProgress(ctx, bookIDFromURL(b.BookURL), book.BookProgress{DurChapterTitle: "第十章", DurChapterIndex: 9, DurChapterPos: 100, DurChapterTime: 123}); err != nil {
		t.Fatalf("SaveProgress() error = %v", err)
	}
	updated := &book.Book{
		BookURL: b.BookURL, Name: "新名", Author: "乙", Origin: b.Origin,
		Intro: "新简介", TotalChapterNum: 1200, LatestChapterTitle: "终章",
		InfoHTML: "<html>info</html>", TocHTML: "<html>toc</html>",
	}
	if err := books.UpdateInfo(ctx, updated); err != nil {
		t.Fatalf("UpdateInfo() error = %v", err)
	}
	got, err := books.FindByID(ctx, bookIDFromURL(b.BookURL))
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Name != "新名" || got.Intro != "新简介" || got.TotalChapterNum != 1200 || got.InfoHTML != "<html>info</html>" {
		t.Fatalf("info columns not updated: %+v", got)
	}
	if got.DurChapterTitle != "第十章" || got.DurChapterIndex != 9 || got.DurChapterPos != 100 {
		t.Fatalf("progress columns overwritten by UpdateInfo: %+v", got)
	}
}

// 章节批量写失败时回滚：事务内 DELETE 不生效，旧章节保留（C-017）。
func TestReplaceByBookRollsBackOnInvalidChapter(t *testing.T) {
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
		{BookURL: "https://example.test/book/1", URL: "https://example.test/c/1", Title: "c1", Index: 0},
	}); err != nil {
		t.Fatalf("initial ReplaceByBook() error = %v", err)
	}
	// 无效章节（负 index）触发 Validate 失败，事务回滚。
	err := chapters.ReplaceByBook(ctx, bookID, []*book.BookChapter{
		{BookURL: b.BookURL, URL: "https://example.test/c/2", Title: "c2", Index: -1},
	})
	if err == nil {
		t.Fatal("ReplaceByBook() error = nil, want validation failure")
	}
	list, err2 := chapters.ListByBook(ctx, bookID)
	if err2 != nil {
		t.Fatalf("ListByBook() error = %v", err2)
	}
	if len(list) != 1 || list[0].Title != "c1" {
		t.Fatalf("chapters after rollback = %#v, want old chapter kept", list)
	}
}

// TestBookRepositoryScanRejectsCorruptDownloadURLs 覆盖 S11-06：download_urls 损坏时
// FindByID/FindByURL 返回明确错误，而不是静默得到 nil DownloadURLs。
func TestBookRepositoryScanRejectsCorruptDownloadURLs(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	b := testBook("https://example.test/book/bad")
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE books SET download_urls = '{broken' WHERE book_url = ?`, b.BookURL); err != nil {
		t.Fatalf("corrupt download_urls: %v", err)
	}
	_, err := books.FindByURL(ctx, b.BookURL)
	if err == nil || !strings.Contains(err.Error(), "download_urls") {
		t.Fatalf("FindByURL() error = %v, want corrupt download_urls error", err)
	}
}

// TestBookRepositoryListMetaSkipsLargeColumns 覆盖 S11-01：ListMeta 不读 info_html/
// toc_html/download_urls 大列，其余元数据列与 List 一致。
func TestBookRepositoryListMetaSkipsLargeColumns(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	b := testBook("https://example.test/book/meta")
	b.InfoHTML = "<html>info</html>"
	b.TocHTML = "<html>toc</html>"
	b.DownloadURLs = []string{"https://example.test/a.epub"}
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	items, total, err := books.ListMeta(ctx, book.BookFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListMeta() error = %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("ListMeta total=%d len=%d", total, len(items))
	}
	got := items[0]
	if got.BookURL != b.BookURL || got.Name != b.Name || got.Author != b.Author || got.LatestChapterTitle != b.LatestChapterTitle {
		t.Fatalf("ListMeta row = %#v", got)
	}
	if got.InfoHTML != "" || got.TocHTML != "" || len(got.DownloadURLs) != 0 {
		t.Fatalf("ListMeta loaded large columns: info=%q toc=%q urls=%v", got.InfoHTML, got.TocHTML, got.DownloadURLs)
	}
	// List 保持全量行为不变。
	full, _, err := books.List(ctx, book.BookFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if full[0].InfoHTML != b.InfoHTML || full[0].TocHTML != b.TocHTML {
		t.Fatalf("List() should keep full columns: %#v", full[0])
	}
}

// TestBookRepositoryUpdateTocStatsWritesConditionalColumns 覆盖 UpdateTocStats 的
// 条件写入语义：Variable 仅在 VariableChanged 时写入，dur_* 仅在 DurChapterChanged 时写入。
func TestBookRepositoryUpdateTocStatsWritesConditionalColumns(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	b := testBook("https://example.test/book/stats")
	b.Variable = "old-var"
	if err := books.Save(ctx, b); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	bookID := bookIDFromURL(b.BookURL)
	// 只写目录统计，不改 variable 与 dur_*。
	if err := books.UpdateTocStats(ctx, bookID, book.TocStats{
		TotalChapterNum: 42, LastCheckTime: 111, LastCheckCount: 3,
		LatestChapterTime: 222, LatestChapterTitle: "c42",
	}); err != nil {
		t.Fatalf("UpdateTocStats() error = %v", err)
	}
	got, err := books.FindByID(ctx, bookID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.TotalChapterNum != 42 || got.LastCheckTime != 111 || got.LastCheckCount != 3 || got.LatestChapterTime != 222 || got.LatestChapterTitle != "c42" {
		t.Fatalf("stats = %#v", got)
	}
	if got.Variable != "old-var" || got.DurChapterIndex != 0 {
		t.Fatalf("stats overwrote unrelated columns: variable=%q dur=%d", got.Variable, got.DurChapterIndex)
	}
	// VariableChanged 与 DurChapterChanged 时写入。
	if err := books.UpdateTocStats(ctx, bookID, book.TocStats{
		TotalChapterNum: 43, Variable: "new-var", VariableChanged: true,
		DurChapterTitle: "c5", DurChapterIndex: 5, DurChapterPos: 10, DurChapterTime: 500, DurChapterChanged: true,
	}); err != nil {
		t.Fatalf("UpdateTocStats(changed) error = %v", err)
	}
	got, err = books.FindByID(ctx, bookID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Variable != "new-var" || got.DurChapterTitle != "c5" || got.DurChapterIndex != 5 || got.DurChapterPos != 10 || got.DurChapterTime != 500 {
		t.Fatalf("changed stats = %#v", got)
	}
	// 行不存在时不创建。
	if err := books.UpdateTocStats(ctx, bookIDFromURL("https://example.test/book/missing"), book.TocStats{TotalChapterNum: 1}); err == nil {
		t.Fatalf("UpdateTocStats(missing) error = nil, want ErrNotFound")
	}
}

// TestBookRepositoryMigrationIndexes 覆盖 S11-02：dur_chapter_time 索引存在且列表查询
// 不再全表扫描 + TEMP B-TREE。
func TestBookRepositoryMigrationIndexes(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	books := NewBookRepository(db)
	for i := range 10 {
		b := testBook("https://example.test/book/" + string(rune('a'+i)))
		b.DurChapterTime = int64(i)
		if err := books.Save(ctx, b); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	var indexes int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_books_dur_chapter_time'`).Scan(&indexes); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	if indexes != 1 {
		t.Fatalf("idx_books_dur_chapter_time missing")
	}
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT book_url FROM books ORDER BY dur_chapter_time DESC LIMIT 5`)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan explain: %v", err)
		}
		if !strings.Contains(detail, "idx_books_dur_chapter_time") {
			t.Fatalf("query plan does not use index: %s", detail)
		}
		if strings.Contains(detail, "TEMP B-TREE") {
			t.Fatalf("query plan materializes sort: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
}
