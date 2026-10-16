package sourceapp

import (
	"context"
	"testing"

	domainsource "reader/internal/domain/source"
	"reader/internal/infra/sqlite/repositories"
)

func TestImporterSkipsWhenExistingIsNewerByDefault(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	existing := optionSource("https://example.test", "Local", 10)
	if err := repo.Save(ctx, existing); err != nil {
		t.Fatalf("save existing: %v", err)
	}
	importer := NewImporter(repo)

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`{"bookSourceUrl":"https://example.test","bookSourceName":"Remote","lastUpdateTime":5}`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Skipped != 1 || result.Updated != 0 {
		t.Fatalf("result = %#v", result)
	}
	got, _ := repo.FindByURL(ctx, "https://example.test")
	if got.BookSourceName != "Local" {
		t.Fatalf("name = %q, want Local", got.BookSourceName)
	}
}

func TestImporterUpdatesWhenIncomingIsNewerAndKeepsLocalOptions(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	existing := optionSource("https://example.test", "Local", 5)
	existing.BookSourceGroup = "local"
	existing.Enabled = false
	existing.EnabledExplore = false
	existing.CustomOrder = 9
	if err := repo.Save(ctx, existing); err != nil {
		t.Fatalf("save existing: %v", err)
	}
	importer := NewImporter(repo)

	result, err := importer.Import(ctx, ImportInput{
		Payload: []byte(`{"bookSourceUrl":"https://example.test","bookSourceName":"Remote","bookSourceGroup":"remote","lastUpdateTime":10,"enabled":true,"enabledExplore":true}`),
		Options: ImportOptions{
			KeepExistingName:    true,
			KeepExistingGroup:   true,
			KeepExistingEnabled: true,
		},
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("result = %#v", result)
	}
	got, _ := repo.FindByURL(ctx, "https://example.test")
	if got.BookSourceName != "Local" || got.BookSourceGroup != "local" || got.Enabled || got.EnabledExplore || got.CustomOrder != 9 {
		t.Fatalf("updated source = %#v", got)
	}
}

func TestImporterAppliesGroupAppendAndReplace(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporter(repo)

	result, err := importer.Import(ctx, ImportInput{
		Payload: []byte(`{"bookSourceUrl":"https://append.example.test","bookSourceName":"Append","bookSourceGroup":"a"}`),
		Options: ImportOptions{GroupMode: "append", GroupName: "b"},
	})
	if err != nil {
		t.Fatalf("Import append error = %v", err)
	}
	if result.Added != 1 {
		t.Fatalf("append result = %#v", result)
	}
	got, _ := repo.FindByURL(ctx, "https://append.example.test")
	if got.BookSourceGroup != "a,b" {
		t.Fatalf("append group = %q, want a,b", got.BookSourceGroup)
	}

	_, err = importer.Import(ctx, ImportInput{
		Payload: []byte(`{"bookSourceUrl":"https://replace.example.test","bookSourceName":"Replace","bookSourceGroup":"a"}`),
		Options: ImportOptions{GroupMode: "replace", GroupName: "b"},
	})
	if err != nil {
		t.Fatalf("Import replace error = %v", err)
	}
	got, _ = repo.FindByURL(ctx, "https://replace.example.test")
	if got.BookSourceGroup != "b" {
		t.Fatalf("replace group = %q, want b", got.BookSourceGroup)
	}
}

func TestImporterSplitsGroupsWithLegadoSeparators(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporter(repo)

	_, err := importer.Import(ctx, ImportInput{
		Payload: []byte(`{"bookSourceUrl":"https://cn.example.test","bookSourceName":"Cn","bookSourceGroup":"玄幻，都市；仙侠"}`),
		Options: ImportOptions{GroupMode: "append", GroupName: "历史;科幻"},
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	got, _ := repo.FindByURL(ctx, "https://cn.example.test")
	if got.BookSourceGroup != "玄幻,都市,仙侠,历史,科幻" {
		t.Fatalf("group = %q, want 玄幻,都市,仙侠,历史,科幻", got.BookSourceGroup)
	}
}

func TestImporterNotifiesSavedCallbackOnlyForWrittenSources(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	var saved []string
	importer := NewImporter(repo).WithSavedCallback(func(bookSourceURL string) {
		saved = append(saved, bookSourceURL)
	})

	if _, err := importer.Import(ctx, ImportInput{
		Payload: []byte(`[
			{"bookSourceUrl":"https://a.example.test","bookSourceName":"A","lastUpdateTime":10},
			{"bookSourceUrl":"https://b.example.test","bookSourceName":"B","lastUpdateTime":10}
		]`),
	}); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(saved) != 2 || saved[0] != "https://a.example.test" || saved[1] != "https://b.example.test" {
		t.Fatalf("saved = %#v", saved)
	}

	// 相同 lastUpdateTime 默认跳过，不再回调。
	if _, err := importer.Import(ctx, ImportInput{
		Payload: []byte(`{"bookSourceUrl":"https://a.example.test","bookSourceName":"A","lastUpdateTime":10}`),
	}); err != nil {
		t.Fatalf("Import(skip) error = %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("saved = %#v, want no callback for skipped import", saved)
	}
}

func optionSource(url, name string, lastUpdate int64) *domainsource.BookSource {
	src := &domainsource.BookSource{
		BookSourceURL:    url,
		BookSourceName:   name,
		Enabled:          true,
		EnabledExplore:   true,
		EnabledCookieJar: true,
		LastUpdateTime:   lastUpdate,
	}
	if err := src.Normalize(); err != nil {
		panic(err)
	}
	return src
}
