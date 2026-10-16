package sourceapp

import (
	"context"
	"database/sql"
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
