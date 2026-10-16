package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sourceapp "reader/internal/app/source"
	"reader/internal/domain/source"
	"reader/internal/infra/config"
)

// newTestConfig 构造指向临时目录的最小可用配置。
func newTestConfig(t *testing.T) config.Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":   dir,
		"READER_SQLITE_DSN": filepath.Join(dir, "reader.db"),
		"READER_ENV":        config.EnvTest,
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestOpenCreatesMigratedDatabaseAndClosesOnCleanup 验证装配入口的真实副作用：
// 建库并把迁移跑到最新版本、收紧数据目录与库文件权限、cleanup 后连接不可用。
func TestOpenCreatesMigratedDatabaseAndClosesOnCleanup(t *testing.T) {
	cfg := newTestConfig(t)
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var version int64
	if err := rt.DB.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("查询 schema_migrations: %v", err)
	}
	if version < 4 {
		t.Fatalf("schema version = %d, 迁移未跑到最新", version)
	}
	var dropped int
	if err := rt.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='search_books'").Scan(&dropped); err != nil {
		t.Fatalf("查询 sqlite_master: %v", err)
	}
	if dropped != 0 {
		t.Fatal("search_books 表应已被迁移删除")
	}

	info, err := os.Stat(cfg.SQLiteDSN)
	if err != nil {
		t.Fatalf("stat db: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("db perm = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(cfg.SQLiteDSN))
	if err != nil {
		t.Fatalf("stat data dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data dir perm = %o, want 700", perm)
	}

	cleanup()
	if err := rt.DB.Ping(); err == nil {
		t.Fatal("cleanup 之后数据库仍可用")
	}
}

// TestOpenRuntimePersistsSources 验证装配出的运行时确实把数据落到同一份 SQLite：
// 仓库写入、导入、再读回都命中同一个库文件。
func TestOpenRuntimePersistsSources(t *testing.T) {
	cfg := newTestConfig(t)
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cleanup()

	ctx := context.Background()
	if err := rt.SourceRepo.Save(ctx, &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://example.test",
		BookSourceName: "示例源",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := rt.Importer.Import(ctx, sourceapp.ImportInput{
		Payload: []byte(`[{"bookSourceUrl":"https://other.test","bookSourceName":"另一个源"}]`),
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, err := rt.SourceRepo.FindByURL(ctx, "https://other.test")
	if err != nil {
		t.Fatalf("FindByURL: %v", err)
	}
	if got.BookSourceName != "另一个源" {
		t.Fatalf("bookSourceName = %q", got.BookSourceName)
	}
}

// TestOpenFailsWhenDataPathIsDirectory 验证不可写路径不会静默成功。
func TestOpenFailsWhenDataPathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":   dir,
		"READER_SQLITE_DSN": dir,
		"READER_ENV":        config.EnvTest,
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, _, err := Open(cfg); err == nil {
		t.Fatal("以目录作为 DSN 时 Open 应失败")
	}
}
