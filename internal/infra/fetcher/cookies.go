package fetcher

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"reader/internal/domain/fetch"

	"golang.org/x/net/publicsuffix"
)

type SessionCookieStore struct {
	mu      sync.Mutex
	cookies map[string]map[string]string
}

func NewSessionCookieStore() *SessionCookieStore {
	return &SessionCookieStore{cookies: map[string]map[string]string{}}
}

func (s *SessionCookieStore) Save(domain string, cookies []*http.Cookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookies[domain] == nil {
		s.cookies[domain] = map[string]string{}
	}
	for _, cookie := range cookies {
		s.cookies[domain][cookie.Name] = cookie.Value
	}
}

func (s *SessionCookieStore) Delete(domain string, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cookies[domain], name)
}

func (s *SessionCookieStore) List(domain string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for key, value := range s.cookies[domain] {
		out[key] = value
	}
	return out
}

type CookieManager struct {
	repo    fetch.CookieRepository
	session *SessionCookieStore
	// cache 按 domain 缓存持久化 Cookie（C-027），Load 命中时不再查 SQLite；
	// Save/Delete 后同步更新或失效对应 domain 的缓存。
	cacheMu sync.RWMutex
	cache   map[string][]fetch.Cookie
}

func NewCookieManager(repo fetch.CookieRepository, session *SessionCookieStore) *CookieManager {
	if session == nil {
		session = NewSessionCookieStore()
	}
	return &CookieManager{repo: repo, session: session, cache: map[string][]fetch.Cookie{}}
}

// cachedCookies 返回 domain 的持久化 Cookie 缓存副本；缓存未命中时查一次 repo 并写入缓存。
// 返回的 bool 表示缓存是否命中；命中时不再访问 repo。
// 命中路径返回切片副本，避免调用方在锁外遍历的同时 setCacheCookie 原地写同一底层数组（数据竞争）。
func (m *CookieManager) cachedCookies(ctx context.Context, domain string) ([]fetch.Cookie, bool, error) {
	m.cacheMu.RLock()
	cached, ok := m.cache[domain]
	if ok {
		cached = append([]fetch.Cookie{}, cached...)
	}
	m.cacheMu.RUnlock()
	if ok {
		return cached, true, nil
	}
	persisted, err := m.repo.ListByDomain(ctx, domain)
	if err != nil {
		return nil, false, err
	}
	m.cacheMu.Lock()
	m.cache[domain] = persisted
	m.cacheMu.Unlock()
	return append([]fetch.Cookie{}, persisted...), false, nil
}

// setCacheCookie 在缓存中更新一条 Cookie（Save 后同步更新）。
func (m *CookieManager) setCacheCookie(domain string, cookie fetch.Cookie) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	cookies := m.cache[domain]
	for i := range cookies {
		if cookies[i].Name == cookie.Name {
			cookies[i] = cookie
			return
		}
	}
	m.cache[domain] = append(append([]fetch.Cookie{}, cookies...), cookie)
}

// deleteCacheCookie 在缓存中删除一条 Cookie（删除后同步失效）。
func (m *CookieManager) deleteCacheCookie(domain, name string) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	cookies := m.cache[domain]
	out := cookies[:0:0]
	for _, c := range cookies {
		if c.Name != name {
			out = append(out, c)
		}
	}
	m.cache[domain] = out
}

func (m *CookieManager) Load(ctx context.Context, rawURL string, requestCookie string) (string, error) {
	if m == nil {
		return requestCookie, nil
	}
	domain, err := cookieDomain(rawURL)
	if err != nil {
		return requestCookie, nil
	}
	merged := cookieToMap(requestCookie)
	if m.repo != nil {
		persisted, _, err := m.cachedCookies(ctx, domain)
		if err != nil {
			return "", err
		}
		now := time.Now()
		for _, cookie := range persisted {
			// 已过期（含 expires_at 损坏而按已过期处理）的持久化 Cookie 不再发送，并批量从存储中清除。
			if cookie.ExpiresAt != nil && !cookie.ExpiresAt.After(now) {
				if err := m.repo.Delete(ctx, cookie.Domain, cookie.Name); err != nil {
					return "", err
				}
				m.deleteCacheCookie(domain, cookie.Name)
				continue
			}
			merged[cookie.Name] = cookie.Value
		}
	}
	for key, value := range m.session.List(domain) {
		merged[key] = value
	}
	return mapToCookie(merged, 4096), nil
}

func (m *CookieManager) Save(ctx context.Context, rawURL string, headers http.Header) error {
	if m == nil {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	domain, err := cookieDomain(rawURL)
	if err != nil {
		return nil
	}
	cookies := (&http.Response{Header: headers}).Cookies()
	sessionCookies := []*http.Cookie{}
	now := time.Now()
	for _, cookie := range cookies {
		if cookie.Expires.IsZero() && cookie.MaxAge == 0 {
			sessionCookies = append(sessionCookies, cookie)
			continue
		}
		persistDomain := domain
		if cookie.Domain != "" {
			persistDomain = strings.TrimPrefix(cookie.Domain, ".")
		}
		// Max-Age 优先于 Expires（RFC 6265 5.3）；Max-Age<=0 或 Expires 已过去表示删除该 Cookie。
		expiresAt := cookie.Expires
		if cookie.MaxAge > 0 {
			expiresAt = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		if cookie.MaxAge < 0 || !expiresAt.After(now) {
			m.session.Delete(domain, cookie.Name)
			if m.repo != nil {
				if err := m.repo.Delete(ctx, persistDomain, cookie.Name); err != nil {
					return err
				}
				m.deleteCacheCookie(persistDomain, cookie.Name)
			}
			continue
		}
		if m.repo != nil {
			persisted := fetch.Cookie{
				Domain:    persistDomain,
				Name:      cookie.Name,
				Value:     cookie.Value,
				Path:      cookie.Path,
				ExpiresAt: &expiresAt,
			}
			if err := m.repo.Save(ctx, persisted); err != nil {
				return err
			}
			m.setCacheCookie(persistDomain, persisted)
		}
	}
	if len(sessionCookies) > 0 {
		if parsed.Hostname() != "" {
			m.session.Save(domain, sessionCookies)
		}
	}
	return nil
}

func cookieDomain(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.TrimSpace(strings.ToLower(parsed.Hostname()))
	if host == "" {
		return "", nil
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && strings.TrimSpace(domain) != "" {
		return domain, nil
	}
	return strings.TrimPrefix(host, "www."), nil
}

func cookieToMap(cookie string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(cookie, ";") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			continue
		}
		if pair[0] == "" {
			continue
		}
		out[pair[0]] = strings.TrimSpace(pair[1])
	}
	return out
}

func mapToCookie(values map[string]string, maxLen int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for key, value := range values {
		next := key + "=" + value
		if maxLen > 0 && len(strings.Join(append(parts, next), "; ")) > maxLen {
			continue
		}
		parts = append(parts, next)
	}
	return strings.Join(parts, "; ")
}
