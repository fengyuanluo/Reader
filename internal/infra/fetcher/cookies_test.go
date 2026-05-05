package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/fetch"
)

func TestCookieManagerMergesRequestPersistentAndSessionCookies(t *testing.T) {
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	session := NewSessionCookieStore()
	manager := NewCookieManager(repo, session)
	ctx := context.Background()
	expires := time.Now().Add(time.Hour)
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "persist", Value: "p1", ExpiresAt: &expires}); err != nil {
		t.Fatalf("save persist: %v", err)
	}
	session.Save("example.test", []*http.Cookie{{Name: "sid", Value: "s1"}})

	got, err := manager.Load(ctx, "https://www.example.test/path", "manual=m1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, want := range []string{"manual=m1", "persist=p1", "sid=s1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cookie = %q missing %s", got, want)
		}
	}
}

func TestHTTPFetcherSavesSetCookieAndSendsCookieOnNextRequest(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, NewSessionCookieStore())
	var secondCookie string
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			http.SetCookie(w, &http.Cookie{Name: "persist", Value: "p1", Expires: time.Now().Add(time.Hour)})
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s1"})
			_, _ = w.Write([]byte("first"))
			return
		}
		secondCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("second"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithCookieManager(manager))
	if _, err := f.Fetch(ctx, fetch.Request{URL: server.URL, UseCookieJar: true}); err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	if _, err := f.Fetch(ctx, fetch.Request{URL: server.URL, UseCookieJar: true}); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	for _, want := range []string{"persist=p1", "sid=s1"} {
		if !strings.Contains(secondCookie, want) {
			t.Fatalf("second Cookie = %q missing %s", secondCookie, want)
		}
	}
}

func TestHTTPFetcherSkipsCookieManagerWhenUseCookieJarDisabled(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, NewSessionCookieStore())
	var secondCookie string
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			http.SetCookie(w, &http.Cookie{Name: "persist", Value: "p1", Expires: time.Now().Add(time.Hour)})
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s1"})
			_, _ = w.Write([]byte("first"))
			return
		}
		secondCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("second"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithCookieManager(manager))
	if _, err := f.Fetch(ctx, fetch.Request{URL: server.URL, UseCookieJar: false}); err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	if _, err := f.Fetch(ctx, fetch.Request{URL: server.URL, UseCookieJar: false}); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	if secondCookie != "" {
		t.Fatalf("second Cookie = %q, want empty", secondCookie)
	}
}

func TestCookieDomainUsesEffectiveTLDPlusOne(t *testing.T) {
	got, err := cookieDomain("https://www.biquge.com.cn/book/1")
	if err != nil {
		t.Fatalf("cookieDomain() error = %v", err)
	}
	if got != "biquge.com.cn" {
		t.Fatalf("cookieDomain() = %q", got)
	}
}

type memoryCookieRepo struct {
	cookies map[string]fetch.Cookie
}

func (r *memoryCookieRepo) Save(ctx context.Context, cookie fetch.Cookie) error {
	r.cookies[cookie.Domain+"|"+cookie.Name] = cookie
	return nil
}

func (r *memoryCookieRepo) ListByDomain(ctx context.Context, domain string) ([]fetch.Cookie, error) {
	out := []fetch.Cookie{}
	for _, cookie := range r.cookies {
		if cookie.Domain == domain {
			out = append(out, cookie)
		}
	}
	return out, nil
}

func (r *memoryCookieRepo) Delete(ctx context.Context, domain string, name string) error {
	delete(r.cookies, domain+"|"+name)
	return nil
}
