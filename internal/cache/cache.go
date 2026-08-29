package cache

import (
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

type FastMemoryCache struct {
	mu         sync.RWMutex
	items      map[string]cacheEntry
	maxEntries int
}

var GlobalCache *FastMemoryCache

func InitFastCache(maxEntries int) {
	if maxEntries <= 0 {
		maxEntries = 5000 // bounded to ~10-25MB memory footprint
	}
	GlobalCache = &FastMemoryCache{
		items:      make(map[string]cacheEntry),
		maxEntries: maxEntries,
	}

	// Periodic cleanup goroutine every 30 seconds
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			GlobalCache.cleanupExpired()
		}
	}()
}

func (c *FastMemoryCache) cleanupExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for k, v := range c.items {
		if now.After(v.expiresAt) {
			delete(c.items, k)
		}
	}
}

func (c *FastMemoryCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	entry, found := c.items[key]
	c.mu.RUnlock()

	if !found {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return nil, false
	}

	return entry.data, true
}

func (c *FastMemoryCache) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If maximum entries exceeded, purge oldest 10%
	if len(c.items) >= c.maxEntries {
		count := 0
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiresAt) || count < c.maxEntries/10 {
				delete(c.items, k)
				count++
			}
		}
	}

	c.items[key] = cacheEntry{
		data:      data,
		expiresAt: time.Now().Add(ttl),
	}
}

func (c *FastMemoryCache) InvalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k := range c.items {
		if strings.HasPrefix(k, prefix) {
			delete(c.items, k)
		}
	}
}

func (c *FastMemoryCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]cacheEntry)
}
