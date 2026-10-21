package script

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"

	"reader/internal/infra/logging"
)

const (
	defaultCacheMaxEntries  = 4096
	defaultCacheMaxBytes    = 16 << 20
	defaultCacheMaxValue    = 1 << 20
	defaultLogMaxEntries    = 1000
	defaultLogMaxBytes      = 256 << 10
	defaultProgramCacheSize = 1024
)

type cacheEntry struct {
	key       string
	value     string
	expiresAt time.Time
}

// NamespacedCache 是脚本 cache/java.put 的进程内存储，按书源命名空间隔离。
// 总条目数、总字节数与单值大小均有上限，超限时按最久未写入淘汰；条目可带 TTL。
type NamespacedCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	maxValue   int
	bytes      int
	order      *list.List
	entries    map[string]*list.Element
	now        func() time.Time
}

func NewNamespacedCache() *NamespacedCache {
	return newNamespacedCache(defaultCacheMaxEntries, defaultCacheMaxBytes, defaultCacheMaxValue)
}

func newNamespacedCache(maxEntries int, maxBytes int, maxValue int) *NamespacedCache {
	return &NamespacedCache{
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		maxValue:   maxValue,
		order:      list.New(),
		entries:    map[string]*list.Element{},
		now:        time.Now,
	}
}

func namespacedKey(namespace string, key string) string {
	return namespace + "\x00" + key
}

// Put 写入不过期的条目。
func (c *NamespacedCache) Put(namespace string, key string, value string) bool {
	return c.PutTTL(namespace, key, value, 0)
}

// PutTTL 写入条目；ttl<=0 表示不过期。单值超过上限时拒绝写入并返回 false。
// 淘汰循环先清除已过期条目（S05-03：过期条目不得占位挤掉新鲜条目），再按容量淘汰。
func (c *NamespacedCache) PutTTL(namespace string, key string, value string, ttl time.Duration) bool {
	full := namespacedKey(namespace, key)
	size := len(full) + len(value)
	c.mu.Lock()
	defer c.mu.Unlock()
	if size > c.maxValue || size > c.maxBytes {
		return false
	}
	c.removeLocked(full)
	entry := &cacheEntry{key: full, value: value}
	if ttl > 0 {
		entry.expiresAt = c.now().Add(ttl)
	}
	c.entries[full] = c.order.PushBack(entry)
	c.bytes += size
	for c.order.Len() > c.maxEntries || c.bytes > c.maxBytes {
		// 先清过期：淘汰循环里优先移除已过期的条目，避免它们占位。
		evicted := false
		for elem := c.order.Front(); elem != nil; elem = elem.Next() {
			candidate := elem.Value.(*cacheEntry)
			if !candidate.expiresAt.IsZero() && !c.now().Before(candidate.expiresAt) {
				c.removeLocked(candidate.key)
				evicted = true
				break
			}
		}
		if evicted {
			continue
		}
		oldest := c.order.Front()
		c.removeLocked(oldest.Value.(*cacheEntry).key)
	}
	return true
}

func (c *NamespacedCache) Get(namespace string, key string) (string, bool) {
	full := namespacedKey(namespace, key)
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[full]
	if !ok {
		return "", false
	}
	entry := elem.Value.(*cacheEntry)
	if !entry.expiresAt.IsZero() && !c.now().Before(entry.expiresAt) {
		c.removeLocked(full)
		return "", false
	}
	// LRU 语义：Get 命中续期（MoveToBack），纯读热点不被容量淘汰挤掉（S05-03）。
	c.order.MoveToBack(elem)
	return entry.value, true
}

func (c *NamespacedCache) Delete(namespace string, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(namespacedKey(namespace, key))
}

// Len 返回当前条目数（含尚未惰性清理的过期条目）。
func (c *NamespacedCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *NamespacedCache) removeLocked(full string) {
	elem, ok := c.entries[full]
	if !ok {
		return
	}
	entry := elem.Value.(*cacheEntry)
	c.bytes -= len(entry.key) + len(entry.value)
	c.order.Remove(elem)
	delete(c.entries, full)
}

// cacheValueString 对齐 Legado CacheManager.put 的 toString 语义；对象与数组以 JSON 保存。
func cacheValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err == nil {
			return string(encoded)
		}
	}
	return fmt.Sprint(value)
}

// logBuffer 是有界日志缓冲：条数或总字节超限时丢弃最旧的记录，单条超长时截断。
type logBuffer struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	entries    []string
}

func newLogBuffer(maxEntries int, maxBytes int) *logBuffer {
	return &logBuffer{maxEntries: maxEntries, maxBytes: maxBytes}
}

