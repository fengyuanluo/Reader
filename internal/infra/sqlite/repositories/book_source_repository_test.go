package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"reader/internal/domain/source"
	readersqlite "reader/internal/infra/sqlite"
)

// openRepoTestDB 走生产同款 Open（文件库 + 每连接外键/busy_timeout/WAL）。
func openRepoTestDB(t *testing.T) *sql.DB {
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

func TestBookSourceRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	src := testSource("https://example.test", "Example")
	src.EventListener = true
	src.CustomButton = true

	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	byID, err := repo.FindByID(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if byID.BookSourceURL != src.BookSourceURL || byID.BookSourceName != src.BookSourceName || !byID.EventListener || !byID.CustomButton {
		t.Fatalf("FindByID() = %#v", byID)
	}
	byURL, err := repo.FindByURL(ctx, src.BookSourceURL)
	if err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
	if byURL.SourceID != src.SourceID {
		t.Fatalf("FindByURL SourceID = %q, want %q", byURL.SourceID, src.SourceID)
	}
}

func TestBookSourceRepositoryListFilters(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	first := testSource("https://one.example.test", "Alpha")
	first.BookSourceGroup = "default,group-a"
	first.BookSourceComment = "fast source"
	first.Enabled = true
	first.EnabledExplore = false
	second := testSource("https://two.example.test", "Beta")
	second.BookSourceGroup = "group-b"
	second.Enabled = false
	second.EnabledExplore = true
	if err := repo.SaveMany(ctx, []*source.BookSource{first, second}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}

	enabled := true
	page, err := repo.List(ctx, source.BookSourceFilter{Keyword: "fast", Group: "group-a", Enabled: &enabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].SourceID != first.SourceID {
		t.Fatalf("filtered page = %#v", page)
	}

	explore := true
	page, err = repo.List(ctx, source.BookSourceFilter{EnabledExplore: &explore, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List explore enabled error = %v", err)
	}
	if page.Total != 1 || page.Items[0].SourceID != second.SourceID {
		t.Fatalf("explore filtered page = %#v", page)
	}
}

func TestBookSourceRepositoryUpdateAndReorder(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	src := testSource("https://example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := repo.UpdateEnabled(ctx, src.SourceID, false); err != nil {
		t.Fatalf("UpdateEnabled() error = %v", err)
	}
	if err := repo.UpdateExploreEnabled(ctx, src.SourceID, false); err != nil {
		t.Fatalf("UpdateExploreEnabled() error = %v", err)
	}
	if err := repo.Reorder(ctx, []source.SourceOrder{{SourceID: src.SourceID, CustomOrder: 42}}); err != nil {
		t.Fatalf("Reorder() error = %v", err)
	}

	got, err := repo.FindByID(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Enabled || got.EnabledExplore || got.CustomOrder != 42 {
		t.Fatalf("updated source = %#v", got)
	}
}

func TestBookSourceRepositoryDeleteCleansSourceData(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	src := testSource("https://example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO source_variables(book_source_url, name, value) VALUES (?, ?, ?)`, src.BookSourceURL, "token", "secret"); err != nil {
		t.Fatalf("insert source variable: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value) VALUES (?, ?, ?, ?, ?)`, "cache-1", "source", "source", src.BookSourceURL, "value"); err != nil {
		t.Fatalf("insert cache: %v", err)
	}

	if err := repo.Delete(ctx, src.SourceID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.FindByID(ctx, src.SourceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByID after delete error = %v, want ErrNotFound", err)
	}
	assertCount(t, db, `SELECT count(*) FROM source_variables WHERE book_source_url = ?`, 0, src.BookSourceURL)
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE owner_type = 'source' AND owner_id = ?`, 0, src.BookSourceURL)
}

func TestBookSourceSaveChangesURLKeepingSourceID(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	src := testSource("https://old.example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO source_variables(book_source_url, name, value) VALUES (?, 'token', 'v')`, src.BookSourceURL); err != nil {
		t.Fatalf("insert source variable: %v", err)
	}
	renamed := testSource("https://new.example.test", "Renamed")
	renamed.SourceID = src.SourceID
	if err := repo.Save(ctx, renamed); err != nil {
		t.Fatalf("Save(renamed) error = %v", err)
	}
	got, err := repo.FindByID(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.BookSourceURL != "https://new.example.test" || got.BookSourceName != "Renamed" {
		t.Fatalf("renamed source = %#v", got)
	}
	assertCount(t, db, `SELECT count(*) FROM book_sources`, 1)
	// 对齐 Legado：改地址等同删除旧书源，旧地址的书源变量随之清理。
	assertCount(t, db, `SELECT count(*) FROM source_variables WHERE book_source_url = ?`, 0, "https://old.example.test")
}

func TestBookSourceSaveConflicts(t *testing.T) {
	cases := []struct {
		name     string
		incoming func(a, b *source.BookSource) *source.BookSource
		wantErr  error
	}{
		{
			name: "same sourceId moves onto another source's url",
			incoming: func(a, b *source.BookSource) *source.BookSource {
				src := testSource(b.BookSourceURL, "Clash")
				src.SourceID = a.SourceID
				return src
			},
			wantErr: source.ErrConflict,
		},
		{
			name: "same url with new sourceId overwrites",
			incoming: func(a, b *source.BookSource) *source.BookSource {
				src := testSource(a.BookSourceURL, "Overwrite")
				src.SourceID = "src_custom"
				return src
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openRepoTestDB(t)
			repo := NewBookSourceRepository(db)
			a := testSource("https://a.example.test", "A")
			b := testSource("https://b.example.test", "B")
			if err := repo.SaveMany(ctx, []*source.BookSource{a, b}); err != nil {
				t.Fatalf("SaveMany() error = %v", err)
			}
			err := repo.Save(ctx, tc.incoming(a, b))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Save() error = %v, want %v", err, tc.wantErr)
			}
			assertCount(t, db, `SELECT count(*) FROM book_sources`, 2)
		})
	}
}

func TestBookSourceSaveManyRollsBackOnConflict(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	a := testSource("https://a.example.test", "A")
	b := testSource("https://b.example.test", "B")
	if err := repo.SaveMany(ctx, []*source.BookSource{a, b}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	fresh := testSource("https://c.example.test", "C")
	clash := testSource(b.BookSourceURL, "Clash")
	clash.SourceID = a.SourceID
	if err := repo.SaveMany(ctx, []*source.BookSource{fresh, clash}); !errors.Is(err, source.ErrConflict) {
		t.Fatalf("SaveMany() error = %v, want ErrConflict", err)
	}
	if _, err := repo.FindByURL(ctx, fresh.BookSourceURL); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source before conflict was not rolled back: %v", err)
	}
}

func TestBookSourceScanRejectsCorruptRuleJSON(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	src := testSource("https://example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE book_sources SET rule_toc = '' WHERE source_id = ?`, src.SourceID); err != nil {
		t.Fatalf("blank rule_toc: %v", err)
	}
	if _, err := repo.FindByID(ctx, src.SourceID); err != nil {
		t.Fatalf("FindByID() with empty rule column error = %v", err)
	}
	if _, err := db.Exec(`UPDATE book_sources SET rule_search = '{broken' WHERE source_id = ?`, src.SourceID); err != nil {
		t.Fatalf("corrupt rule_search: %v", err)
	}
	_, err := repo.FindByID(ctx, src.SourceID)
	if err == nil || !strings.Contains(err.Error(), "rule_search") || !strings.Contains(err.Error(), src.SourceID) {
		t.Fatalf("FindByID() error = %v, want corrupt rule_search error", err)
	}
}

func TestBookSourceListEscapesLikeWildcards(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	percent := testSource("https://percent.example.test", "100%好书")
	plain := testSource("https://plain.example.test", "1000好书")
	underscore := testSource("https://underscore.example.test", "U")
	underscore.BookSourceGroup = "a_b"
	lookalike := testSource("https://lookalike.example.test", "L")
	lookalike.BookSourceGroup = "axb,other"
	if err := repo.SaveMany(ctx, []*source.BookSource{percent, plain, underscore, lookalike}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	cases := []struct {
		name   string
		filter source.BookSourceFilter
		want   string
	}{
		{name: "percent keyword", filter: source.BookSourceFilter{Keyword: "100%"}, want: percent.SourceID},
		{name: "underscore keyword", filter: source.BookSourceFilter{Keyword: "a_b"}, want: underscore.SourceID},
		{name: "underscore group", filter: source.BookSourceFilter{Group: "a_b"}, want: underscore.SourceID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := repo.List(ctx, tc.filter)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if page.Total != 1 || page.Items[0].SourceID != tc.want {
				t.Fatalf("List(%+v) = %d items, want only %s", tc.filter, page.Total, tc.want)
			}
		})
	}
}

// TestBookSourceSaveManyBatchConflicts 覆盖 S11-04 的批内冲突预检：双 ON CONFLICT
// upsert 在批内 sourceId/地址交错时会把 URL 改写到他人地址，必须整批 ErrConflict。
func TestBookSourceSaveManyBatchConflicts(t *testing.T) {
	cases := []struct {
		name  string
		batch func(a, b *source.BookSource) []*source.BookSource
	}{
		{
			name: "batch two sources swap urls",
			batch: func(a, b *source.BookSource) []*source.BookSource {
				// 先落库 A/B，再一批把 A 改到 B 地址、B 改到 A 地址：upsert 的
				// UNIQUE(book_source_url) 冲突分支会互相改写 sourceId。
				ra := testSource("https://a.example.test", "A")
				ra.SourceID = a.SourceID
				ra.BookSourceURL = "https://b.example.test"
				rb := testSource("https://a.example.test", "B")
				rb.SourceID = b.SourceID
				return []*source.BookSource{ra, rb}
			},
		},
		{
			name: "batch two sources share target url",
			batch: func(a, b *source.BookSource) []*source.BookSource {
				ra := testSource("https://shared.example.test", "A")
				ra.SourceID = a.SourceID
				rb := testSource("https://shared.example.test", "B")
				rb.SourceID = b.SourceID
				return []*source.BookSource{ra, rb}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openRepoTestDB(t)
			repo := NewBookSourceRepository(db)
			a := testSource("https://a.example.test", "A")
			b := testSource("https://b.example.test", "B")
			if err := repo.SaveMany(ctx, []*source.BookSource{a, b}); err != nil {
				t.Fatalf("SaveMany() setup error = %v", err)
			}
			if err := repo.SaveMany(ctx, tc.batch(a, b)); !errors.Is(err, source.ErrConflict) {
				t.Fatalf("SaveMany() error = %v, want ErrConflict", err)
			}
			// 整批回滚：原 A/B 两个源不变，也没有多出任何行。
			assertCount(t, db, `SELECT count(*) FROM book_sources`, 2)
		})
	}
}

// TestBookSourceSaveManyKeepsDataWhenOldURLReusedByBatch 覆盖 S11-04 的换址删除保护：
// 批内源 A 的旧地址恰为本批源 B 的新地址时，A 的换址删除不得清掉 B 刚写入的变量与缓存。
func TestBookSourceSaveManyKeepsDataWhenOldURLReusedByBatch(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	a := testSource("https://old.example.test", "A")
	b := testSource("https://b.example.test", "B")
	if err := repo.SaveMany(ctx, []*source.BookSource{a, b}); err != nil {
		t.Fatalf("SaveMany() setup error = %v", err)
	}
	// B 的变量与缓存先写入旧地址（模拟 B 历史数据），本批把 B 改到 A 的旧地址。
	if _, err := db.Exec(`INSERT INTO source_variables(book_source_url, name, value) VALUES ('https://old.example.test', 'token', 'v')`); err != nil {
		t.Fatalf("insert source variable: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value) VALUES ('cache-b', 'source', 'source', 'https://old.example.test', 'v')`); err != nil {
		t.Fatalf("insert cache: %v", err)
	}
	bNew := testSource("https://old.example.test", "B")
	bNew.SourceID = b.SourceID
	aNew := testSource("https://a-new.example.test", "A")
	aNew.SourceID = a.SourceID
	if err := repo.SaveMany(ctx, []*source.BookSource{aNew, bNew}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	// B 的新地址（原 A 旧地址）上的数据不被误删。
	assertCount(t, db, `SELECT count(*) FROM source_variables WHERE book_source_url = 'https://old.example.test'`, 1)
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE owner_type = 'source' AND owner_id = 'https://old.example.test'`, 1)
}

// TestBookSourceStreamAllPagesWithoutOverlapOrGap 覆盖 S00-03 的 keyset 流式分页：
// 同名同序书源（同名同 custom_order）翻页不重不漏，游标含 source_id 决胜。
func TestBookSourceStreamAllPagesWithoutOverlapOrGap(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	// 同 custom_order 同名书源 60 个：旧 OFFSET 按 (custom_order, name) 排序时
	// 同键行在页边界可能重复/丢失，keyset 必须靠 source_id 决胜。
	batch := make([]*source.BookSource, 0, 60)
	for i := range 60 {
		src := testSource(fmt.Sprintf("https://s%d.example.test", i), "Same")
		src.CustomOrder = 1
		batch = append(batch, src)
	}
	if err := repo.SaveMany(ctx, batch); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	seen := map[string]int{}
	if err := repo.StreamAll(ctx, source.BookSourceFilter{}, func(src *source.BookSource) error {
		seen[src.SourceID]++
		return nil
	}); err != nil {
		t.Fatalf("StreamAll() error = %v", err)
	}
	if len(seen) != 60 {
		t.Fatalf("StreamAll() returned %d unique sources, want 60", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("source %s visited %d times, want 1", id, count)
		}
	}
}

// TestListAllUsesStreamAllWhenAvailable 覆盖 S00-03 的类型断言路径：支持 StreamAll 的
// 仓储走流式且不触发分页 List；不支持的仓储回退分页。
func TestListAllUsesStreamAllWhenAvailable(t *testing.T) {
	repo := NewBookSourceRepository(openRepoTestDB(t))
	ctx := context.Background()
	if err := repo.SaveMany(ctx, []*source.BookSource{
		testSource("https://a.example.test", "A"),
		testSource("https://b.example.test", "B"),
	}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	items, err := source.ListAll(ctx, repo, source.BookSourceFilter{})
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ListAll() returned %d sources, want 2", len(items))
	}
}

func testSource(url, name string) *source.BookSource {
	src := &source.BookSource{
		BookSourceURL:    url,
		BookSourceName:   name,
		BookSourceType:   0,
		CustomOrder:      1,
		Enabled:          true,
		EnabledExplore:   true,
		EnabledCookieJar: true,
		SearchURL:        url + "/search",
	}
	if err := src.Normalize(); err != nil {
		panic(err)
	}
	return src
}

func assertCount(t *testing.T, db *sql.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count query error: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}
