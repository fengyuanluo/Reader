package sourceapp

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"reader/internal/domain/source"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
)

// recordingLimiter 记录 RemoveSource 的调用键，供断言。
type recordingLimiter struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingLimiter) RemoveSource(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, key)
}

func (r *recordingLimiter) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func openManagerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := readersqlite.Open(t.TempDir() + "/manager.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestManagerDeleteRemovesLimiterKey 验证删除书源后按 bookSourceUrl 清理限流信号量。
func TestManagerDeleteRemovesLimiterKey(t *testing.T) {
	db := openManagerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	limiter := &recordingLimiter{}
	m := NewManager(repo).WithLimiter(limiter)
	src := &source.BookSource{BookSourceURL: "https://del.example.test", BookSourceName: "Del"}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if _, err := m.Create(context.Background(), src); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.Delete(context.Background(), src.SourceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got := limiter.keys()
	if len(got) != 1 || got[0] != "https://del.example.test" {
		t.Fatalf("RemoveSource calls = %#v, want [https://del.example.test]", got)
	}
}

// TestManagerUpdateRenameRemovesOldLimiterKey 验证改 bookSourceUrl 时移除旧限流键。
func TestManagerUpdateRenameRemovesOldLimiterKey(t *testing.T) {
	db := openManagerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	limiter := &recordingLimiter{}
	m := NewManager(repo).WithLimiter(limiter)
	src := &source.BookSource{BookSourceURL: "https://old.example.test", BookSourceName: "Old"}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	created, err := m.Create(context.Background(), src)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated := &source.BookSource{BookSourceURL: "https://new.example.test", BookSourceName: "New"}
	if _, err := m.Update(context.Background(), created.SourceID, updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := limiter.keys()
	if len(got) != 1 || got[0] != "https://old.example.test" {
		t.Fatalf("RemoveSource calls = %#v, want [https://old.example.test]", got)
	}
}

// TestManagerUpdateSameURLKeepsLimiterKey 验证 URL 未变时不误清限流键。
func TestManagerUpdateSameURLKeepsLimiterKey(t *testing.T) {
	db := openManagerTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	limiter := &recordingLimiter{}
	m := NewManager(repo).WithLimiter(limiter)
	src := &source.BookSource{BookSourceURL: "https://same.example.test", BookSourceName: "Same"}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	created, err := m.Create(context.Background(), src)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated := &source.BookSource{BookSourceURL: "https://same.example.test", BookSourceName: "Renamed"}
	if _, err := m.Update(context.Background(), created.SourceID, updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := limiter.keys(); len(got) != 0 {
		t.Fatalf("RemoveSource calls = %#v, want none", got)
	}
}
