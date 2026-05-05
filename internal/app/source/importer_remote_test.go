package sourceapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"reader/internal/infra/fetcher"
	"reader/internal/infra/sqlite/repositories"
)

func TestImporterImportsRemoteURLAndRequestWithoutUA(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`[{"bookSourceUrl":"https://remote-source.example.test","bookSourceName":"Remote"}]`))
	}))
	defer server.Close()

	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	importer := NewImporterWithFetcher(repo, fetcher.NewHTTPFetcher(fetcher.WithAllowPrivateNetwork(true), fetcher.WithDefaultUserAgent("Reader-Test")))

	result, err := importer.Import(ctx, ImportInput{RemoteURL: server.URL + "#requestWithoutUA"})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if gotUA != "" {
		t.Fatalf("User-Agent = %q, want empty", gotUA)
	}
	if result.Added != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := repo.FindByURL(ctx, "https://remote-source.example.test"); err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
}

func TestImporterRemoteNonJSONResponseReturnsRemoteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	importer := NewImporterWithFetcher(repositories.NewBookSourceRepository(openImporterTestDB(t)), fetcher.NewHTTPFetcher(fetcher.WithAllowPrivateNetwork(true)))
	result, err := importer.Import(context.Background(), ImportInput{RemoteURL: server.URL})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.RemoteErrors) != 1 {
		t.Fatalf("result = %#v", result)
	}
}
