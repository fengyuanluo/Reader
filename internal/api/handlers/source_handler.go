package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	bookapp "reader/internal/app/book"
	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	debugapp "reader/internal/app/debug"
	discoveryapp "reader/internal/app/discovery"
	replaceapp "reader/internal/app/replace"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/replace"
	"reader/internal/domain/source"
	sqliterepo "reader/internal/infra/sqlite/repositories"
)

type SourceHandler struct {
	generated.Unimplemented
	manager   *sourceapp.Manager
	importer  *sourceapp.Importer
	searcher  Searcher
	discovery DiscoveryService
	books     *bookapp.Service
	content   *contentapp.AppService
	images    *contentapp.ImageProxy
	replace   *replaceapp.Manager
	debugSvc  *debugapp.Service
	debug     *debugapp.Publisher
	check     *checkapp.Manager
	// heartbeat 覆盖 SSE 心跳间隔，仅供测试缩短等待；零值使用 defaultSSEHeartbeatInterval。
	heartbeat time.Duration
}

func NewSourceHandler(manager *sourceapp.Manager) *SourceHandler {
	return &SourceHandler{manager: manager}
}

func (h *SourceHandler) WithImporter(importer *sourceapp.Importer) *SourceHandler {
	h.importer = importer
	return h
}

type Searcher interface {
	Search(ctx context.Context, input searchapp.MultiSearchInput) (searchapp.MultiSearchResult, error)
}

type DiscoveryService interface {
	Discover(ctx context.Context, input discoveryapp.Input) (discoveryapp.Result, error)
}

func (h *SourceHandler) WithSearcher(searcher Searcher) *SourceHandler {
	h.searcher = searcher
	return h
}

func (h *SourceHandler) WithDiscovery(discovery DiscoveryService) *SourceHandler {
	h.discovery = discovery
	return h
}

func (h *SourceHandler) WithBooks(books *bookapp.Service, content *contentapp.AppService) *SourceHandler {
	h.books = books
	h.content = content
	return h
}

func (h *SourceHandler) WithImages(images *contentapp.ImageProxy) *SourceHandler {
	h.images = images
	return h
}

func (h *SourceHandler) WithReplace(replace *replaceapp.Manager) *SourceHandler {
	h.replace = replace
	return h
}

func (h *SourceHandler) WithDebug(service *debugapp.Service, publisher *debugapp.Publisher) *SourceHandler {
	h.debugSvc = service
	h.debug = publisher
	return h
}

func (h *SourceHandler) WithCheck(check *checkapp.Manager) *SourceHandler {
	h.check = check
	return h
}

func (h *SourceHandler) ImportBookSources(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		writeNotConfigured(w, r, "import service is not configured")
		return
	}
	var body generated.ImportBookSourcesRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	input, err := importInputFromDTO(body)
	if err != nil {
		writeValidationError(w, r, err.Error())
		return
	}
	result, err := h.importer.Import(r.Context(), input)
	if err != nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "import_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, mapImportResult(result, middleware.TraceIDFromContext(r.Context())))
}

func (h *SourceHandler) ListReplaceRules(w http.ResponseWriter, r *http.Request) {
	if h.replace == nil {
		writeNotConfigured(w, r, "replace service is not configured")
		return
	}
	items, _, err := h.replace.List(r.Context(), replace.ReplaceRuleFilter{Page: 1, PageSize: 10000})
	if err != nil {
		writeReplaceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.ReplaceRuleListResponse{Items: mapReplaceRules(items)})
}

func (h *SourceHandler) CreateReplaceRule(w http.ResponseWriter, r *http.Request) {
	if h.replace == nil {
		writeNotConfigured(w, r, "replace service is not configured")
		return
	}
	var body generated.ReplaceRule
	if !decodeJSON(w, r, &body) {
		return
	}
	rule := replaceRuleFromDTO(body)
	created, err := h.replace.Create(r.Context(), &rule)
	if err != nil {
		writeReplaceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapReplaceRule(created))
}

func (h *SourceHandler) UpdateReplaceRule(w http.ResponseWriter, r *http.Request, ruleId int64) {
	if h.replace == nil {
		writeNotConfigured(w, r, "replace service is not configured")
		return
	}
	var body generated.ReplaceRule
	if !decodeJSON(w, r, &body) {
		return
	}
	rule := replaceRuleFromDTO(body)
	updated, err := h.replace.Update(r.Context(), ruleId, &rule)
	if err != nil {
		writeReplaceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapReplaceRule(updated))
}

