package fetch

import (
	"context"
	"errors"
	"net/http"
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
}

type Response struct {
	StatusCode int
	Headers    http.Header
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
