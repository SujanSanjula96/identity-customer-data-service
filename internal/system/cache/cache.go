/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

// Package cache provides an in-memory cache with a time-to-live.
package cache

import (
	"fmt"
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// CacheItem is a cached value and the time it expires.
type CacheItem struct {
	Value      interface{}
	Expiration time.Time
}

// Cache is an in-memory key-value store that gives every entry the same TTL. It is safe for
// concurrent use.
type Cache struct {
	items map[string]CacheItem
	mutex sync.RWMutex
	ttl   time.Duration
	// sweepAt is the entry count that triggers a sweep of expired items on the next Set.
	// It rises with the cache so a genuinely large working set is not swept on every write.
	sweepAt int
}

// initialSweepThreshold is the entry count at which a cache first sweeps expired items.
const initialSweepThreshold = 1024

// registry holds every cache that NewCache made, so that ClearAll can reach them.
var (
	registryMu sync.Mutex
	registry   []*Cache
)

// NewCache creates a new cache with a TTL (time-to-live)
func NewCache(defaultTTL time.Duration) *Cache {
	c := &Cache{
		items:   make(map[string]CacheItem),
		ttl:     defaultTTL,
		sweepAt: initialSweepThreshold,
	}
	registryMu.Lock()
	registry = append(registry, c)
	registryMu.Unlock()
	return c
}

// ClearAll removes every entry of every cache. A switch to another database makes every cached row
// invalid, so the test hook that switches the database calls it.
func ClearAll() {
	registryMu.Lock()
	caches := append([]*Cache(nil), registry...)
	registryMu.Unlock()
	for _, c := range caches {
		c.Clear()
	}
}

// Set adds an item to the cache
func (c *Cache) Set(key string, value interface{}) {
	logger := log.GetLogger()
	logger.Debug(fmt.Sprint("Setting cache for key: ", key))
	c.mutex.Lock()
	defer c.mutex.Unlock()

	expiration := time.Now().Add(c.ttl)
	c.items[key] = CacheItem{
		Value:      value,
		Expiration: expiration,
	}

	// An expired entry is invisible to Get but still occupies the map, so a cache keyed by
	// something high-cardinality — an email address, say — would otherwise grow for the
	// life of the process and never shrink. Sweeping on a size trigger keeps that bounded
	// without a background goroutine that would outlive the cache.
	if len(c.items) >= c.sweepAt {
		c.sweepExpiredLocked()
	}
}

// sweepExpiredLocked drops every expired entry. The caller must hold the write lock.
func (c *Cache) sweepExpiredLocked() {
	now := time.Now()
	for key, item := range c.items {
		if now.After(item.Expiration) {
			delete(c.items, key)
		}
	}

	// If sweeping freed little, the live set really is this large; raise the bar so the
	// next sweep is not attempted on every subsequent write.
	if next := len(c.items) * 2; next > c.sweepAt {
		c.sweepAt = next
	} else if initialSweepThreshold > c.sweepAt {
		c.sweepAt = initialSweepThreshold
	}
}

// Get retrieves an item from the cache
func (c *Cache) Get(key string) (interface{}, bool) {
	logger := log.GetLogger()
	logger.Debug(fmt.Sprint("Getting cache for key: ", key))
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item, found := c.items[key]
	if !found {
		logger.Debug(fmt.Sprint("Cache not found for key: ", key))
		return nil, false
	}
	// Check if expired
	if time.Now().After(item.Expiration) {
		logger.Debug(fmt.Sprint("Cache expired for key: ", key))
		c.mutex.RUnlock()
		c.mutex.Lock()
		// Re-check: another writer may have refreshed the entry while the lock was handed over.
		if current, stillThere := c.items[key]; stillThere && time.Now().After(current.Expiration) {
			delete(c.items, key)
		}
		c.mutex.Unlock()
		c.mutex.RLock()
		return nil, false
	}

	return item.Value, true
}

// Len reports how many entries the cache currently holds, expired ones included.
func (c *Cache) Len() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.items)
}

// Delete removes an item from the cache
func (c *Cache) Delete(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.items, key)
}

// Clear removes every entry. Used where a write invalidates a whole cached namespace and
// threading the affected keys through the write path would cost more than re-reading.
func (c *Cache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.items = make(map[string]CacheItem)
	c.sweepAt = initialSweepThreshold
}