func (b *logBuffer) Append(message string) {
	message = logging.RedactString(message)
	if len(message) > b.maxBytes {
		message = message[:b.maxBytes]
		// 多字节 UTF-8 截断回退：最多退 3 字节避免在 rune 中间切断（S05-08）。
		for len(message) > 0 && !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, message)
	b.bytes += len(message)
	drop := 0
	for len(b.entries)-drop > b.maxEntries || b.bytes > b.maxBytes {
		b.bytes -= len(b.entries[drop])
		drop++
	}
	if drop > 0 {
		b.entries = append(b.entries[:0], b.entries[drop:]...)
	}
}

func (b *logBuffer) Snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.entries...)
}

// programCache 按 jsLib 内容哈希缓存编译结果（LRU）；*goja.Program 不可变，可在多个 VM 中并发运行。
type programCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	entries  map[[sha256.Size]byte]*list.Element
}

type programEntry struct {
	key     [sha256.Size]byte
	program *goja.Program
	// rewritten 是 rewriteJavaCompatScript 之后的文本（S05-02）：原文哈希命中时跳过改写。
	rewritten string
}

func newProgramCache(capacity int) *programCache {
	return &programCache{capacity: capacity, order: list.New(), entries: map[[sha256.Size]byte]*list.Element{}}
}

// rewriteCache 是"原文哈希 → 改写文本"的一级 LRU 缓存（S05-02）：缓存命中路径不再重复执行
// rewriteJavaCompatScript 的正则与 strings.Contains 扫描。
type rewriteCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	entries  map[[sha256.Size]byte]*list.Element
}

type rewriteEntry struct {
	key       [sha256.Size]byte
	rewritten string
}

func newRewriteCache(capacity int) *rewriteCache {
	return &rewriteCache{capacity: capacity, order: list.New(), entries: map[[sha256.Size]byte]*list.Element{}}
}

func (c *rewriteCache) get(source string) (string, bool) {
	key := sha256.Sum256([]byte(source))
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return "", false
	}
	c.order.MoveToBack(elem)
	return elem.Value.(*rewriteEntry).rewritten, true
}

func (c *rewriteCache) put(source string, rewritten string) {
	key := sha256.Sum256([]byte(source))
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToBack(elem)
		elem.Value.(*rewriteEntry).rewritten = rewritten
		return
	}
	c.entries[key] = c.order.PushBack(&rewriteEntry{key: key, rewritten: rewritten})
	for c.order.Len() > c.capacity {
		oldest := c.order.Front()
		delete(c.entries, oldest.Value.(*rewriteEntry).key)
		c.order.Remove(oldest)
	}
}

// rewriteJavaCompat 兜底入口：先查原文级改写缓存，未命中才执行改写（S05-02）。
func (c *rewriteCache) rewrite(source string) string {
	if rewritten, ok := c.get(source); ok {
		return rewritten
	}
	rewritten := rewriteJavaCompatScript(source)
	c.put(source, rewritten)
	return rewritten
}

func (c *programCache) compile(source string) (*goja.Program, error) {
	key := sha256.Sum256([]byte(source))
	c.mu.Lock()
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToBack(elem)
		program := elem.Value.(*programEntry).program
		c.mu.Unlock()
		return program, nil
	}
	c.mu.Unlock()
	program, err := goja.Compile("jsLib", rewriteDefault(source), false)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToBack(elem)
		return elem.Value.(*programEntry).program, nil
	}
	c.entries[key] = c.order.PushBack(&programEntry{key: key, program: program})
	for c.order.Len() > c.capacity {
		oldest := c.order.Front()
		delete(c.entries, oldest.Value.(*programEntry).key)
		c.order.Remove(oldest)
	}
	return program, nil
}

// rewriteDefault 与 compile 的键一致：compile 缓存键是原文哈希，改写必须对同一原文稳定。
var defaultRewriteCache = newRewriteCache(1024)

func rewriteDefault(source string) string {
	return defaultRewriteCache.rewrite(source)
}

// compileScript 缓存热点脚本（主脚本、loginCheck、@js: header、模板表达式）的编译结果。
// 缓存键包含调用点 site 与改写后脚本文本：相同文本在不同调用点语义可能不同（绑定不同），
// 不能误配；改写走原文级 LRU 缓存（S05-02），缓存命中路径不再重复跑正则。
func (c *programCache) compileScript(site string, source string) (*goja.Program, error) {
	rewritten := rewriteDefault(source)
	key := sha256.Sum256([]byte(site + "\x00" + rewritten))
	c.mu.Lock()
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToBack(elem)
		program := elem.Value.(*programEntry).program
		c.mu.Unlock()
		return program, nil
	}
	c.mu.Unlock()
	program, err := goja.Compile(site, rewritten, false)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToBack(elem)
		return elem.Value.(*programEntry).program, nil
	}
	c.entries[key] = c.order.PushBack(&programEntry{key: key, program: program})
	for c.order.Len() > c.capacity {
		oldest := c.order.Front()
		delete(c.entries, oldest.Value.(*programEntry).key)
		c.order.Remove(oldest)
	}
	return program, nil
}

func (c *programCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
