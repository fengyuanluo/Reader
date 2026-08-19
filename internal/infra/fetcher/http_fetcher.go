package fetcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	"reader/internal/domain/fetch"
)

type HTTPFetcher struct {
	client           *http.Client
	defaultUserAgent string
	maxBodyBytes     int64
	allowPrivate     bool
	resolver         func(string) ([]net.IP, error)
	cookies          *CookieManager
	rateLimiter      *sourceRateLimiter
}

type Option func(*HTTPFetcher)

func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	f := &HTTPFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		defaultUserAgent: "Reader/0.0.1",
		maxBodyBytes:     10 * 1024 * 1024,
		resolver:         net.LookupIP,
		rateLimiter:      newSourceRateLimiter(),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

func WithTimeout(timeout time.Duration) Option {
	return func(f *HTTPFetcher) {
		f.client.Timeout = timeout
	}
}

func WithMaxRedirects(max int) Option {
	return func(f *HTTPFetcher) {
		f.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= max {
				return errors.New("too many redirects")
			}
			if !f.allowPrivate {
				if err := f.checkHost(req.URL.Hostname()); err != nil {
					return err
				}
			}
			return nil
		}
	}
}

func WithResolver(resolver func(string) ([]net.IP, error)) Option {
	return func(f *HTTPFetcher) {
		f.resolver = resolver
	}
}

