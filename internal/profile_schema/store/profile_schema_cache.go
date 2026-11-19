/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
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

package store

import (
	"context"
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// ownedSchemaCache holds the schema attributes that each organization owns, keyed by org ID.
//
// Every profile write reads the whole schema of its organization at least twice (validation and
// the unification rules), and every unification job reads it again, while the schema changes only
// on admin writes and identity attribute syncs. Every write in this package invalidates the entry
// of its organization on this instance; the TTL bounds how long another instance may serve the
// old attributes.
var ownedSchemaCache = newSchemaCache(constants.ProfileSchemaCacheTTL)

type schemaCache struct {
	entries *cache.Cache

	// mu orders the fill of an entry against its invalidation. generation increases on every
	// invalidation, so a read that started before a write cannot put the attributes it read
	// before the write into the cache after the write invalidated it.
	mu         sync.Mutex
	generation uint64
}

func newSchemaCache(ttl time.Duration) *schemaCache {
	return &schemaCache{entries: cache.NewCache(ttl)}
}

// get returns a copy of the cached attributes of the organization. The caller may change the
// returned slice and its elements; the SubAttributes and CanonicalValues slices are shared and
// must not be changed.
func (c *schemaCache) get(orgID string) ([]model.ProfileSchemaAttribute, bool) {
	value, found := c.entries.Get(orgID)
	if !found {
		return nil, false
	}
	attrs, ok := value.([]model.ProfileSchemaAttribute)
	if !ok {
		return nil, false
	}
	return copyAttributes(attrs), true
}

// currentGeneration returns the generation that a read records before it queries the database.
func (c *schemaCache) currentGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// set caches a copy of the attributes, unless an invalidation happened after the read recorded
// readGeneration.
func (c *schemaCache) set(orgID string, attrs []model.ProfileSchemaAttribute, readGeneration uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != readGeneration {
		return
	}
	c.entries.Set(orgID, copyAttributes(attrs))
}

// invalidate drops the cached attributes of the organization.
func (c *schemaCache) invalidate(orgID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries.Delete(orgID)
}

// clear drops the cached attributes of every organization.
func (c *schemaCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries.Clear()
}

func copyAttributes(attrs []model.ProfileSchemaAttribute) []model.ProfileSchemaAttribute {
	return append(make([]model.ProfileSchemaAttribute, 0, len(attrs)), attrs...)
}

// invalidateOwnedSchema drops the cached attributes of the organization on this instance. Each
// write calls it with defer, so that the entry is dropped after the write, also when it fails.
func invalidateOwnedSchema(orgID string) {
	ownedSchemaCache.invalidate(orgID)
}

// InvalidateProfileSchemaCache drops the cached schema attributes of every organization on this
// instance. It is for tests and tools that write the profile_schema table directly.
func InvalidateProfileSchemaCache() {
	ownedSchemaCache.clear()
}

// GetProfileSchemaAttributesForOrg returns all profile schema attributes that the organization
// owns. The result is cached on this instance for constants.ProfileSchemaCacheTTL.
func GetProfileSchemaAttributesForOrg(ctx context.Context, orgID string) ([]model.ProfileSchemaAttribute, error) {
	// An organization that CDS does not know gets an empty key, which matches no row. It is not
	// cached.
	if orgID == "" {
		return loadProfileSchemaAttributesForOrg(ctx, orgID)
	}
	if attrs, found := ownedSchemaCache.get(orgID); found {
		return attrs, nil
	}
	readGeneration := ownedSchemaCache.currentGeneration()
	attrs, err := loadProfileSchemaAttributesForOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	ownedSchemaCache.set(orgID, attrs, readGeneration)
	return attrs, nil
}
