package script

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"

	"reader/internal/infra/logging"
)

const (
	defaultCacheMaxEntries  = 4096
	defaultCacheMaxBytes    = 16 << 20
	defaultCacheMaxValue    = 1 << 20
	defaultLogMaxEntries    = 1000
	defaultLogMaxBytes      = 256 << 10
	defaultProgramCacheSize = 64
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
}

func newProgramCache(capacity int) *programCache {
	return &programCache{capacity: capacity, order: list.New(), entries: map[[sha256.Size]byte]*list.Element{}}
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
	program, err := goja.Compile("jsLib", rewriteJavaCompatScript(source), false)
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