func WithCookieManager(manager *CookieManager) Option {
	return func(f *HTTPFetcher) {
		f.cookies = manager
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(f *HTTPFetcher) {
		f.client = client
	}
}

func WithDefaultUserAgent(userAgent string) Option {
	return func(f *HTTPFetcher) {
		f.defaultUserAgent = userAgent
	}
}

func WithMaxBodyBytes(max int64) Option {
	return func(f *HTTPFetcher) {
		f.maxBodyBytes = max
	}
}

func WithAllowPrivateNetwork(allow bool) Option {
	return func(f *HTTPFetcher) {
		f.allowPrivate = allow
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if req.WebViewFallback {
		slog.WarnContext(ctx, "Legado webView request is using plain HTTP fallback")
	}
	if !req.SkipRateLimit && f.rateLimiter != nil {
		if err := f.rateLimiter.wait(ctx, req.SourceKey, req.ConcurrentRate); err != nil {
			return nil, err
		}
	}
	attempts := req.Retry + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastResp *fetch.Response
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := f.fetchOnce(ctx, req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		lastResp = resp
		lastErr = nil
		if req.AllowNonSuccess || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
			return resp, nil
		}
	}
	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func (f *HTTPFetcher) fetchOnce(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	parsed, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "data" {
		body, err := decodeDataURI(req.URL)
		if err != nil {
			return nil, err
		}
		if req.EncodeBodyAsHex {
			body = []byte(hex.EncodeToString(body))
		}
		return &fetch.Response{StatusCode: 200, Headers: http.Header{}, Body: body, URL: req.URL}, nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: unsupported scheme", fetch.ErrBlocked)
	}
	if !f.allowPrivate {
		if err := f.checkHost(parsed.Hostname()); err != nil {
			return nil, err
		}
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	targetURL := req.URL
	if len(req.Query) > 0 {
		parsed.RawQuery = mergeQuery(parsed.Query(), req.Query)
		targetURL = parsed.String()
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, err
	}
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	if req.UseCookieJar && f.cookies != nil {
		cookie, err := f.cookies.Load(ctx, targetURL, httpReq.Header.Get("Cookie"))
		if err != nil {
			return nil, err
		}
		if cookie != "" {
			httpReq.Header.Set("Cookie", cookie)
		}
	}
	if req.RequestWithoutUA {
		httpReq.Header["User-Agent"] = []string{""}
	} else if f.defaultUserAgent != "" && httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", f.defaultUserAgent)
	}
	if httpReq.Header.Get("Accept-Encoding") == "" {
		httpReq.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	reader := resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	respBody, err := io.ReadAll(io.LimitReader(reader, f.maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.Header.Get("Content-Encoding") != "gzip" && hasGzipMagic(respBody) {
		gzipReader, err := gzip.NewReader(bytes.NewReader(respBody))
		if err != nil {
			return nil, err
		}
		respBody, err = io.ReadAll(io.LimitReader(gzipReader, f.maxBodyBytes+1))
		_ = gzipReader.Close()
		if err != nil {
			return nil, err
		}
	}
	if int64(len(respBody)) > f.maxBodyBytes {
		return nil, errors.New("response body too large")
	}
	if !req.EncodeBodyAsHex {
		if decoded, err := decodeTextResponseBody(respBody, resp.Header.Get("Content-Type"), f.maxBodyBytes); err == nil {
			respBody = decoded
		}
	}
	if req.EncodeBodyAsHex {
		respBody = []byte(hex.EncodeToString(respBody))
	}
	if !req.AllowNonSuccess && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	if req.UseCookieJar && f.cookies != nil {
		if err := f.cookies.Save(ctx, resp.Request.URL.String(), resp.Header); err != nil {
			return nil, err
		}
	}
	return &fetch.Response{StatusCode: resp.StatusCode, Headers: resp.Header, Body: respBody, URL: resp.Request.URL.String()}, nil
}

func hasGzipMagic(body []byte) bool {
	return len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b
}

func decodeTextResponseBody(body []byte, contentType string, maxBodyBytes int64) ([]byte, error) {
	if !looksLikeTextContent(contentType) {
		return body, nil
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body, err
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
	if err != nil {
		return body, err
	}
	if int64(len(decoded)) > maxBodyBytes {
		return nil, errors.New("response body too large")
	}
	return decoded, nil
}

func looksLikeTextContent(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case strings.HasPrefix(contentType, "application/json"):
		return true
	case strings.HasPrefix(contentType, "application/xml"):
		return true
	case strings.HasPrefix(contentType, "application/xhtml+xml"):
		return true
	case strings.HasPrefix(contentType, "application/javascript"):
		return true
	case strings.HasPrefix(contentType, "application/x-javascript"):
		return true
	default:
		return false
	}
}

func (f *HTTPFetcher) checkHost(host string) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%w: empty host", fetch.ErrBlocked)
	}
	ips, err := f.resolver(host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: private or reserved address %s", fetch.ErrBlocked, ip.String())
		}
	}
	return nil
}

func mergeQuery(base url.Values, extra map[string]string) string {
	for key, value := range extra {
		base.Set(key, value)
	}
	return base.Encode()
}

func decodeDataURI(raw string) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	data := parsed.Opaque
	if data == "" {
		data = strings.TrimPrefix(raw, "data:")
	}
	parts := strings.SplitN(data, ",", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid data uri")
	}
	meta, payload := parts[0], parts[1]
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return base64.StdEncoding.DecodeString(payload)
	}
	decoded, err := url.QueryUnescape(payload)
	if err != nil {
		return nil, err
	}
	return []byte(decoded), nil
}

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// RFC 6890 special-use and metadata-style ranges.
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 0:
			return true
		case ip4[0] == 100 && ip4[1]&0b1100_0000 == 0b0100_0000:
			return true
		case ip4[0] == 169 && ip4[1] == 254:
			return true
		case ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19):
			return true
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0:
			return true
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2:
			return true
		case ip4[0] == 192 && ip4[1] == 88 && ip4[2] == 99:
			return true
		case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100:
			return true
		case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113:
			return true
		case ip4[0] >= 224:
			return true
		}
	}
	if ip16 := ip.To16(); ip16 != nil && strings.HasPrefix(ip.String(), "fe80:") {
		return true
	}
	return false
}

func checkHostWithResolver(host string, resolver func(string) ([]net.IP, error)) error {
	f := NewHTTPFetcher(WithResolver(resolver))
	return f.checkHost(host)
}
