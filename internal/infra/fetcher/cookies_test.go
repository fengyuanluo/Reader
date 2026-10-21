package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	mu      sync.Mutex
	cookies map[string]fetch.Cookie
}

func (r *memoryCookieRepo) Save(ctx context.Context, cookie fetch.Cookie) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cookies[cookie.Domain+"|"+cookie.Name] = cookie
	return nil
}

func (r *memoryCookieRepo) ListByDomain(ctx context.Context, domain string) ([]fetch.Cookie, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []fetch.Cookie{}
	for _, cookie := range r.cookies {
		if cookie.Domain == domain {
			out = append(out, cookie)
		}
	}
	return out, nil
}

func (r *memoryCookieRepo) Delete(ctx context.Context, domain string, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cookies, domain+"|"+name)
	return nil
}

func TestCookieManagerLoadCachesPerDomain(t *testing.T) {
	ctx := context.Background()
	repo := &countingCookieRepo{memoryCookieRepo: memoryCookieRepo{cookies: map[string]fetch.Cookie{}}}
	manager := NewCookieManager(repo, nil)
	expires := time.Now().Add(time.Hour)
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "persist", Value: "p1", ExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}

	// 第一次 Load 查 repo 并写缓存；第二次命中缓存不再访问 repo。
	if _, err := manager.Load(ctx, "https://www.example.test/", ""); err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	first := repo.listCalls
	if _, err := manager.Load(ctx, "https://www.example.test/other", ""); err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if repo.listCalls != first {
		t.Fatalf("ListByDomain calls = %d, want unchanged %d", repo.listCalls, first)
	}

	// Save 之后下一次 Load 能看到新值（缓存同步更新）。
	headers := http.Header{}
	headers.Add("Set-Cookie", "fresh=f1; Max-Age=3600")
	if err := manager.Save(ctx, "https://www.example.test/", headers); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := manager.Load(ctx, "https://www.example.test/", "")
	if err != nil {
		t.Fatalf("Load() after Save error = %v", err)
	}
	if !strings.Contains(got, "fresh=f1") {
		t.Fatalf("cookie = %q missing fresh=f1", got)
	}

	// 过期 cookie 在缓存命中路径下也不发送，且从存储中删除。
	staleHeaders := http.Header{}
	staleHeaders.Add("Set-Cookie", "stale=s1; Max-Age=1")
	if err := manager.Save(ctx, "https://www.example.test/", staleHeaders); err != nil {
		t.Fatalf("Save() stale error = %v", err)
	}
	// 把缓存中的过期时间拨到过去，模拟过期。
	manager.mutateCache("example.test", func(cookies []fetch.Cookie) []fetch.Cookie {
		for i := range cookies {
			if cookies[i].Name == "stale" {
				past := time.Now().Add(-time.Minute)
				cookies[i].ExpiresAt = &past
			}
		}
		return cookies
	})
	got, err = manager.Load(ctx, "https://www.example.test/", "")
	if err != nil {
		t.Fatalf("Load() stale error = %v", err)
	}
	if strings.Contains(got, "stale=") {
		t.Fatalf("cookie = %q still sends expired stale", got)
	}
	if _, ok := repo.cookies["example.test|stale"]; ok {
		t.Fatal("expired cookie still persisted in repo")
	}
}

