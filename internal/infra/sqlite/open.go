package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// 注册 modernc SQLite 驱动；所有生产连接都经由 Open 创建。
	_ "modernc.org/sqlite"
)

// connPragmas 通过 DSN 的 _pragma 参数下发，驱动对连接池中每条新连接都会执行，
// 避免连接级 PRAGMA 只在首条连接生效。busy_timeout 由驱动排在最前，
// 保证切换 WAL 时遇锁等待而不是立即失败；_txlock=immediate 使所有写事务
// 以 BEGIN IMMEDIATE 开启（S11-09）：并发迁移/写方在事务开始时就排队拿写锁，
// 避免延迟升级到写锁时 SQLITE_BUSY。
const connPragmas = "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-65536)"

// Open 打开位于 path 的 SQLite 数据库文件：数据目录 0700、数据库文件 0600（库中含站点 Cookie 等会话凭证），
// 并为每条连接开启外键约束、busy_timeout 与 WAL。path 必须是文件路径，不接受 URI 或 :memory:。
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite data dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create sqlite file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("create sqlite file: %w", err)
	}
	// 已存在的库文件可能是旧版本按 umask 创建的 0644，这里统一收紧。
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("restrict sqlite file mode: %w", err)
	}
	db, err := sql.Open("sqlite", path+connPragmas)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return db, nil
}
