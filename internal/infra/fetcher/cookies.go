package fetcher

import (
	"container/list"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"reader/internal/domain/fetch"

	"golang.org/x/net/publicsuffix"
)

const (
	// cookieCacheCapacity 是持久化 Cookie 域缓存的 LRU 容量上限（S00-04），
	// 防止 2 万书源场景下按 domain 无界增长。
	cookieCacheCapacity = 4096
	// cookieNegativeCacheTTL 是"该域没有持久化 Cookie"负缓存的存活时间（S00-04）。
	cookieNegativeCacheTTL = time.Minute
	// cookieMaxHeaderLength 与 Legado CookieStore 一致的单条 Cookie 头长度上限。
	cookieMaxHeaderLength = 4096
)

// expiredCookieBatchDeleter 是可选的批量删除接口（S08-07）：
// CookieRepository 的具体实现可以选择提供该能力，Load 用类型断言探测，
// 未提供时回退为逐条 Delete。
type expiredCookieBatchDeleter interface {
	DeleteExpired(ctx context.Context, domain string, before time.Time) (int64, error)
}

// SessionCookieStore 保存"会话期 Cookie"（无 Expires/Max-Age），进程重启后失效。
// 键为 Cookie 的归属域（Set-Cookie 声明的 Domain，缺省为当前 host），
// 读取时按 RFC 6265 domain-match 合并，避免父域请求带出子域的会话 Cookie（S08-06）。
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

// cookieCacheEntry 是域缓存的一个条目（S00-04）。
// minExpires 是条目内所有 Cookie 中最早的整体失效时间（空条目为零值）；
// expireAt 是条目自身在缓存中的存活期限（负缓存取 cookieNegativeCacheTTL）。
type cookieCacheEntry struct {
	cookies    []fetch.Cookie
	minExpires time.Time
	expireAt   time.Time
}

// cookieLRU 是有容量上限的 LRU 缓存（S00-04）：插入超过容量时淘汰最久未使用的域。
type cookieLRU struct {
	mu    sync.Mutex
	max   int
	order *list.List // 前端是最近使用
	items map[string]*list.Element
}

type cookieLRUItem struct {
	domain string
	entry  cookieCacheEntry
}

func newCookieLRU(max int) *cookieLRU {
	if max <= 0 {
		max = cookieCacheCapacity
	}
	return &cookieLRU{max: max, order: list.New(), items: map[string]*list.Element{}}
}

func (l *cookieLRU) get(domain string, now time.Time) (cookieCacheEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	elem, ok := l.items[domain]
	if !ok {
		return cookieCacheEntry{}, false
	}
	item := elem.Value.(*cookieLRUItem)
	if !item.entry.expireAt.IsZero() && !now.Before(item.entry.expireAt) {
		// 条目已过期（负缓存到期或整体失效），按未命中处理并淘汰。
		l.order.Remove(elem)
		delete(l.items, domain)
		return cookieCacheEntry{}, false
	}
	l.order.MoveToFront(elem)
	return item.entry, true
}

func (l *cookieLRU) put(domain string, entry cookieCacheEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elem, ok := l.items[domain]; ok {
		item := elem.Value.(*cookieLRUItem)
		item.entry = entry
		l.order.MoveToFront(elem)
		return
	}
	l.items[domain] = l.order.PushFront(&cookieLRUItem{domain: domain, entry: entry})
	for len(l.items) > l.max {
		oldest := l.order.Back()
		if oldest == nil {
			break
		}
		l.order.Remove(oldest)
		delete(l.items, oldest.Value.(*cookieLRUItem).domain)
	}
}

// invalidate 失效一个域的缓存条目（Save/Delete/批量删除后调用）。
func (l *cookieLRU) invalidate(domain string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elem, ok := l.items[domain]; ok {
		l.order.Remove(elem)
		delete(l.items, domain)
	}
}

func (l *cookieLRU) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

type CookieManager struct {
	repo    fetch.CookieRepository
	session *SessionCookieStore
	// cache 按 domain 的 LRU 缓存持久化 Cookie（S00-04）：容量上限 cookieCacheCapacity，
	// 空结果按 cookieNegativeCacheTTL 负缓存，条目带 minExpires 整体失效时间。
	cache *cookieLRU
}

func NewCookieManager(repo fetch.CookieRepository, session *SessionCookieStore) *CookieManager {
	if session == nil {
		session = NewSessionCookieStore()
	}
	return &CookieManager{repo: repo, session: session, cache: newCookieLRU(cookieCacheCapacity)}
}

