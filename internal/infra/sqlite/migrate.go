package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// migration 是一个版本化迁移步骤；apply 与版本记录在同一事务内完成。
type migration struct {
	version int64
	name    string
	apply   func(ctx context.Context, tx *sql.Tx) error
}

// migrations 按版本升序排列。已发布的版本禁止修改，结构变更只能追加新版本。
var migrations = []migration{
	{version: 1, name: "initial schema", apply: execSQL(initialSchema)},
	{version: 2, name: "check_task_items.failure_class", apply: addCheckItemFailureClass},
	{version: 3, name: "drop unused search_books", apply: execSQL(`DROP TABLE IF EXISTS search_books`)},
	{version: 4, name: "debug_events session index", apply: execSQL(`CREATE INDEX IF NOT EXISTS idx_debug_events_session ON debug_events(session_id, event_id)`)},
	// 版本 5：正文缓存改为保存净化前正文，旧库里保存的是已应用用户替换规则的内容，清掉等待重新抓取。
	{version: 5, name: "drop preprocessed content cache", apply: execSQL(`DELETE FROM cache_entries WHERE namespace = 'content'`)},
	{version: 6, name: "books legado metadata columns", apply: addBookLegadoColumns},
	// 版本 7：补齐书源与校验条目的新增列；旧库只有版本 1 的建表结果，缺这些列。
	{version: 7, name: "book source and check item columns", apply: addExtraColumns},
	// 版本 8：cache_entries 按 owner（book）删除正文缓存走 owner 索引，避免全表扫描。
	{version: 8, name: "cache entries owner index", apply: execSQL(`CREATE INDEX IF NOT EXISTS idx_cache_entries_owner ON cache_entries(owner_type, owner_id)`)},
	// 版本 9（S11-02/S11-07）：书架列表 ORDER BY dur_chapter_time 与 cache_entries 过期清理
	// 走索引，避免全表扫描 + TEMP B-TREE。
	{version: 9, name: "books dur_chapter_time and cache expiry indexes", apply: execSQL(`
CREATE INDEX IF NOT EXISTS idx_books_dur_chapter_time ON books(dur_chapter_time DESC);
CREATE INDEX IF NOT EXISTS idx_cache_entries_namespace_expires ON cache_entries(namespace, expires_at);
`)},
}

// Migrate 把数据库升级到最新版本。连接级 PRAGMA（外键、busy_timeout、WAL）由 Open 通过 DSN 设置。
func Migrate(db *sql.DB) error {
	return applyMigrations(context.Background(), db, migrations)
}

func applyMigrations(ctx context.Context, db *sql.DB, steps []migration) error {
	// schema_migrations 建表是 autocommit 语句（不在迁移事务内），_txlock=immediate
	// 只作用于显式事务；并发迁移双方同时建表时仍可能 SQLITE_BUSY，退避重试兜底。
	var createErr error
	for range 5 {
		if _, createErr = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); createErr == nil || !isBusy(createErr) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if createErr != nil {
		return fmt.Errorf("create schema_migrations: %w", createErr)
	}
	var current int64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, step := range steps {
		if step.version <= current {
			continue
		}
		// SQLITE_BUSY 重试（S11-09）：busy_timeout 只等待单条语句；BEGIN IMMEDIATE
		// 排队也可能在 busy_timeout 窗口外（如迁移语句耗时超过窗口）遇到锁竞争，
		// 简单退避重试后重新评估 current，保证并发迁移双方最终都成功。
		var err error
		for retry := range 5 {
			if err = applyMigration(ctx, db, step); err == nil || !isBusy(err) {
				break
			}
			time.Sleep(time.Duration(100*(retry+1)) * time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("migration %d (%s): %w", step.version, step.name, err)
		}
	}
	return nil
}

// isBusy 判断错误是否为 SQLite 锁竞争（SQLITE_BUSY / SQLITE_LOCKED）。
func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}

// isUniqueViolation 判断错误是否为 SQLite UNIQUE / PRIMARY KEY 约束冲突（与
// repositories.isUniqueViolation 同语义，供并发迁移的版本冲突判定使用）。
func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

func applyMigration(ctx context.Context, db *sql.DB, step migration) error {
	// BEGIN IMMEDIATE 全程持写锁（S11-09）：两个进程并发迁移同一库时，先提交者成功，
	// 后启动方在开始时就拿到 busy_timeout 等待，避免双方都读到相同 current 后
	// INSERT INTO schema_migrations 因 version PRIMARY KEY 冲突使后启动方启动失败。
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelLinearizable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := step.apply(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, step.version); err != nil {
		// 并发迁移方已提交同一版本时视为成功：重读确认版本已存在且本步 apply 为幂等 DDL。
		if isUniqueViolation(err) {
			var applied int
			if qerr := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, step.version).Scan(&applied); qerr == nil && applied > 0 {
				return nil
			}
		}
		return err
	}
	return tx.Commit()
}

