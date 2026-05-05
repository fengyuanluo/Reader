package fetcher

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"

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
}

func NewCookieManager(repo fetch.CookieRepository, session *SessionCookieStore) *CookieManager {
	if session == nil {
		session = NewSessionCookieStore()
	}
	return &CookieManager{repo: repo, session: session}
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
		persisted, err := m.repo.ListByDomain(ctx, domain)
		if err != nil {
			return "", err
		}
		for _, cookie := range persisted {
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
	for _, cookie := range cookies {
		if cookie.Expires.IsZero() && cookie.MaxAge == 0 {
			sessionCookies = append(sessionCookies, cookie)
			continue
		}
		if m.repo != nil {
			persisted := fetch.Cookie{
				Domain: domain,
				Name:   cookie.Name,
				Value:  cookie.Value,
				Path:   cookie.Path,
			}
			if !cookie.Expires.IsZero() {
				persisted.ExpiresAt = &cookie.Expires
			}
			if cookie.Domain != "" {
				persisted.Domain = strings.TrimPrefix(cookie.Domain, ".")
			}
			if err := m.repo.Save(ctx, persisted); err != nil {
				return err
			}
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
