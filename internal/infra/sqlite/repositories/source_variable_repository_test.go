package repositories

import (
	"context"
	"database/sql"
	"testing"

	readersqlite "reader/internal/infra/sqlite"

	_ "modernc.org/sqlite"
)

func openSourceVariableTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestSourceVariableRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	repo := NewSourceVariableRepository(openSourceVariableTestDB(t))
	const url = "https://example.test#tag"

	if _, ok, err := repo.GetVariable(ctx, url, "loginHeader"); err != nil || ok {
		t.Fatalf("GetVariable(missing) ok=%v err=%v", ok, err)
	}
	if err := repo.PutVariable(ctx, url, "loginHeader", `{"A":"1"}`); err != nil {
		t.Fatalf("PutVariable() error = %v", err)
	}
	if err := repo.PutVariable(ctx, url, "loginHeader", `{"A":"2"}`); err != nil {
		t.Fatalf("PutVariable(overwrite) error = %v", err)
	}
	if err := repo.PutVariable(ctx, "https://other.test", "loginHeader", "other"); err != nil {
		t.Fatalf("PutVariable(other) error = %v", err)
	}
	got, ok, err := repo.GetVariable(ctx, url, "loginHeader")
	if err != nil || !ok || got != `{"A":"2"}` {
		t.Fatalf("GetVariable() = %q ok=%v err=%v", got, ok, err)
	}
	// 空字符串是合法值，与"不存在"区分。
	if err := repo.PutVariable(ctx, url, "v_empty", ""); err != nil {
		t.Fatalf("PutVariable(empty) error = %v", err)
	}
	if got, ok, err := repo.GetVariable(ctx, url, "v_empty"); err != nil || !ok || got != "" {
		t.Fatalf("GetVariable(empty) = %q ok=%v err=%v", got, ok, err)
	}
	if err := repo.DeleteVariable(ctx, url, "loginHeader"); err != nil {
		t.Fatalf("DeleteVariable() error = %v", err)
	}
	if err := repo.DeleteVariable(ctx, url, "loginHeader"); err != nil {
		t.Fatalf("DeleteVariable(missing) error = %v", err)
	}
	if _, ok, err := repo.GetVariable(ctx, url, "loginHeader"); err != nil || ok {
		t.Fatalf("GetVariable(after delete) ok=%v err=%v", ok, err)
	}
	if got, ok, _ := repo.GetVariable(ctx, "https://other.test", "loginHeader"); !ok || got != "other" {
		t.Fatalf("other source affected: %q ok=%v", got, ok)
	}
}

// 删除书源时 BookSourceRepository 在同一事务里清理 source_variables，重新导入不继承旧状态。
func TestSourceVariableRepositoryClearedBySourceDelete(t *testing.T) {
	ctx := context.Background()
	db := openSourceVariableTestDB(t)
	sources := NewBookSourceRepository(db)
	vars := NewSourceVariableRepository(db)
	src := testSource("https://example.test", "Example")
	if err := sources.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := vars.PutVariable(ctx, src.BookSourceURL, "loginInfo", "secret"); err != nil {
		t.Fatalf("PutVariable() error = %v", err)
	}
	if err := sources.Delete(ctx, src.SourceID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := sources.Save(ctx, testSource("https://example.test", "Example")); err != nil {
		t.Fatalf("re-Save() error = %v", err)
	}
	if _, ok, err := vars.GetVariable(ctx, src.BookSourceURL, "loginInfo"); err != nil || ok {
		t.Fatalf("variable survived source delete: ok=%v err=%v", ok, err)
	}
}