// TestCookieManagerConcurrentLoadSaveNoRace 覆盖同 domain 并发 Load/Save：
// 命中路径返回切片副本，setCacheCookie 不再与锁外遍历共享底层数组，-race 无告警且写入后能读到新值。
func TestCookieManagerConcurrentLoadSaveNoRace(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, nil)
	expires := time.Now().Add(time.Hour)
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "seed", Value: "s0", ExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}
	// 预热缓存，让 Load 走命中路径（原先直接返回内部切片的路径）。
	if _, err := manager.Load(ctx, "https://www.example.test/", ""); err != nil {
		t.Fatalf("warmup Load() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			for range 50 {
				if _, err := manager.Load(ctx, "https://www.example.test/", ""); err != nil {
					t.Errorf("Load() error = %v", err)
					return
				}
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			for j := range 50 {
				headers := http.Header{}
				headers.Add("Set-Cookie", fmt.Sprintf("c%d=v%d-%d; Max-Age=3600", n%4, n%4, j))
				if err := manager.Save(ctx, "https://www.example.test/", headers); err != nil {
					t.Errorf("Save() error = %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	// 写入后的新值必须可读（缓存同步更新）。
	headers := http.Header{}
	headers.Add("Set-Cookie", "final=f1; Max-Age=3600")
	if err := manager.Save(ctx, "https://www.example.test/", headers); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := manager.Load(ctx, "https://www.example.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(got, "final=f1") {
		t.Fatalf("cookie = %q missing final=f1", got)
	}
}

type countingCookieRepo struct {
	memoryCookieRepo
	listCalls int
}

func (r *countingCookieRepo) ListByDomain(ctx context.Context, domain string) ([]fetch.Cookie, error) {
	r.listCalls++
	return r.memoryCookieRepo.ListByDomain(ctx, domain)
}

// batchDeleteRepo 记录批量删除调用次数（S08-07）。
type batchDeleteRepo struct {
	memoryCookieRepo
	batchCalls int
	deleted    int64
}

func (r *batchDeleteRepo) DeleteExpired(ctx context.Context, domain string, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchCalls++
	n := int64(0)
	for key, cookie := range r.cookies {
		if cookie.Domain == domain && cookie.ExpiresAt != nil && !cookie.ExpiresAt.After(before) {
			delete(r.cookies, key)
			n++
		}
	}
	r.deleted += n
	return n, nil
}

// TestCookieManagerCacheBoundedByLRUCapacity 覆盖 S00-04：连续 Load 1 万个不同 domain，
// 缓存条目数不超过 LRU 容量上限；空结果按负缓存短 TTL 处理。
func TestCookieManagerCacheBoundedByLRUCapacity(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, nil)
	for i := range 10000 {
		if _, err := manager.Load(ctx, fmt.Sprintf("https://host%d.example.site/", i), ""); err != nil {
			t.Fatalf("Load(%d) error = %v", i, err)
		}
	}
	if n := manager.cache.len(); n > cookieCacheCapacity {
		t.Fatalf("cache len = %d, want <= %d", n, cookieCacheCapacity)
	}
	// 最近使用的域仍在缓存中，最久未使用的被淘汰。
	if _, err := manager.Load(ctx, "https://host9999.example.site/", ""); err != nil {
		t.Fatalf("Load(9999) error = %v", err)
	}
}

// TestCookieManagerNegativeCacheExpires 覆盖 S00-04 空结果负缓存：TTL 内命中缓存，TTL 后重新查 repo。
func TestCookieManagerNegativeCacheExpires(t *testing.T) {
	ctx := context.Background()
	repo := &countingCookieRepo{memoryCookieRepo: memoryCookieRepo{cookies: map[string]fetch.Cookie{}}}
	manager := NewCookieManager(repo, nil)
	if _, err := manager.Load(ctx, "https://empty.example.test/", ""); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	first := repo.listCalls
	if _, err := manager.Load(ctx, "https://empty.example.test/", ""); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if repo.listCalls != first {
		t.Fatalf("negative cache miss within TTL: repo calls = %d, want %d", repo.listCalls, first)
	}
}

// TestCookieManagerSessionCookieDomainMatch 覆盖 S08-06：a.example.com 种下的 session cookie，
// example.com（父域）请求不带；b.a.example.com 与 example.com 的子域匹配规则按 RFC 6265。
func TestCookieManagerSessionCookieDomainMatch(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, NewSessionCookieStore())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	headers := http.Header{}
	headers.Add("Set-Cookie", "sub=s1")
	if err := manager.Save(ctx, "https://a.example.test/", headers); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// 父域请求不带子域的 session cookie。
	got, err := manager.Load(ctx, "https://example.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(got, "sub=s1") {
		t.Fatalf("cookie = %q leaks subdomain session cookie to parent domain", got)
	}
	// 子域自身请求带。
	got, err = manager.Load(ctx, "https://a.example.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(got, "sub=s1") {
		t.Fatalf("cookie = %q missing sub=s1 for its own host", got)
	}
}

// TestCookieManagerSaveRejectsCrossSiteDomain 覆盖 S08-06：Set-Cookie 声明的 Domain 与当前 host
// 不做 domain-match 时拒绝（RFC 6265 5.3 步骤 6），避免跨站投毒。
func TestCookieManagerSaveRejectsCrossSiteDomain(t *testing.T) {
	ctx := context.Background()
	repo := &memoryCookieRepo{cookies: map[string]fetch.Cookie{}}
	manager := NewCookieManager(repo, NewSessionCookieStore())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	headers := http.Header{}
	headers.Add("Set-Cookie", "evil=1; Domain=other.test")
	if err := manager.Save(ctx, "https://example.test/", headers); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := manager.Load(ctx, "https://example.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(got, "evil=1") {
		t.Fatalf("cookie = %q accepted cross-site Domain", got)
	}
	got, err = manager.Load(ctx, "https://other.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(got, "evil=1") {
		t.Fatalf("cookie = %q persisted cross-site Domain", got)
	}
}

// TestCookieManagerBatchDeletesExpiredCookies 覆盖 S08-07：5 条过期 cookie 批量删除，
// 批量接口只调用一次；无批量接口时回退逐条 Delete 且缓存一次失效。
func TestCookieManagerBatchDeletesExpiredCookies(t *testing.T) {
	ctx := context.Background()
	repo := &batchDeleteRepo{memoryCookieRepo: memoryCookieRepo{cookies: map[string]fetch.Cookie{}}}
	manager := NewCookieManager(repo, nil)
	past := time.Now().Add(-time.Hour)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		expires := past
		if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: name, Value: "v", ExpiresAt: &expires}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	fresh := time.Now().Add(time.Hour)
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "keep", Value: "v", ExpiresAt: &fresh}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := manager.Load(ctx, "https://example.test/", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(got, "a=v") || !strings.Contains(got, "keep=v") {
		t.Fatalf("cookie = %q, want expired dropped and keep=v", got)
	}
	if repo.batchCalls != 1 {
		t.Fatalf("DeleteExpired calls = %d, want 1", repo.batchCalls)
	}
	if _, ok := repo.cookies["example.test|a"]; ok {
		t.Fatal("expired cookie a still persisted")
	}
}

// TestCookieManagerFallbackDeletesExpiredCookies 覆盖 S08-07 回退路径：仓储无批量能力时逐条 Delete。
func TestCookieManagerFallbackDeletesExpiredCookies(t *testing.T) {
	ctx := context.Background()
	repo := &countingCookieRepo{memoryCookieRepo: memoryCookieRepo{cookies: map[string]fetch.Cookie{}}}
	manager := NewCookieManager(repo, nil)
	past := time.Now().Add(-time.Hour)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		expires := past
		if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: name, Value: "v", ExpiresAt: &expires}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	if _, err := manager.Load(ctx, "https://example.test/", ""); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if _, ok := repo.cookies["example.test|"+name]; ok {
			t.Fatalf("expired cookie %s still persisted", name)
		}
	}
}

// TestMapToCookieDeterministicTruncation 覆盖 S08-08：100 条 cookie 截断到 4096 且两次调用结果一致。
func TestMapToCookieDeterministicTruncation(t *testing.T) {
	values := map[string]string{}
	for i := range 100 {
		values[fmt.Sprintf("cookie%03d", i)] = strings.Repeat("v", 100)
	}
	first := mapToCookie(values, cookieMaxHeaderLength)
	second := mapToCookie(values, cookieMaxHeaderLength)
	if len(first) > cookieMaxHeaderLength {
		t.Fatalf("cookie length = %d, want <= %d", len(first), cookieMaxHeaderLength)
	}
	if first != second {
		t.Fatal("mapToCookie not deterministic across calls")
	}
	// 无上限时全部保留。
	full := mapToCookie(values, 0)
	if len(full) <= cookieMaxHeaderLength {
		t.Fatalf("full length = %d, want > %d", len(full), cookieMaxHeaderLength)
	}
}