func execSQL(statements string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, statements)
		return err
	}
}

// addCheckItemFailureClass 兼容旧库：版本机制引入前 failure_class 已可能被探测式补列添加过。
func addCheckItemFailureClass(ctx context.Context, tx *sql.Tx) error {
	return addColumnIfMissing(ctx, tx, "check_task_items", "failure_class", `ALTER TABLE check_task_items ADD COLUMN failure_class TEXT`)
}

// addExtraColumns 补齐版本 1 schema 之后新增的书源与校验条目列。
func addExtraColumns(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		table  string
		column string
		ddl    string
	}{
		{"book_sources", "event_listener", `ALTER TABLE book_sources ADD COLUMN event_listener INTEGER NOT NULL DEFAULT 0`},
		{"book_sources", "custom_button", `ALTER TABLE book_sources ADD COLUMN custom_button INTEGER NOT NULL DEFAULT 0`},
		{"check_task_items", "keyword_used", `ALTER TABLE check_task_items ADD COLUMN keyword_used TEXT`},
		{"check_task_items", "timeout_ms", `ALTER TABLE check_task_items ADD COLUMN timeout_ms INTEGER`},
		{"check_task_items", "stages_json", `ALTER TABLE check_task_items ADD COLUMN stages_json TEXT`},
	}
	for _, column := range columns {
		if err := addColumnIfMissing(ctx, tx, column.table, column.column, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

// addColumnIfMissing 在列缺失时补列；版本机制引入前的旧库已由探测式补列建过同名列。
func addColumnIfMissing(ctx context.Context, tx *sql.Tx, table string, column string, ddl string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, ddl)
	return err
}

// addBookLegadoColumns 补齐 Legado Book.kt 中需要经 API 往返的书籍字段。
// latest_chapter_time/total_chapter_num/sync_time 在初始 schema 已存在，这里只补列。
func addBookLegadoColumns(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"custom_tag", `ALTER TABLE books ADD COLUMN custom_tag TEXT`},
		{"custom_cover_url", `ALTER TABLE books ADD COLUMN custom_cover_url TEXT`},
		{"custom_intro", `ALTER TABLE books ADD COLUMN custom_intro TEXT`},
		{"charset", `ALTER TABLE books ADD COLUMN charset TEXT`},
		{"book_group", `ALTER TABLE books ADD COLUMN book_group INTEGER NOT NULL DEFAULT 0`},
		{"last_check_time", `ALTER TABLE books ADD COLUMN last_check_time INTEGER NOT NULL DEFAULT 0`},
		{"last_check_count", `ALTER TABLE books ADD COLUMN last_check_count INTEGER NOT NULL DEFAULT 0`},
		{"can_update", `ALTER TABLE books ADD COLUMN can_update INTEGER NOT NULL DEFAULT 1`},
		{"book_order", `ALTER TABLE books ADD COLUMN book_order INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range columns {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('books') WHERE name = ?`, column.name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

// initialSchema 是版本 1 的建表语句，已发布后不再修改；新结构变更只追加迁移版本。
const initialSchema = `
CREATE TABLE IF NOT EXISTS book_sources (
  source_id TEXT NOT NULL UNIQUE,
  book_source_url TEXT NOT NULL UNIQUE,
  book_source_name TEXT NOT NULL,
  book_source_group TEXT,
  book_source_type INTEGER NOT NULL DEFAULT 0,
  book_url_pattern TEXT,
  custom_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  enabled_explore INTEGER NOT NULL DEFAULT 1,
  event_listener INTEGER NOT NULL DEFAULT 0,
  custom_button INTEGER NOT NULL DEFAULT 0,
  js_lib TEXT,
  enabled_cookie_jar INTEGER NOT NULL DEFAULT 1,
  concurrent_rate TEXT,
  header TEXT,
  login_url TEXT,
  login_ui TEXT,
  login_check_js TEXT,
  cover_decode_js TEXT,
  book_source_comment TEXT,
  variable_comment TEXT,
  last_update_time INTEGER NOT NULL DEFAULT 0,
  respond_time INTEGER NOT NULL DEFAULT 180000,
  weight INTEGER NOT NULL DEFAULT 0,
  explore_url TEXT,
  explore_screen TEXT,
  rule_explore TEXT,
  search_url TEXT,
  rule_search TEXT,
  rule_book_info TEXT,
  rule_toc TEXT,
  rule_content TEXT,
  rule_review TEXT,
  raw_json TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_book_sources_enabled ON book_sources(enabled);
CREATE INDEX IF NOT EXISTS idx_book_sources_enabled_explore ON book_sources(enabled_explore);
CREATE INDEX IF NOT EXISTS idx_book_sources_custom_order ON book_sources(custom_order);

CREATE TABLE IF NOT EXISTS books (
  book_id TEXT NOT NULL PRIMARY KEY,
  book_url TEXT NOT NULL UNIQUE,
  toc_url TEXT,
  origin TEXT,
  origin_name TEXT,
  name TEXT NOT NULL,
  author TEXT NOT NULL,
  kind TEXT,
  cover_url TEXT,
  intro TEXT,
  type INTEGER NOT NULL DEFAULT 0,
  word_count TEXT,
  latest_chapter_title TEXT,
  info_html TEXT,
  toc_html TEXT,
  download_urls TEXT,
  latest_chapter_time INTEGER NOT NULL DEFAULT 0,
  total_chapter_num INTEGER NOT NULL DEFAULT 0,
  dur_chapter_title TEXT,
  dur_chapter_index INTEGER NOT NULL DEFAULT 0,
  dur_chapter_pos INTEGER NOT NULL DEFAULT 0,
  dur_chapter_time INTEGER NOT NULL DEFAULT 0,
  origin_order INTEGER NOT NULL DEFAULT 0,
  variable TEXT,
  sync_time INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS search_books (
  book_url TEXT NOT NULL PRIMARY KEY,
  origin TEXT NOT NULL,
  origin_name TEXT,
  type INTEGER NOT NULL DEFAULT 0,
  name TEXT NOT NULL,
  author TEXT NOT NULL,
  kind TEXT,
  cover_url TEXT,
  intro TEXT,
  word_count TEXT,
  latest_chapter_title TEXT,
  toc_url TEXT,
  variable TEXT,
  origin_order INTEGER NOT NULL DEFAULT 0,
  respond_time INTEGER NOT NULL DEFAULT -1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chapters (
  book_id TEXT NOT NULL,
  book_url TEXT NOT NULL,
  url TEXT NOT NULL,
  title TEXT NOT NULL,
  is_volume INTEGER NOT NULL DEFAULT 0,
  base_url TEXT,
  chapter_index INTEGER NOT NULL,
  is_vip INTEGER NOT NULL DEFAULT 0,
  is_pay INTEGER NOT NULL DEFAULT 0,
  resource_url TEXT,
  tag TEXT,
  word_count TEXT,
  variable TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(book_id, url),
  UNIQUE(book_id, chapter_index),
  FOREIGN KEY(book_id) REFERENCES books(book_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS replace_rules (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  rule_group TEXT,
  pattern TEXT NOT NULL,
  replacement TEXT NOT NULL DEFAULT '',
  scope TEXT,
  scope_title INTEGER NOT NULL DEFAULT 0,
  scope_content INTEGER NOT NULL DEFAULT 1,
  exclude_scope TEXT,
  is_enabled INTEGER NOT NULL DEFAULT 1,
  is_regex INTEGER NOT NULL DEFAULT 1,
  timeout_millisecond INTEGER NOT NULL DEFAULT 3000,
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS cookies (
  domain TEXT NOT NULL,
  name TEXT NOT NULL,
  value TEXT NOT NULL,
  path TEXT,
  expires_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(domain, name)
);

CREATE TABLE IF NOT EXISTS source_variables (
  book_source_url TEXT NOT NULL,
  name TEXT NOT NULL,
  value TEXT,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(book_source_url, name)
);

CREATE TABLE IF NOT EXISTS cache_entries (
  cache_key TEXT NOT NULL PRIMARY KEY,
  namespace TEXT NOT NULL,
  owner_type TEXT,
  owner_id TEXT,
  value BLOB,
  content_type TEXT,
  expires_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS debug_sessions (
  session_id TEXT NOT NULL PRIMARY KEY,
  source_id TEXT NOT NULL,
  mode TEXT NOT NULL,
  status TEXT NOT NULL,
  keyword TEXT,
  url TEXT,
  trace_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TEXT
);

CREATE TABLE IF NOT EXISTS debug_events (
  event_id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  stage TEXT,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  elapsed_ms INTEGER NOT NULL DEFAULT 0,
  trace_id TEXT,
  data_json TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(session_id) REFERENCES debug_sessions(session_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS check_tasks (
  task_id TEXT NOT NULL PRIMARY KEY,
  status TEXT NOT NULL,
  total INTEGER NOT NULL DEFAULT 0,
  finished INTEGER NOT NULL DEFAULT 0,
  trace_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TEXT
);

CREATE TABLE IF NOT EXISTS check_task_items (
  task_id TEXT NOT NULL,
  source_id TEXT NOT NULL,
  book_source_url TEXT,
  book_source_name TEXT,
  status TEXT NOT NULL,
  failure_code TEXT,
  failure_class TEXT,
  message TEXT,
  respond_time INTEGER,
  keyword_used TEXT,
  timeout_ms INTEGER,
  stages_json TEXT,
  trace_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(task_id, source_id),
  FOREIGN KEY(task_id) REFERENCES check_tasks(task_id) ON DELETE CASCADE
);
`