func (h *SourceHandler) DeleteReplaceRule(w http.ResponseWriter, r *http.Request, ruleId int64) {
	if h.replace == nil {
		writeNotConfigured(w, r, "replace service is not configured")
		return
	}
	if err := h.replace.Delete(r.Context(), ruleId); err != nil {
		writeReplaceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SourceHandler) TestReplaceRule(w http.ResponseWriter, r *http.Request) {
	if h.replace == nil {
		writeNotConfigured(w, r, "replace service is not configured")
		return
	}
	var body generated.ReplaceRuleTestRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	rule, err := replaceRuleFromUnion(body.Rule)
	if err != nil {
		writeValidationError(w, r, err.Error())
		return
	}
	text, err := h.replace.Test(r.Context(), rule, body.Text)
	if err != nil {
		writeReplaceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.ReplaceRuleTestResponse{Text: text})
}

func (h *SourceHandler) CreateBook(w http.ResponseWriter, r *http.Request) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	var body generated.Book
	if !decodeJSON(w, r, &body) {
		return
	}
	b := bookFromDTO(body)
	if strings.TrimSpace(b.BookURL) == "" || strings.TrimSpace(b.Name) == "" {
		writeValidationError(w, r, "bookUrl and name are required")
		return
	}
	saved, err := h.books.Save(r.Context(), &b)
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapBook(saved))
}

func (h *SourceHandler) ListBooks(w http.ResponseWriter, r *http.Request, params generated.ListBooksParams) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	page := 1
	if params.Page != nil {
		page = *params.Page
	}
	pageSize := 50
	if params.PageSize != nil {
		pageSize = *params.PageSize
	}
	if page < 1 || pageSize < 1 {
		writeValidationError(w, r, "page and pageSize must be positive")
		return
	}
	items, total, err := h.books.List(r.Context(), book.BookFilter{Page: page, PageSize: pageSize})
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.BookListResponse{
		Items: mapBooks(items),
		Meta:  generated.PageMeta{Page: page, PageSize: pageSize, Total: total},
	})
}

func (h *SourceHandler) GetBook(w http.ResponseWriter, r *http.Request, bookId string) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	b, err := h.books.Get(r.Context(), bookId)
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBook(b))
}

func (h *SourceHandler) DeleteBook(w http.ResponseWriter, r *http.Request, bookId string) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	if err := h.books.Delete(r.Context(), bookId); err != nil {
		writeBookError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SourceHandler) UpdateBookProgress(w http.ResponseWriter, r *http.Request, bookId string) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	var body generated.BookProgress
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.books.SaveProgress(r.Context(), bookId, progressFromDTO(body))
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBook(updated))
}

func (h *SourceHandler) RefreshBookInfo(w http.ResponseWriter, r *http.Request, bookId string) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	updated, err := h.books.RefreshInfo(r.Context(), bookId)
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBook(&updated))
}

func (h *SourceHandler) RefreshBookToc(w http.ResponseWriter, r *http.Request, bookId string) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	chapters, b, err := h.books.RefreshToc(r.Context(), bookId)
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.ChapterListResponse{Items: mapChapters(chapters), Book: mapBookPtr(b)})
}

func (h *SourceHandler) ListBookChapters(w http.ResponseWriter, r *http.Request, bookId string) {
	if h.books == nil {
		writeNotConfigured(w, r, "book service is not configured")
		return
	}
	chapters, b, err := h.books.ListChapters(r.Context(), bookId)
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.ChapterListResponse{Items: mapChapters(chapters), Book: mapBookPtr(b)})
}

func (h *SourceHandler) GetBookChapterContent(w http.ResponseWriter, r *http.Request, bookId string, index int) {
	if h.content == nil {
		writeNotConfigured(w, r, "content service is not configured")
		return
	}
	if index < 0 {
		writeValidationError(w, r, "index must be >= 0")
		return
	}
	result, b, err := h.content.GetChapterContent(r.Context(), bookId, index)
	if err != nil {
		writeBookError(w, r, err)
		return
	}
	chapter := mapChapter(&result.Chapter)
	writeJSON(w, http.StatusOK, generated.ChapterContentResponse{
		Content: result.Content,
		Title:   stringPtr(result.Chapter.Title),
		Chapter: &chapter,
		Book:    mapBookPtr(b),
	})
}

func (h *SourceHandler) ProxyImage(w http.ResponseWriter, r *http.Request, params generated.ProxyImageParams) {
	if h.images == nil {
		writeNotConfigured(w, r, "image proxy service is not configured")
		return
	}
	if strings.TrimSpace(params.Url) == "" {
		writeValidationError(w, r, "url is required")
		return
	}
	decodeMode := contentapp.DecodeModeNone
	if params.DecodeMode != nil {
		decodeMode = contentapp.DecodeMode(*params.DecodeMode)
	}
	result, err := h.images.Fetch(r.Context(), contentapp.ImageProxyInput{
		URL:        params.Url,
		BookID:     stringValue(params.BookId),
		SourceID:   stringValue(params.SourceId),
		DecodeMode: decodeMode,
	})
	if err != nil {
		writeImageProxyError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Body)
}

