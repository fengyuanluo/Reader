package sourceapp

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
)

func TestImporterImportsSingleBookSource(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporter(repo)

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`{
		"bookSourceUrl":"https://example.test",
		"bookSourceName":"Example",
		"ruleSearch":{"bookList":".item"}
	}`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Added != 1 || result.Failed != 0 || len(result.Items) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := repo.FindByURL(ctx, "https://example.test"); err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
}

func TestImporterReturnsItemErrorForMissingRequiredFields(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	importer := NewImporter(repositories.NewBookSourceRepository(db))

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`{"bookSourceName":"Example"}`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Failed != 1 || len(result.Items) != 1 || result.Items[0].Error == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestImporterStripsUTF8BOM(t *testing.T) {
	payloads := map[string]string{
		"array":  "\xef\xbb\xbf[{\"bookSourceUrl\":\"https://bom-array.test\",\"bookSourceName\":\"BOM\"}]",
		"object": "\xef\xbb\xbf {\"bookSourceUrl\":\"https://bom-object.test\",\"bookSourceName\":\"BOM\"}",
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			importer := NewImporter(repositories.NewBookSourceRepository(openImporterTestDB(t)))
			result, err := importer.Import(context.Background(), ImportInput{Payload: []byte(payload)})
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			if result.Added != 1 {
				t.Fatalf("Added = %d, want 1; items %+v", result.Added, result.Items)
			}
		})
	}
}

func TestValidUpdatePolicy(t *testing.T) {
	tests := map[string]bool{
		"":               true,
		"lastUpdateTime": true,
		"always":         true,
		"skipExisting":   true,
		"Always":         false,
		"skip":           false,
	}
	for policy, want := range tests {
		if got := ValidUpdatePolicy(policy); got != want {
			t.Fatalf("ValidUpdatePolicy(%q) = %v, want %v", policy, got, want)
		}
	}
}

// TestImporterMarksInvalidPayload 覆盖 R-032：请求体不合法时返回 sentinel，由 handler 映射为 400。
func TestImporterMarksInvalidPayload(t *testing.T) {
	ctx := context.Background()
	importer := NewImporter(repositories.NewBookSourceRepository(openImporterTestDB(t)))

	tests := map[string]string{
		"broken json":  `{"bookSourceUrl":`,
		"number":       `42`,
		"empty string": ``,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importer.Import(ctx, ImportInput{Payload: []byte(payload)})
			if !errors.Is(err, ErrInvalidImportPayload) {
				t.Fatalf("Import() error = %v, want ErrInvalidImportPayload", err)
			}
		})
	}
}

// TestImporterRequiresFetcherForSourceURLs 覆盖远程导入缺少抓取器的装配错误。
func TestImporterRequiresFetcherForSourceURLs(t *testing.T) {
	ctx := context.Background()
	importer := NewImporter(repositories.NewBookSourceRepository(openImporterTestDB(t)))

	_, err := importer.Import(ctx, ImportInput{RemoteURL: "https://example.test/sources.json"})
	if !errors.Is(err, ErrImportFetcherUnavailable) {
		t.Fatalf("Import() error = %v, want ErrImportFetcherUnavailable", err)
	}
}

func openImporterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := readersqlite.Open(filepath.Join(t.TempDir(), "reader.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestImporterSameSourceIDDifferentURLsDedupedInBatch 验证同一批次两条同 sourceId 不同 URL 的条目：
// 第二条按“已存在”处理（updated），最终库里只有一条，result 与落库状态一致（S15-06）。
func TestImporterSameSourceIDDifferentURLsDedupedInBatch(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporter(repo)

	payload := `[
		{"sourceId":"collide","bookSourceUrl":"https://first.example.test","bookSourceName":"First"},
		{"sourceId":"collide","bookSourceUrl":"https://second.example.test","bookSourceName":"Second"}
	]`
	result, err := importer.Import(ctx, ImportInput{Payload: []byte(payload)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %#v", result.Items)
	}
	// 第二条基于第一条的 pending 状态按已存在处理：不再以 added 落库静默互覆。
	if result.Items[0].Status != ImportStatusAdded {
		t.Fatalf("first status = %q, want added", result.Items[0].Status)
	}
	if result.Items[1].Status == ImportStatusAdded {
		t.Fatalf("second status = %q, want not added (pending dedup by sourceId)", result.Items[1].Status)
	}
	count, err := countBookSources(db)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("book source rows = %d, want 1", count)
	}
}

// countBookSources 统计 book_sources 表行数，用于断言最终落库条数。
func countBookSources(db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM book_sources`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
