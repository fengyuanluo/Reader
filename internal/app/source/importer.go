package sourceapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// 导入更新策略，取值与 OpenAPI ImportOptions.updatePolicy 枚举一致；空串等同 lastUpdateTime。
const (
	UpdatePolicyLastUpdateTime = "lastUpdateTime"
	UpdatePolicyAlways         = "always"
	UpdatePolicySkipExisting   = "skipExisting"
)

// ValidUpdatePolicy 判断更新策略是否合法（空串视为默认 lastUpdateTime）。
func ValidUpdatePolicy(policy string) bool {
	switch policy {
	case "", UpdatePolicyLastUpdateTime, UpdatePolicyAlways, UpdatePolicySkipExisting:
		return true
	default:
		return false
	}
}

// utf8BOM 是 Windows 工具导出 JSON 时常带的 UTF-8 字节序标记。
var utf8BOM = []byte("\xef\xbb\xbf")

var (
	// ErrInvalidImportPayload 标记请求体本身不合法（非 JSON、空 payload、格式不支持），由 handler 映射为 400。
	ErrInvalidImportPayload = errors.New("invalid import payload")
	// ErrImportFetcherUnavailable 标记导入链路缺少抓取器（服务装配不完整）。
	ErrImportFetcherUnavailable = errors.New("fetcher is required for sourceUrls import")
)

type ImportInput struct {
	Payload   []byte
	RemoteURL string
	Options   ImportOptions
	// Depth 是远程 sourceUrls 递归展开的当前深度；0 表示用户直接发起的导入。
	// 内部递归调用时逐层 +1，超过 maxRemoteDepth 后记 RemoteError 并停止，
	// 防止自引用/相互引用的远程文档造成无限抓取循环（对齐 Legado 仅一层远程展开的语义）。
	Depth int
}

const (
	// maxRemoteDepth 限制远程 sourceUrls 文档的递归展开层数；超出即停止展开。
	maxRemoteDepth = 3
	// maxSourceURLsPerDocument 限制单个 sourceUrls 文档允许携带的 URL 数量，超限截断。
	maxSourceURLsPerDocument = 1000
	// importTotalTimeout 是单次导入请求远程抓取链的整体预算，防止多个慢 URL 让请求长时间挂起。
	importTotalTimeout = 2 * time.Minute
)

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
	// onSaved 在书源写入成功后回调，用于清理依赖书源的缓存（如探索分类缓存）。
	onSaved func(bookSourceURL string)
}

func NewImporter(repo source.BookSourceRepository) *Importer {
	return &Importer{repo: repo}
}

func NewImporterWithFetcher(repo source.BookSourceRepository, fetcher fetch.Fetcher) *Importer {
	return &Importer{repo: repo, fetcher: fetcher}
}

// WithSavedCallback 注册书源写入成功后的回调，参数为书源地址。
func (i *Importer) WithSavedCallback(callback func(bookSourceURL string)) *Importer {
	i.onSaved = callback
	return i
}

func (i *Importer) Import(ctx context.Context, input ImportInput) (*ImportResult, error) {
	if input.RemoteURL != "" {
		return i.importSourceURLs(ctx, []string{input.RemoteURL}, input, input.Depth)
	}
	var raw json.RawMessage
	// 与 Legado 导入一致容忍开头 BOM；远程拉取的内容递归进入本函数，同样覆盖。
	if err := json.Unmarshal(bytes.TrimPrefix(input.Payload, utf8BOM), &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImportPayload, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrInvalidImportPayload)
	}
	switch raw[0] {
	case '{':
		if urls, ok := sourceURLs(raw); ok {
			return i.importSourceURLs(ctx, urls, input, input.Depth)
		}
		return i.importObject(ctx, raw, input.Options)
	case '[':
		return i.importArray(ctx, raw, input.Options)
	default:
		return nil, fmt.Errorf("%w: only BookSource object/array or sourceUrls document are supported", ErrInvalidImportPayload)
	}
}