func (h *SourceHandler) SearchBooks(w http.ResponseWriter, r *http.Request) {
	if h.searcher == nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "search service is not configured", nil)
		return
	}
	var body generated.SearchRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	keyword := strings.TrimSpace(body.Keyword)
	if keyword == "" {
		writeValidationError(w, r, "keyword is required")
		return
	}
	page := 1
	if body.Page != nil {
		page = *body.Page
	}
	if page < 1 {
		writeValidationError(w, r, "page must be positive")
		return
	}
	enabledOnly := true
	if body.EnabledOnly != nil {
		enabledOnly = *body.EnabledOnly
	}
	input := searchapp.MultiSearchInput{
		Keyword:     keyword,
		Page:        page,
		EnabledOnly: enabledOnly,
	}
	if body.SourceIds != nil {
		input.SourceIDs = append([]string(nil), (*body.SourceIds)...)
	}
	if body.Group != nil {
		input.Group = strings.TrimSpace(*body.Group)
	}
	if body.Precision != nil {
		input.Precision = *body.Precision
	}
	result, err := h.searcher.Search(r.Context(), input)
	if err != nil {
		writeSearchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.SearchResponse{
		Items:    mapSearchBooks(result.Items),
		Warnings: mapSourceWarnings(result.Warnings),
		HasMore:  false,
	})
}

func (h *SourceHandler) DiscoverBooks(w http.ResponseWriter, r *http.Request) {
	if h.discovery == nil {
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "discovery service is not configured", nil)
		return
	}
	var body generated.DiscoveryRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	page := 1
	if body.Page != nil {
		page = *body.Page
	}
	if page < 1 {
		writeValidationError(w, r, "page must be positive")
		return
	}
	input := discoveryapp.Input{Page: page}
	if body.SourceIds != nil {
		input.SourceIDs = append([]string(nil), (*body.SourceIds)...)
	}
	if body.Group != nil {
		input.Group = strings.TrimSpace(*body.Group)
	}
	if body.DiscoveryUrl != nil {
		input.DiscoveryURL = strings.TrimSpace(*body.DiscoveryUrl)
	}
	result, err := h.discovery.Discover(r.Context(), input)
	if err != nil {
		writeDiscoveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.SearchResponse{
		Items:    mapSearchBooks(result.Items),
		Warnings: mapDiscoveryWarnings(result.Warnings),
		HasMore:  false,
	})
}

func (h *SourceHandler) ListBookSources(w http.ResponseWriter, r *http.Request, params generated.ListBookSourcesParams) {
	page := 1
	if params.Page != nil {
		page = int(*params.Page)
	}
	pageSize := 50
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
	}
	if page < 1 || pageSize < 1 {
		writeValidationError(w, r, "page and pageSize must be positive")
		return
	}
	filter := source.BookSourceFilter{
		Page:           page,
		PageSize:       pageSize,
		Enabled:        params.Enabled,
		EnabledExplore: params.EnabledExplore,
	}
	if params.Keyword != nil {
		filter.Keyword = *params.Keyword
	}
	if params.Group != nil {
		filter.Group = *params.Group
	}
	result, err := h.manager.List(r.Context(), filter)
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.BookSourceListResponse{
		Items: mapSourceSummaries(result.Items),
		Meta: generated.PageMeta{
			Page:     page,
			PageSize: pageSize,
			Total:    result.Total,
		},
	})
}

func (h *SourceHandler) CreateBookSource(w http.ResponseWriter, r *http.Request) {
	src, ok := decodeBookSourceBody(w, r)
	if !ok {
		return
	}
	created, err := h.manager.Create(r.Context(), src)
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, mapBookSource(created))
}

func (h *SourceHandler) GetBookSource(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	src, err := h.manager.Get(r.Context(), string(sourceID))
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBookSource(src))
}

func (h *SourceHandler) UpdateBookSource(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	src, ok := decodeBookSourceBody(w, r)
	if !ok {
		return
	}
	updated, err := h.manager.Update(r.Context(), string(sourceID), src)
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBookSource(updated))
}

func (h *SourceHandler) DeleteBookSource(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	if err := h.manager.Delete(r.Context(), string(sourceID)); err != nil {
		writeSourceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SourceHandler) UpdateBookSourceEnabled(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	body, ok := decodeEnabledBody(w, r)
	if !ok {
		return
	}
	src, err := h.manager.UpdateEnabled(r.Context(), string(sourceID), body.Enabled)
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBookSourceSummary(src))
}

