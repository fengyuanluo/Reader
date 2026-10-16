package discovery

import (
	"sync"
	"time"
)

// 探索分类缓存参数：JS 生成分类的脚本可能依赖远端状态，因此设置 TTL 并限制条目数，
// 避免长期返回旧分类以及条目无限增长。
const (
	defaultCategoryCacheTTL      = 10 * time.Minute
	defaultCategoryCacheMaxItems = 256
)

type categoryCacheEntry struct {
	categories []Category
	expiresAt  time.Time
	insertedAt time.Time
}

// categoryCache 是带 TTL 与容量上限的进程级探索分类缓存，可安全并发使用。
type categoryCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxItems int
	entries  map[categoryCacheKey]categoryCacheEntry
	now      func() time.Time
}

func newCategoryCache(ttl time.Duration, maxItems int) *categoryCache {
	return &categoryCache{
		ttl:      ttl,
		maxItems: maxItems,
		entries:  map[categoryCacheKey]categoryCacheEntry{},
		now:      time.Now,
	}
}

// get 返回未过期且已克隆的分类列表。
func (c *categoryCache) get(key categoryCacheKey) ([]Category, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.expires(entry) {
		delete(c.entries, key)
		return nil, false
	}
	return cloneCategories(entry.categories), true
}

// put 写入分类列表并淘汰过期/最旧条目，保证条目数不超过上限。
func (c *categoryCache) put(key categoryCacheKey, categories []Category) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.evictExpired(now)
	if c.maxItems > 0 && len(c.entries) >= c.maxItems {
		if _, exists := c.entries[key]; !exists {
			c.evictOldest()
		}
	}
	c.entries[key] = categoryCacheEntry{
		categories: cloneCategories(categories),
		expiresAt:  now.Add(c.ttl),
		insertedAt: now,
	}
}

// invalidateSource 清除某个书源的全部探索分类缓存，对齐 Legado removeExploreKinds。
func (c *categoryCache) invalidateSource(sourceURL string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if key.sourceURL == sourceURL {
			delete(c.entries, key)
		}
	}
}

func (c *categoryCache) expires(entry categoryCacheEntry) bool {
	if c.ttl <= 0 {
		return false
	}
	return !c.now().Before(entry.expiresAt)
}

func (c *categoryCache) evictExpired(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

func (c *categoryCache) evictOldest() {
	var oldestKey categoryCacheKey
	var oldestAt time.Time
	first := true
	for key, entry := range c.entries {
		if first || entry.insertedAt.Before(oldestAt) {
			oldestKey = key
			oldestAt = entry.insertedAt
			first = false
		}
	}
	if !first {
		delete(c.entries, oldestKey)
	}
}
