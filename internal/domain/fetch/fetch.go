package fetch

import (
	"context"
	"errors"
	"net/textproto"
	"time"
)

var ErrBlocked = errors.New("fetch blocked")

type Request struct {
	URL              string
	Method           string
	Headers          map[string]string
	Body             []byte
	Query            map[string]string
	RequestWithoutUA bool
	UseCookieJar     bool
	AllowNonSuccess  bool
	EncodeBodyAsHex  bool
	Retry            int
	// Proxy 为本次请求使用的代理，支持 Legado 写法 scheme://host:port@user@pass 与标准 userinfo 写法。
	Proxy string
	// DNSIP 固定该请求主机连接的 IP（dnsIp 选项），Host 头与 TLS SNI 仍取自 URL；不可与 Proxy 同时使用。
	DNSIP string
	// SourceKey 是按源限速的键（书源地址），ConcurrentRate 为该源的 Legado concurrentRate 配置。
	SourceKey       string
	ConcurrentRate  string
	SkipRateLimit   bool
	BodyJS          string
	WebViewFallback bool
}

// Headers 是响应头的 domain 自有类型，语义与 net/http.Header 一致（键为规范形式，值为字符串切片），
// 但避免 domain 层直接依赖 net/http；infra 的 fetcher 在适配层完成转换。
type Headers map[string][]string

// Get 返回首个值；键按 MIME 头的规范形式匹配，与 net/http.Header.Get 行为一致。
func (h Headers) Get(name string) string {
	values := h[textproto.CanonicalMIMEHeaderKey(name)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Values 返回键对应的全部值；语义与 net/http.Header.Values 一致。
func (h Headers) Values(name string) []string {
	return h[textproto.CanonicalMIMEHeaderKey(name)]
}

type Response struct {
	StatusCode int
	Headers    Headers
	Body       []byte
	URL        string
}

type Fetcher interface {
	Fetch(ctx context.Context, req Request) (*Response, error)
}

type ImageCacheEntry struct {
	Body        []byte
	ContentType string
}

type ImageCache interface {
	GetImage(ctx context.Context, imageURL string) (ImageCacheEntry, bool, error)
	SaveImage(ctx context.Context, imageURL string, entry ImageCacheEntry) error
}

type Cookie struct {
	Domain    string
	Name      string
	Value     string
	Path      string
	ExpiresAt *time.Time
}

type CookieRepository interface {
	Save(ctx context.Context, cookie Cookie) error
	ListByDomain(ctx context.Context, domain string) ([]Cookie, error)
	Delete(ctx context.Context, domain string, name string) error
}