func (h *SourceHandler) UpdateBookSourceExploreEnabled(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	body, ok := decodeEnabledBody(w, r)
	if !ok {
		return
	}
	src, err := h.manager.UpdateExploreEnabled(r.Context(), string(sourceID), body.Enabled)
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapBookSourceSummary(src))
}

func (h *SourceHandler) ReorderBookSources(w http.ResponseWriter, r *http.Request) {
	var body generated.BookSourceReorderRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	orders := make([]source.SourceOrder, 0, len(body.Orders))
	for _, order := range body.Orders {
		if order.SourceId == "" {
			writeValidationError(w, r, "sourceId is required")
			return
		}
		orders = append(orders, source.SourceOrder{SourceID: order.SourceId, CustomOrder: order.CustomOrder})
	}
	result, err := h.manager.Reorder(r.Context(), orders)
	if err != nil {
		writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, generated.BookSourceListResponse{
		Items: mapSourceSummaries(result.Items),
		Meta: generated.PageMeta{
			Page:     1,
			PageSize: len(result.Items),
			Total:    result.Total,
		},
	})
}

func decodeBookSourceBody(w http.ResponseWriter, r *http.Request) (*source.BookSource, bool) {
	var body generated.BookSource
	if !decodeJSON(w, r, &body) {
		return nil, false
	}
	src, err := bookSourceFromDTO(body)
	if err != nil {
		writeValidationError(w, r, err.Error())
		return nil, false
	}
	return src, true
}

func decodeEnabledBody(w http.ResponseWriter, r *http.Request) (generated.EnabledUpdate, bool) {
	var body generated.EnabledUpdate
	if !decodeJSON(w, r, &body) {
		return generated.EnabledUpdate{}, false
	}
	return body, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeValidationError(w, r, err.Error())
		return false
	}
	return true
}

func writeSourceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, source.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "source_not_found", "book source not found", nil)
	case errors.Is(err, source.ErrMissingBookSourceURL), errors.Is(err, source.ErrMissingBookSourceName), errors.Is(err, source.ErrConflict):
		writeValidationError(w, r, err.Error())
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}

func writeSearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, source.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "source_not_found", "book source not found", nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}

func writeDiscoveryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, source.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "source_not_found", "book source not found", nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}

func writeBookError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sqliterepo.ErrNotFound), errors.Is(err, source.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "book_not_found", "book not found", nil)
	case errors.Is(err, contentapp.ErrContentEmpty):
		middleware.WriteError(w, r, http.StatusInternalServerError, "content_empty", "content is empty", nil)
	case errors.Is(err, contentapp.ErrUnsupportedContentBrowserFlow):
		middleware.WriteError(w, r, http.StatusBadRequest, "unsupported_rule_feature", err.Error(), nil)
	case errors.Is(err, bookapp.ErrTocEmpty):
		middleware.WriteError(w, r, http.StatusInternalServerError, "toc_empty", "toc is empty", nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}

func writeImageProxyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, contentapp.ErrImageURLRequired):
		writeValidationError(w, r, "url is required")
	case errors.Is(err, contentapp.ErrImageSourceRequired):
		writeValidationError(w, r, "sourceId is required when decodeMode is set")
	case errors.Is(err, contentapp.ErrImageSourceNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "source_not_found", "book source not found", nil)
	case errors.Is(err, contentapp.ErrInvalidImageType):
		middleware.WriteError(w, r, http.StatusBadGateway, "fetch_failed", "image response content type is not allowed", nil)
	case errors.Is(err, contentapp.ErrImageDecodeFailed):
		middleware.WriteError(w, r, http.StatusBadGateway, "script_failed", err.Error(), nil)
	case errors.Is(err, fetch.ErrBlocked):
		middleware.WriteError(w, r, http.StatusBadGateway, "fetch_blocked", err.Error(), nil)
	default:
		middleware.WriteError(w, r, http.StatusBadGateway, "fetch_failed", err.Error(), nil)
	}
}

func writeReplaceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sqliterepo.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "replace_not_found", "replace rule not found", nil)
	case errors.Is(err, replace.ErrEmptyPattern), errors.Is(err, replace.ErrInvalidRegex):
		writeValidationError(w, r, err.Error())
	case errors.Is(err, replaceapp.ErrRegexLimitExceeded):
		middleware.WriteError(w, r, http.StatusInternalServerError, "replace_failed", "replace regex limit exceeded", nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}

func writeNotConfigured(w http.ResponseWriter, r *http.Request, message string) {
	middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", message, nil)
}

func writeValidationError(w http.ResponseWriter, r *http.Request, message string) {
	middleware.WriteError(w, r, http.StatusBadRequest, "validation_failed", message, nil)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
