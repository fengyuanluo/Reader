package sourceapp

import (
	"context"
	"errors"
	"testing"

	domainsource "reader/internal/domain/source"
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

// 批量导入单事务落库：任一条写库失败整批回滚，不留下部分导入状态。
func TestImporterArrayRollsBackWholeBatchOnSaveFailure(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	a := &domainsource.BookSource{BookSourceURL: "https://a.example.test", BookSourceName: "A"}
	b := &domainsource.BookSource{BookSourceURL: "https://b.example.test", BookSourceName: "B"}
	if err := repo.SaveMany(ctx, []*domainsource.BookSource{a, b}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	importer := NewImporter(repo)

	_, err := importer.Import(ctx, ImportInput{Payload: []byte(`[
		{"bookSourceUrl":"https://c.example.test","bookSourceName":"C"},
		{"sourceId":"` + a.SourceID + `","bookSourceUrl":"https://b.example.test","bookSourceName":"Clash","lastUpdateTime":1}
	]`)})
	if !errors.Is(err, domainsource.ErrConflict) {
		t.Fatalf("Import() error = %v, want ErrConflict", err)
	}
	if _, err := repo.FindByURL(ctx, "https://c.example.test"); !errors.Is(err, domainsource.ErrNotFound) {
		t.Fatalf("earlier entry persisted despite failed batch: err = %v", err)
	}
}

func TestImporterArrayDuplicateURLInBatchUpdatesEarlierEntry(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporter(repo)

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`[
		{"bookSourceUrl":"https://dup.example.test","bookSourceName":"Old","lastUpdateTime":1},
		{"bookSourceUrl":"https://dup.example.test","bookSourceName":"New","lastUpdateTime":2}
	]`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Added != 1 || result.Updated != 1 {
		t.Fatalf("result = %#v", result)
	}
	got, err := repo.FindByURL(ctx, "https://dup.example.test")
	if err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
	if got.BookSourceName != "New" {
		t.Fatalf("stored name = %q, want New", got.BookSourceName)
	}
}