func (i *Importer) importSourceURLs(ctx context.Context, urls []string, input ImportInput, depth int) (*ImportResult, error) {
	if i.fetcher == nil {
		return nil, ErrImportFetcherUnavailable
	}
	if depth >= maxRemoteDepth {
		// 展开层数达到上限，不再抓取；按 Legado 语义远程文档最多展开数层。
		return nil, fmt.Errorf("remote sourceUrls recursion exceeded %d levels", maxRemoteDepth)
	}
	// 整体预算：无论 URL 列表多长，远程抓取链必须在预算内收敛，
	// 防止无人值守请求被慢站点串行挂起几十分钟。
	fetchCtx, cancel := context.WithTimeout(ctx, importTotalTimeout)
	defer cancel()
	result := &ImportResult{}
	for _, remoteURL := range urls {
		if fetchCtx.Err() != nil {
			result.RemoteErrors = append(result.RemoteErrors, RemoteError{URL: remoteURL, Error: "import deadline exceeded"})
			continue
		}
		reqURL := remoteURL
		requestWithoutUA := false
		if strings.HasSuffix(remoteURL, "#requestWithoutUA") {
			reqURL = strings.TrimSuffix(remoteURL, "#requestWithoutUA")
			requestWithoutUA = true
		}
		resp, err := i.fetcher.Fetch(fetchCtx, fetch.Request{URL: reqURL, RequestWithoutUA: requestWithoutUA})
		if err != nil {
			result.RemoteErrors = append(result.RemoteErrors, RemoteError{URL: remoteURL, Error: err.Error()})
			continue
		}
		partial, err := i.Import(fetchCtx, ImportInput{Payload: resp.Body, Options: input.Options, Depth: depth + 1})
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
	if len(doc.SourceURLs) > maxSourceURLsPerDocument {
		// 超限截断，防止单个恶意文档驱使服务端发起海量抓取。
		doc.SourceURLs = doc.SourceURLs[:maxSourceURLsPerDocument]
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
	// pendingSourceIDs 记录本批次已决定写入的 sourceId：SaveMany 的 upsert 第一键是
	// source_id，同批次两条同 sourceId 不同地址的条目会在事务内静默互覆，
	// 必须让后者按“已存在”处理，保证 result 与最终落库状态一致。
	pendingSourceIDs := map[string]*source.BookSource{}
	toSave := make([]*source.BookSource, 0, len(entries))
	for _, entry := range entries {
		src, err := source.ParseBookSource(entry)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, ImportItemResult{Status: ImportStatusFailed, Error: err.Error()})
			continue
		}
		save, err := i.planImport(ctx, result, src, options, pending, pendingSourceIDs)
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
		i.notifySaved(toSave)
	}
	return result, nil
}

// notifySaved 在整批写入成功后回调每个书源地址，供缓存失效使用。
func (i *Importer) notifySaved(sources []*source.BookSource) {
	if i.onSaved == nil {
		return
	}
	seen := map[string]struct{}{}
	for _, src := range sources {
		url := strings.TrimSpace(src.BookSourceURL)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		i.onSaved(url)
	}
}

// planImport 决定单条书源的导入状态并应用导入选项，记入 result；需要写库时返回 true。
// pending 记录本批次已决定写入的书源（按地址），使同批次重复地址按“已存在”处理；
// pendingSourceIDs 按 sourceId 记录，防止同批次同 sourceId 不同地址的条目静默互覆。
func (i *Importer) planImport(ctx context.Context, result *ImportResult, src *source.BookSource, options ImportOptions, pending, pendingSourceIDs map[string]*source.BookSource) (bool, error) {
	existing := pending[src.BookSourceURL]
	if existing == nil {
		// 同批次已有相同 sourceId 的条目视作已存在（SaveMany 按 source_id upsert，后者会覆盖前者）。
		existing = pendingSourceIDs[src.SourceID]
	}
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
	pendingSourceIDs[src.SourceID] = src
	return true, nil
}

// shouldSkip 判断默认（lastUpdateTime）策略下是否跳过导入。
// 对齐 Legado ImportBookSourceViewModel.comparisonSource：`source.lastUpdateTime < it.lastUpdateTime`
// 才会默认选中更新，因此两者相等（含大量书源默认的 0）时跳过；如需强制覆盖请使用 updatePolicy=always。
func shouldSkip(existing, incoming *source.BookSource, policy string) bool {
	switch policy {
	case UpdatePolicyAlways:
		return false
	case UpdatePolicySkipExisting:
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
		groups := source.SplitGroups(incoming.BookSourceGroup)
		for _, group := range source.SplitGroups(options.GroupName) {
			if !containsGroup(groups, group) {
				groups = append(groups, group)
			}
		}
		incoming.BookSourceGroup = strings.Join(groups, ",")
		return
	}
	incoming.BookSourceGroup = options.GroupName
}

func containsGroup(groups []string, want string) bool {
	for _, group := range groups {
		if group == want {
			return true
		}
	}
	return false
}
