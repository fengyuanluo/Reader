package contentapp

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
)

var (
	ErrImageURLRequired    = errors.New("image url is required")
	ErrInvalidImageType    = errors.New("image response content type is not allowed")
	ErrImageSourceRequired = errors.New("image source id is required when decode mode is set")
	ErrImageSourceNotFound = errors.New("image source not found")
	ErrImageDecodeFailed   = errors.New("image decode failed")
)

type DecodeMode string

const (
	DecodeModeNone    DecodeMode = ""
	DecodeModeCover   DecodeMode = "cover"
	DecodeModeContent DecodeMode = "content"
)

type ImageProxy struct {
	fetcher fetch.Fetcher
	cache   fetch.ImageCache
	sources source.BookSourceRepository
	books   book.BookRepository
	script  *script.Runtime
}

type ImageProxyResult struct {
	Body        []byte
	ContentType string
}

type ImageProxyInput struct {
	URL        string
	BookID     string
	SourceID   string
	DecodeMode DecodeMode
}

func NewImageProxy(fetcher fetch.Fetcher, sources source.BookSourceRepository, books book.BookRepository, scriptRuntime *script.Runtime) *ImageProxy {
	return &ImageProxy{fetcher: fetcher, sources: sources, books: books, script: scriptRuntime}
}

func (p *ImageProxy) WithCache(cache fetch.ImageCache) *ImageProxy {
	p.cache = cache
	return p
}

func (p *ImageProxy) Fetch(ctx context.Context, input ImageProxyInput) (ImageProxyResult, error) {
	imageURL := strings.TrimSpace(input.URL)
	if imageURL == "" {
		return ImageProxyResult{}, ErrImageURLRequired
	}
	if input.DecodeMode != DecodeModeNone {
		if strings.TrimSpace(input.SourceID) == "" {
			return ImageProxyResult{}, ErrImageSourceRequired
		}
	}
	cacheKey := imageCacheKey(input)
	if p.cache != nil {
		if cached, ok, err := p.cache.GetImage(ctx, cacheKey); err != nil {
			return ImageProxyResult{}, err
		} else if ok {
			return ImageProxyResult{Body: cached.Body, ContentType: cached.ContentType}, nil
		}
	}
	resp, err := p.fetcher.Fetch(ctx, fetch.Request{
		URL: imageURL,
		Headers: map[string]string{
			"Accept": "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
		},
	})
	if err != nil {
		return ImageProxyResult{}, err
	}
	body := resp.Body
	if input.DecodeMode != DecodeModeNone {
		decoded, err := p.decodeImage(ctx, input, imageURL, resp.Body)
		if err != nil {
			return ImageProxyResult{}, err
		}
		body = decoded
	}
	contentType := normalizeImageContentType(resp.Headers, body)
	if !isAllowedImageContentType(contentType) {
		return ImageProxyResult{}, ErrInvalidImageType
	}
	if p.cache != nil {
		if err := p.cache.SaveImage(ctx, cacheKey, fetch.ImageCacheEntry{Body: body, ContentType: contentType}); err != nil {
			return ImageProxyResult{}, err
		}
	}
	return ImageProxyResult{Body: body, ContentType: contentType}, nil
}

func (p *ImageProxy) decodeImage(ctx context.Context, input ImageProxyInput, imageURL string, body []byte) ([]byte, error) {
	ctx = script.WithStage(ctx, "image_decode")
	if p.sources == nil || p.script == nil {
		return nil, fmt.Errorf("%w: image decode runtime is not configured", ErrImageDecodeFailed)
	}
	src, err := p.sources.FindByID(ctx, input.SourceID)
	if err != nil {
		if errors.Is(err, source.ErrNotFound) {
			return nil, ErrImageSourceNotFound
		}
		return nil, err
	}
	scriptText := decodeScriptForSource(src, input.DecodeMode)
	if strings.TrimSpace(scriptText) == "" {
		return body, nil
	}
	var bookValue *book.Book
	if strings.TrimSpace(input.BookID) != "" {
		if p.books == nil {
			return nil, fmt.Errorf("%w: image book repository is not configured", ErrImageDecodeFailed)
		}
		bookValue, err = p.books.FindByID(ctx, input.BookID)
		if err != nil {
			// 书籍记录缺失时按无 book 绑定继续解码；其余错误（如数据库故障）仍需上抛。
			if !errors.Is(err, book.ErrNotFound) {
				return nil, err
			}
			bookValue = nil
		}
	}
	value, err := p.script.EvalWithBindings(ctx, scriptText, script.Bindings{
		"result": body,
		"src":    imageURL,
		"source": src,
		"book":   bookValue,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageDecodeFailed, err)
	}
	decoded := scriptBytes(value)
	if len(decoded) == 0 {
		return nil, fmt.Errorf("%w: empty decode result", ErrImageDecodeFailed)
	}
	return decoded, nil
}

func imageCacheKey(input ImageProxyInput) string {
	return strings.Join([]string{
		strings.TrimSpace(input.URL),
		strings.TrimSpace(input.SourceID),
		string(input.DecodeMode),
		strings.TrimSpace(input.BookID),
	}, "|")
}

func decodeScriptForSource(src *source.BookSource, mode DecodeMode) string {
	switch mode {
	case DecodeModeCover:
		return strings.TrimSpace(src.CoverDecodeJS)
	case DecodeModeContent:
		if src.RuleContent.Value == nil {
			return ""
		}
		return strings.TrimSpace(src.RuleContent.Value.ImageDecode)
	default:
		return ""
	}
}

func scriptBytes(value any) []byte {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), typed...)
	case string:
		return []byte(typed)
	case []any:
		out := make([]byte, 0, len(typed))
		for _, item := range typed {
			switch raw := item.(type) {
			case float64:
				out = append(out, byte(raw))
			case int:
				out = append(out, byte(raw))
			case int64:
				out = append(out, byte(raw))
			case uint8:
				out = append(out, raw)
			default:
				return nil
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeImageContentType(headers fetch.Headers, body []byte) string {
	contentType := strings.TrimSpace(headers.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(contentType)
	}
	return strings.ToLower(mediaType)
}

func isAllowedImageContentType(contentType string) bool {
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/svg+xml", "image/bmp":
		return true
	default:
		return false
	}
}
