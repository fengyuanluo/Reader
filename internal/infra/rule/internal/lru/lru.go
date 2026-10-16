// Package lru 提供规则解析层共用的并发安全定长 LRU 缓存，
// 用于复用正则编译、CSS 选择器解析等按字符串键计算的纯函数结果。
package lru

import (
	"container/list"
	"sync"
)

// Cache 是带互斥锁的定长 LRU 缓存；容量满时淘汰最久未使用的条目。
type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	items    map[K]*list.Element
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

// New 创建容量为 capacity 的缓存；capacity 小于 1 时按 1 处理。
func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	return &Cache[K, V]{capacity: capacity, order: list.New(), items: make(map[K]*list.Element, capacity)}
}

// GetOrCompute 命中时直接返回缓存值，否则调用 compute 计算并写入缓存。
// compute 在锁外执行，并发首次访问同一键时可能重复计算，但结果等价，取先写入者。
func (c *Cache[K, V]) GetOrCompute(key K, compute func() V) V {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		value := el.Value.(*entry[K, V]).value
		c.mu.Unlock()
		return value
	}
	c.mu.Unlock()

	value := compute()

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*entry[K, V]).value
	}
	c.items[key] = c.order.PushFront(&entry[K, V]{key: key, value: value})
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry[K, V]).key)
	}
	return value
}

// Len 返回当前缓存条目数。
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