// cachedCookies 返回 domain 的持久化 Cookie 缓存副本；缓存未命中时查一次 repo 并写入缓存。
// 空结果以短 TTL 负缓存，避免"该域确实没有 Cookie"的请求每次都打 SQLite。
// 命中路径返回切片副本，避免调用方在锁外遍历的同时原地写同一底层数组（数据竞争）。
func (m *CookieManager) cachedCookies(ctx context.Context, domain string) ([]fetch.Cookie, error) {
	now := time.Now()
	if entry, ok := m.cache.get(domain, now); ok {
		return append([]fetch.Cookie{}, entry.cookies...), nil
	}
	persisted, err := m.repo.ListByDomain(ctx, domain)
	if err != nil {
		return nil, err
	}
	entry := cookieCacheEntry{cookies: persisted, expireAt: now.Add(cookieNegativeCacheTTL)}
	for _, cookie := range persisted {
		// 条目整体失效时间取所有 Cookie 中最早的 ExpiresAt（S00-04）。
		if cookie.ExpiresAt != nil && (entry.minExpires.IsZero() || cookie.ExpiresAt.Before(entry.minExpires)) {
			entry.minExpires = *cookie.ExpiresAt
		}
	}
	if !entry.minExpires.IsZero() && entry.minExpires.After(entry.expireAt) {
		entry.expireAt = entry.minExpires
	}
	m.cache.put(domain, entry)
	return append([]fetch.Cookie{}, persisted...), nil
}

// cacheMuUpdate 在 LRU 条目内更新或删除单条 Cookie（Save 后同步更新）；条目不存在时忽略。
func (m *CookieManager) cacheMuUpdate(domain string, cookie fetch.Cookie, remove bool) {
	entry, ok := m.cache.get(domain, time.Now())
	if !ok {
		return
	}
	cookies := entry.cookies
	out := make([]fetch.Cookie, 0, len(cookies)+1)
	replaced := false
	for _, existing := range cookies {
		if existing.Name == cookie.Name {
			if remove {
				continue
			}
			out = append(out, cookie)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !remove && !replaced {
		out = append(out, cookie)
	}
	entry.cookies = out
	m.cache.put(domain, entry)
}

// mutateCache 修改一个域缓存条目内的 Cookie 列表（供测试模拟过期时间推进）。
// 条目不存在时忽略；修改后条目的 minExpires/expireAt 保持不变。
func (m *CookieManager) mutateCache(domain string, fn func([]fetch.Cookie) []fetch.Cookie) {
	entry, ok := m.cache.get(domain, time.Now())
	if !ok {
		return
	}
	entry.cookies = fn(entry.cookies)
	m.cache.put(domain, entry)
}

// domainMatch 实现 RFC 6265 5.1.3 的 domain-match：host 等于 domain，
// 或 host 是 domain 的子域（host 以 "."+domain 结尾）。两方都应为小写。
func domainMatch(host, domain string) bool {
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}

func (m *CookieManager) Load(ctx context.Context, rawURL string, requestCookie string) (string, error) {
	if m == nil {
		return requestCookie, nil
	}
	host, err := requestHost(rawURL)
	if err != nil {
		return requestCookie, nil
	}
	merged := cookieToMap(requestCookie)
	if m.repo != nil && host != "" {
		baseDomain, _ := cookieDomain(rawURL)
		persisted, err := m.loadPersisted(ctx, host, baseDomain)
		if err != nil {
			return "", err
		}
		now := time.Now()
		expired := make([]fetch.Cookie, 0)
		for _, cookie := range persisted {
			// 已过期（含 expires_at 损坏而按已过期处理）的持久化 Cookie 不再发送，批量从存储中清除（S08-07）。
			if cookie.ExpiresAt != nil && !cookie.ExpiresAt.After(now) {
				expired = append(expired, cookie)
				continue
			}
			// RFC 6265 domain-match：父域请求不带子域 Cookie（S08-06）。
			if !domainMatch(host, strings.TrimPrefix(cookie.Domain, ".")) {
				continue
			}
			merged[cookie.Name] = cookie.Value
		}
		if len(expired) > 0 {
			if err := m.deleteExpiredBatch(ctx, baseDomain, expired); err != nil {
				return "", err
			}
		}
	}
	if host != "" {
		// 会话 Cookie 按归属域做 RFC 6265 domain-match 合并（S08-06）。
		for owner, cookies := range m.session.domains() {
			if !domainMatch(host, strings.TrimPrefix(owner, ".")) {
				continue
			}
			for key, value := range cookies {
				merged[key] = value
			}
		}
	}
	return mapToCookie(merged, cookieMaxHeaderLength), nil
}

// loadPersisted 返回 host 命中（按 eTLD+1 与精确 host 两级查询）的持久化 Cookie。
func (m *CookieManager) loadPersisted(ctx context.Context, host, baseDomain string) ([]fetch.Cookie, error) {
	seen := map[string]struct{}{}
	out := make([]fetch.Cookie, 0, 8)
	domains := []string{baseDomain}
	if baseDomain != host {
		domains = append(domains, host)
	}
	for _, domain := range domains {
		if domain == "" {
			continue
		}
		persisted, err := m.cachedCookies(ctx, domain)
		if err != nil {
			return nil, err
		}
		for _, cookie := range persisted {
			key := cookie.Domain + "\x00" + cookie.Name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, cookie)
		}
	}
	return out, nil
}

// deleteExpiredBatch 一次性批量清除过期持久化 Cookie（S08-07）：
// 优先走仓储的批量删除能力（一次 SQLite 往返），未提供时回退逐条 Delete；
// 删除后整体失效该域缓存。
func (m *CookieManager) deleteExpiredBatch(ctx context.Context, domain string, expired []fetch.Cookie) error {
	domain = strings.TrimPrefix(domain, ".")
	if batch, ok := m.repo.(expiredCookieBatchDeleter); ok {
		// before 取过期项中最早的 ExpiresAt，删除所有早于该时间的 Cookie。
		before := time.Now().Add(time.Minute)
		for _, cookie := range expired {
			if cookie.ExpiresAt != nil && cookie.ExpiresAt.Before(before) {
				before = *cookie.ExpiresAt
			}
		}
		if _, err := batch.DeleteExpired(ctx, domain, before); err != nil {
			return err
		}
	} else {
		for _, cookie := range expired {
			owner := strings.TrimPrefix(cookie.Domain, ".")
			if owner == "" {
				owner = domain
			}
			if err := m.repo.Delete(ctx, owner, cookie.Name); err != nil {
				return err
			}
		}
	}
	// 已过期的会话 Cookie 同步清理。
	now := time.Now()
	for _, cookie := range expired {
		if cookie.ExpiresAt != nil && cookie.ExpiresAt.After(now) {
			continue
		}
		m.session.Delete(strings.TrimPrefix(cookie.Domain, "."), cookie.Name)
	}
	m.cache.invalidate(domain)
	return nil
}

// domains 返回会话 Cookie 的完整快照（owner 域 -> name/value）。
func (s *SessionCookieStore) domains() map[string]map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]map[string]string, len(s.cookies))
	for domain, cookies := range s.cookies {
		if len(cookies) == 0 {
			continue
		}
		snapshot := make(map[string]string, len(cookies))
		for key, value := range cookies {
			snapshot[key] = value
		}
		out[domain] = snapshot
	}
	return out
}

