package repositories

import (
	"context"
	"testing"

	"reader/internal/domain/fetch"
)

func TestImageCacheRepositorySaveAndGet(t *testing.T) {
	db := openRepoTestDB(t)
	repo := NewImageCacheRepository(db)
	ctx := context.Background()

	if err := repo.SaveImage(ctx, "https://img.example.test/cover.png", fetch.ImageCacheEntry{Body: []byte("png"), ContentType: "image/png"}); err != nil {
		t.Fatalf("SaveImage() error = %v", err)
	}
	got, ok, err := repo.GetImage(ctx, "https://img.example.test/cover.png")
	if err != nil {
		t.Fatalf("GetImage() error = %v", err)
	}
	if !ok || string(got.Body) != "png" || got.ContentType != "image/png" {
		t.Fatalf("got = %#v ok=%v", got, ok)
	}
}

func TestImageCacheRepositoryExpiry(t *testing.T) {
	db := openRepoTestDB(t)
	repo := NewImageCacheRepository(db)
	ctx := context.Background()
	for _, u := range []string{"https://img.example.test/fresh.png", "https://img.example.test/stale.png", "https://img.example.test/legacy.png"} {
		if err := repo.SaveImage(ctx, u, fetch.ImageCacheEntry{Body: []byte("png"), ContentType: "image/png"}); err != nil {
			t.Fatalf("SaveImage() error = %v", err)
		}
	}
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE namespace = 'image' AND expires_at > datetime('now', '+6 days')`, 3)
	if _, err := db.Exec(`UPDATE cache_entries SET expires_at = datetime('now', '-1 second') WHERE owner_id = ?`, "https://img.example.test/stale.png"); err != nil {
		t.Fatalf("expire entry: %v", err)
	}
	if _, err := db.Exec(`UPDATE cache_entries SET expires_at = NULL WHERE owner_id = ?`, "https://img.example.test/legacy.png"); err != nil {
		t.Fatalf("legacy entry: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value) VALUES ('content:1', 'content', 'book', 'b1', 'text')`); err != nil {
		t.Fatalf("insert content cache: %v", err)
	}

	cases := []struct {
		url    string
		wantOK bool
	}{
		{"https://img.example.test/fresh.png", true},
		{"https://img.example.test/stale.png", false},
		{"https://img.example.test/legacy.png", false},
	}
	for _, tc := range cases {
		if _, ok, err := repo.GetImage(ctx, tc.url); err != nil || ok != tc.wantOK {
			t.Fatalf("GetImage(%s) ok=%v err=%v, want ok=%v", tc.url, ok, err, tc.wantOK)
		}
	}

	deleted, err := repo.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	if deleted != 2 {
		t.Fatalf("DeleteExpired() = %d, want 2", deleted)
	}
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE namespace = 'image'`, 1)
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE namespace = 'content'`, 1)
}
