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
	return i.importEntries(ctx, []json.RawMessage{raw}, options)
}

func (i *Importer) importArray(ctx context.Context, raw json.RawMessage, options ImportOptions) (*ImportResult, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	return i.importEntries(ctx, entries, options)
}

// importEntries 先逐条解析并决定导入状态，再用 SaveMany 单事务批量写入（对齐 Legado
// SourceHelp.insertBookSource 一次性 insert）；写库失败整批回滚，不会留下部分导入状态。
func (i *Importer) importEntries(ctx context.Context, entries []json.RawMessage, options ImportOptions) (*ImportResult, error) {
	result := &ImportResult{}
	pending := map[string]*source.BookSource{}
	toSave := make([]*source.BookSource, 0, len(entries))
	for _, entry := range entries {
		src, err := source.ParseBookSource(entry)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, ImportItemResult{Status: ImportStatusFailed, Error: err.Error()})
			continue
		}
		save, err := i.planImport(ctx, result, src, options, pending)
		if err != nil {
			return nil, err
		}
		if save {
			toSave = append(toSave, src)
		}
	}
	if len(toSave) > 0 {
		if err := i.repo.SaveMany(ctx, toSave); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// planImport 决定单条书源的导入状态并应用导入选项，记入 result；需要写库时返回 true。
// pending 记录本批次已决定写入的书源，使同批次重复地址按“已存在”处理。
func (i *Importer) planImport(ctx context.Context, result *ImportResult, src *source.BookSource, options ImportOptions, pending map[string]*source.BookSource) (bool, error) {
	existing := pending[src.BookSourceURL]
	if existing == nil {
		found, err := i.repo.FindByURL(ctx, src.BookSourceURL)
		if err != nil && !errors.Is(err, source.ErrNotFound) {
			return false, err
		}
		existing = found
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
	if status == ImportStatusSkipped {
		return false, nil
	}
	pending[src.BookSourceURL] = src
	return true, nil
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
