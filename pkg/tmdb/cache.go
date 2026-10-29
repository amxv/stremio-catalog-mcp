package tmdb

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	maxCacheEntries = 500
	lookupTTL       = 15 * time.Minute
	notFoundTTL     = 2 * time.Minute
)

type cacheEntry struct {
	value   Item
	expires time.Time
}

type lookupCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	flights singleflight.Group
}

var sharedLookupCache = newLookupCache()

func newLookupCache() *lookupCache {
	return &lookupCache{entries: make(map[string]cacheEntry)}
}

func (c *lookupCache) get(key string) (Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		delete(c.entries, key)
		return Item{}, false
	}
	return entry.value, true
}

func (c *lookupCache) put(key string, value Item) {
	if value.Status == "error" {
		return
	}
	ttl := lookupTTL
	if value.Status == "not_found" {
		ttl = notFoundTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxCacheEntries {
		var oldestKey string
		var oldestTime time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.expires.Before(oldestTime) {
				oldestKey, oldestTime = key, entry.expires
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = cacheEntry{value: value, expires: time.Now().Add(ttl)}
}

func (c *lookupCache) lookup(_ context.Context, spec titleQuery, query string, resolve func() Item) Item {
	key := fmt.Sprintf("%s:%d:%s:%s:%t", normalize(spec.Name), spec.Year, spec.Kind, spec.Country, spec.Anime)
	if result, ok := c.get(key); ok {
		result.Query = query
		return result
	}
	// Coalesce simultaneous calls, including overlapping batches in distinct
	// requests on the same warm Vercel instance.
	value, _, _ := c.flights.Do(key, func() (any, error) {
		if result, ok := c.get(key); ok {
			return result, nil
		}
		result := resolve()
		c.put(key, result)
		return result, nil
	})
	result := value.(Item)
	result.Query = query
	return result
}
