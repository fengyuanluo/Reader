package sourceapp

import (
	"context"
	"errors"
	"testing"

	"reader/internal/domain/fetch"
	"reader/internal/infra/sqlite/repositories"
)

type mockFetcher map[string]mockFetchResult

type mockFetchResult struct {
	body []byte
	err  error
}

func (m mockFetcher) Fetch(_ context.Context, req fetch.Request) (*fetch.Response, error) {
	result, ok := m[req.URL]
	if !ok {
		return nil, errors.New("not found")
	}
	if result.err != nil {
		return nil, result.err
	}
	return &fetch.Response{StatusCode: 200, Body: result.body}, nil
}

func TestImporterImportsSourceURLsAggregate(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporterWithFetcher(repo, mockFetcher{
		"https://ok.example.test/sources.json":  {body: []byte(`[{"bookSourceUrl":"https://source.example.test","bookSourceName":"Source"}]`)},
		"https://bad.example.test/sources.json": {err: errors.New("remote failed")},
	})

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`{"sourceUrls":["https://ok.example.test/sources.json","https://bad.example.test/sources.json"]}`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Added != 1 || len(result.RemoteErrors) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := repo.FindByURL(ctx, "https://source.example.test"); err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
}

func TestImporterRecordsBlockedSourceURL(t *testing.T) {
	ctx := context.Background()
	importer := NewImporterWithFetcher(repositories.NewBookSourceRepository(openImporterTestDB(t)), mockFetcher{
		"http://127.0.0.1/sources.json": {err: fetch.ErrBlocked},
	})

	result, err := importer.Import(ctx, ImportInput{Payload: []byte(`{"sourceUrls":["http://127.0.0.1/sources.json"]}`)})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.RemoteErrors) != 1 || result.RemoteErrors[0].Error == "" {
		t.Fatalf("result = %#v", result)
	}
}
