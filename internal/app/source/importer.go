package sourceapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
)

type ImportStatus string

const (
	ImportStatusAdded   ImportStatus = "added"
	ImportStatusUpdated ImportStatus = "updated"
	ImportStatusSkipped ImportStatus = "skipped"
	ImportStatusFailed  ImportStatus = "failed"
)

type ImportInput struct {
	Payload   []byte
	RemoteURL string
	Options   ImportOptions
}

type ImportOptions struct {
	KeepExistingName    bool
	KeepExistingGroup   bool
	KeepExistingEnabled bool
	GroupMode           string
	GroupName           string
	UpdatePolicy        string
}

type ImportResult struct {
	Added        int
	Updated      int
	Skipped      int
	Failed       int
	Items        []ImportItemResult
	RemoteErrors []RemoteError
}

type RemoteError struct {
	URL   string
	Error string
}

type ImportItemResult struct {
	SourceID       string
	BookSourceURL  string
	BookSourceName string
	Status         ImportStatus
	Error          string
}

type Importer struct {
	repo    source.BookSourceRepository
	fetcher fetch.Fetcher
}

func NewImporter(repo source.BookSourceRepository) *Importer {
	return &Importer{repo: repo}
}

func NewImporterWithFetcher(repo source.BookSourceRepository, fetcher fetch.Fetcher) *Importer {
	return &Importer{repo: repo, fetcher: fetcher}
}

func (i *Importer) Import(ctx context.Context, input ImportInput) (*ImportResult, error) {
	if input.RemoteURL != "" {
		return i.importSourceURLs(ctx, []string{input.RemoteURL}, input.Options)
	}
	var raw json.RawMessage
	if err := json.Unmarshal(input.Payload, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("empty import payload")
	}
	switch raw[0] {
	case '{':
		if urls, ok := sourceURLs(raw); ok {
			return i.importSourceURLs(ctx, urls, input.Options)
		}
		return i.importObject(ctx, raw, input.Options)
	case '[':
		return i.importArray(ctx, raw, input.Options)
	default:
		return nil, errors.New("unsupported import payload")
	}
}

func (i *Importer) importSourceURLs(ctx context.Context, urls []string, options ImportOptions) (*ImportResult, error) {
	if i.fetcher == nil {
		return nil, errors.New("fetcher is required for sourceUrls import")
	}
	result := &ImportResult{}
	for _, remoteURL := range urls {
		reqURL := remoteURL
		requestWithoutUA := false
		if strings.HasSuffix(remoteURL, "#requestWithoutUA") {
			reqURL = strings.TrimSuffix(remoteURL, "#requestWithoutUA")
			requestWithoutUA = true
		}
		resp, err := i.fetcher.Fetch(ctx, fetch.Request{URL: reqURL, RequestWithoutUA: requestWithoutUA})
		if err != nil {
			result.RemoteErrors = append(result.RemoteErrors, RemoteError{URL: remoteURL, Error: err.Error()})
			continue
		}
		partial, err := i.Import(ctx, ImportInput{Payload: resp.Body, Options: options})
		if err != nil {
			result.RemoteErrors = append(result.RemoteErrors, RemoteError{URL: remoteURL, Error: err.Error()})
			continue
		}
		mergeImportResult(result, partial)
	}
	return result, nil
}

func sourceURLs(raw json.RawMessage) ([]string, bool) {
	var doc struct {
		SourceURLs []string `json:"sourceUrls"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}
	return doc.SourceURLs, len(doc.SourceURLs) > 0
}

func mergeImportResult(dst, src *ImportResult) {
	dst.Added += src.Added
	dst.Updated += src.Updated
	dst.Skipped += src.Skipped
	dst.Failed += src.Failed
	dst.Items = append(dst.Items, src.Items...)
	dst.RemoteErrors = append(dst.RemoteErrors, src.RemoteErrors...)
}

func (i *Importer) importObject(ctx context.Context, raw json.RawMessage, options ImportOptions) (*ImportResult, error) {
	src, err := source.ParseBookSource(raw)
	result := &ImportResult{}
	if err != nil {
		result.Failed = 1
		result.Items = append(result.Items, ImportItemResult{Status: ImportStatusFailed, Error: err.Error()})
		return result, nil
	}
	if err := i.importOne(ctx, result, src, options); err != nil {
		return nil, err
	}
	return result, nil
}

func (i *Importer) importArray(ctx context.Context, raw json.RawMessage, options ImportOptions) (*ImportResult, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	result := &ImportResult{}
	for _, entry := range entries {
		src, err := source.ParseBookSource(entry)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, ImportItemResult{Status: ImportStatusFailed, Error: err.Error()})
			continue
		}
		if err := i.importOne(ctx, result, src, options); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (i *Importer) importOne(ctx context.Context, result *ImportResult, src *source.BookSource, options ImportOptions) error {
	existing, err := i.repo.FindByURL(ctx, src.BookSourceURL)
	if err != nil && !errors.Is(err, source.ErrNotFound) {
		return err
	}
	status := ImportStatusAdded
	if existing != nil {
		status = ImportStatusUpdated
		if shouldSkip(existing, src, options.UpdatePolicy) {
			status = ImportStatusSkipped
		} else {
			applyImportOptions(src, existing, options)
		}
	}
	applyGroupOption(src, options)
	if status != ImportStatusSkipped {
		if err := i.repo.Save(ctx, src); err != nil {
			return err
		}
	}
	switch status {
	case ImportStatusAdded:
		result.Added++
	case ImportStatusUpdated:
		result.Updated++
	case ImportStatusSkipped:
		result.Skipped++
	}
	result.Items = append(result.Items, ImportItemResult{
		SourceID:       src.SourceID,
		BookSourceURL:  src.BookSourceURL,
		BookSourceName: src.BookSourceName,
		Status:         status,
	})
	return nil
}

func shouldSkip(existing, incoming *source.BookSource, policy string) bool {
	switch policy {
	case "always":
		return false
	case "skipExisting":
		return true
	default:
		return existing.LastUpdateTime >= incoming.LastUpdateTime
	}
}

func applyImportOptions(incoming, existing *source.BookSource, options ImportOptions) {
	if options.KeepExistingName {
		incoming.BookSourceName = existing.BookSourceName
	}
	if options.KeepExistingGroup {
		incoming.BookSourceGroup = existing.BookSourceGroup
	}
	if options.KeepExistingEnabled {
		incoming.Enabled = existing.Enabled
		incoming.EnabledExplore = existing.EnabledExplore
	}
	incoming.CustomOrder = existing.CustomOrder
}

func applyGroupOption(incoming *source.BookSource, options ImportOptions) {
	if options.GroupName == "" {
		return
	}
	if options.GroupMode == "append" {
		groups := splitGroups(incoming.BookSourceGroup)
		for _, group := range splitGroups(options.GroupName) {
			if !containsGroup(groups, group) {
				groups = append(groups, group)
			}
		}
		incoming.BookSourceGroup = strings.Join(groups, ",")
		return
	}
	incoming.BookSourceGroup = options.GroupName
}

func splitGroups(value string) []string {
	parts := strings.Split(value, ",")
	groups := []string{}
	for _, part := range parts {
		group := strings.TrimSpace(part)
		if group != "" {
			groups = append(groups, group)
		}
	}
	return groups
}

func containsGroup(groups []string, want string) bool {
	for _, group := range groups {
		if group == want {
			return true
		}
	}
	return false
}
