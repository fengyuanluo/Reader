package sourceapp

import (
	"context"
	"testing"

	"reader/internal/infra/sqlite/repositories"
)

func TestImporterImportsArrayWithPartialFailures(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporter(repo)

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`[
		{"bookSourceUrl":"https://one.example.test","bookSourceName":"One"},
		{"bookSourceName":"Missing URL"},
		{"bookSourceUrl":"https://two.example.test","bookSourceName":"Two"}
	]`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Added != 2 || result.Failed != 1 || len(result.Items) != 3 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := repo.FindByURL(ctx, "https://one.example.test"); err != nil {
		t.Fatalf("FindByURL one error = %v", err)
	}
	if _, err := repo.FindByURL(ctx, "https://two.example.test"); err != nil {
		t.Fatalf("FindByURL two error = %v", err)
	}
}