func (m *CookieManager) Save(ctx context.Context, rawURL string, headers http.Header) error {
	if m == nil {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return nil
	}
	domain, err := cookieDomain(rawURL)
	if err != nil {
		return nil
	}
	cookies := (&http.Response{Header: headers}).Cookies()
	sessionCookies := map[string][]*http.Cookie{}
	now := time.Now()
	for _, cookie := range cookies {
		// Set-Cookie 声明的 Domain 必须与当前 host 做 RFC 6265 domain-match（5.3 步骤 6），
		// 否则拒绝该 Cookie，避免跨站投毒。
		cookieHost := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if cookieHost != "" && !domainMatch(host, cookieHost) {
			continue
		}
		// 归属域：持久化 Cookie 沿用 Legado 二级域名语义（eTLD+1），声明了 Domain 用子域；
		// 会话 Cookie 按当前 host 粒度（S08-06），避免父域请求带出子域会话。
		owner := domain
		if cookieHost != "" {
			owner = cookieHost
		}
		if cookie.Expires.IsZero() && cookie.MaxAge == 0 {
			sessionOwner := host
			if cookieHost != "" {
				sessionOwner = cookieHost
			}
			sessionCookies[sessionOwner] = append(sessionCookies[sessionOwner], cookie)
			continue
		}
		// Max-Age 优先于 Expires（RFC 6265 5.3）；Max-Age<=0 或 Expires 已过去表示删除该 Cookie。
		expiresAt := cookie.Expires
		if cookie.MaxAge > 0 {
			expiresAt = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		if cookie.MaxAge < 0 || !expiresAt.After(now) {
			m.session.Delete(owner, cookie.Name)
			if m.repo != nil {
				if err := m.repo.Delete(ctx, owner, cookie.Name); err != nil {
					return err
				}
				m.cache.invalidate(owner)
			}
			continue
		}
		if m.repo != nil {
			persisted := fetch.Cookie{
				Domain:    owner,
				Name:      cookie.Name,
				Value:     cookie.Value,
				Path:      cookie.Path,
				ExpiresAt: &expiresAt,
			}
			if err := m.repo.Save(ctx, persisted); err != nil {
				return err
			}
			m.cacheMuUpdate(owner, persisted, false)
		}
	}
	if len(sessionCookies) > 0 && parsed.Hostname() != "" {
		for owner, list := range sessionCookies {
			m.session.Save(owner, list)
		}
	}
	return nil
}

// requestHost 返回 URL 的小写 host（空返回 ""）。
func requestHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return strings.ToLower(parsed.Hostname()), nil
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

// mapToCookie 按 key 排序后线性累计长度拼接（S08-08）：超长时停止追加并记 debug 日志，
// 保证同一批 Cookie 两次调用结果一致（确定性截断）。
func mapToCookie(values map[string]string, maxLen int) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	dropped := 0
	for i, key := range keys {
		pair := key + "=" + values[key]
		sep := 0
		if builder.Len() > 0 {
			sep = 2
		}
		if maxLen > 0 && builder.Len()+sep+len(pair) > maxLen {
			dropped = len(keys) - i
			break
		}
		if sep > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString(pair)
	}
	if dropped > 0 {
		slog.Debug("cookie header truncated", "dropped", dropped, "maxLen", maxLen)
	}
	return builder.String()
}
