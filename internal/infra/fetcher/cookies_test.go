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

func TestCookieManagerDropsAndDeletesExpiredPersistedCookies(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, nil)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)
	corrupt := time.Time{}
	for _, cookie := range []fetch.Cookie{
		{Domain: "example.test", Name: "expired", Value: "old", ExpiresAt: &past},
		{Domain: "example.test", Name: "corrupt", Value: "bad", ExpiresAt: &corrupt},
		{Domain: "example.test", Name: "valid", Value: "v1", ExpiresAt: &future},
		{Domain: "example.test", Name: "forever", Value: "f1"},
	} {
		if err := repo.Save(ctx, cookie); err != nil {
			t.Fatal(err)
		}
	}

	got, err := manager.Load(ctx, "https://www.example.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, want := range []string{"valid=v1", "forever=f1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cookie = %q missing %s", got, want)
		}
	}
	for _, unwanted := range []string{"expired=", "corrupt="} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("cookie = %q still sends %s", got, unwanted)
		}
	}
	for _, key := range []string{"example.test|expired", "example.test|corrupt"} {
		if _, ok := repo.cookies[key]; ok {
			t.Fatalf("expired cookie %s was not deleted", key)
		}
	}
}

func TestCookieManagerSaveHonoursExpiryAttributes(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	session := NewSessionCookieStore()
	manager := NewCookieManager(repo, session)
	stale := time.Now().Add(time.Hour)
	repo.cookies["example.test|gone"] = fetch.Cookie{Domain: "example.test", Name: "gone", Value: "old", ExpiresAt: &stale}
	repo.cookies["example.test|past"] = fetch.Cookie{Domain: "example.test", Name: "past", Value: "old", ExpiresAt: &stale}
	session.Save("example.test", []*http.Cookie{{Name: "sess", Value: "s1"}})

	headers := http.Header{}
	headers.Add("Set-Cookie", "maxage=m1; Max-Age=60")
	headers.Add("Set-Cookie", "gone=; Max-Age=0")
	headers.Add("Set-Cookie", "past=; Expires=Thu, 01 Jan 1970 00:00:00 GMT")
	headers.Add("Set-Cookie", "sess=; Max-Age=0")
	before := time.Now()
	if err := manager.Save(ctx, "https://www.example.test/", headers); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	maxAge, ok := repo.cookies["example.test|maxage"]
	if !ok || maxAge.ExpiresAt == nil {
		t.Fatalf("Max-Age cookie = %#v, want persisted with expiry", maxAge)
	}
	if d := maxAge.ExpiresAt.Sub(before); d < 59*time.Second || d > 61*time.Second {
		t.Fatalf("Max-Age expiry in %v, want ~60s", d)
	}
	for _, key := range []string{"example.test|gone", "example.test|past"} {
		if _, ok := repo.cookies[key]; ok {
			t.Fatalf("deleted cookie %s still persisted", key)
		}
	}
	if _, ok := session.List("example.test")["sess"]; ok {
		t.Fatal("deleted session cookie still present")
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
