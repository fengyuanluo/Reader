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
