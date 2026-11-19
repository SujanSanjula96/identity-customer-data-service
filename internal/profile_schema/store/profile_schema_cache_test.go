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
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// cacheOrgCounter gives every test its own organization, so that no cached entry carries over
// from one test to the next.
var cacheOrgCounter atomic.Int64

type cacheEnv struct {
	ctx   context.Context
	db    *sql.DB
	orgID string
}

func newCacheEnv(t *testing.T) *cacheEnv {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	return &cacheEnv{ctx: context.Background(), db: testDB.DB,
		orgID: fmt.Sprintf("schema-cache-org-%d", cacheOrgCounter.Add(1))}
}

// insertDirectly writes a traits attribute without the store, so that the cache is not
// invalidated. A read that does not see the row was served from the cache.
func (e *cacheEnv) insertDirectly(t *testing.T, orgID, name string) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO profile_schema
		(attribute_id, org_id, attribute_name, display_name, value_type, merge_strategy,
		application_identifier, mutability, scope)
		VALUES (?, ?, ?, ?, 'string', 'overwrite', '', 'readWrite', 'traits')`,
		orgID+"-"+name, orgID, name, name)
	require.NoError(t, err)
}

func (e *cacheEnv) names(t *testing.T, orgID string) []string {
	t.Helper()
	attrs, err := GetProfileSchemaAttributesForOrg(e.ctx, orgID)
	require.NoError(t, err)
	names := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		names = append(names, attr.AttributeName)
	}
	return names
}

func traitAttribute(orgID, name string) model.ProfileSchemaAttribute {
	return model.ProfileSchemaAttribute{
		AttributeID:   orgID + "-" + name,
		AttributeName: name,
		DisplayName:   name,
		ValueType:     constants.StringDataType,
		MergeStrategy: "overwrite",
		Mutability:    constants.MutabilityReadWrite,
	}
}

func TestOwnedSchemaIsReadOnceAndThenServedFromTheCache(t *testing.T) {
	env := newCacheEnv(t)
	env.insertDirectly(t, env.orgID, "traits.city")

	assert.Equal(t, []string{"traits.city"}, env.names(t, env.orgID))

	env.insertDirectly(t, env.orgID, "traits.country")
	assert.Equal(t, []string{"traits.city"}, env.names(t, env.orgID),
		"the second read must come from the cache")
}

func TestOwnedSchemaIsCachedForEachOrganization(t *testing.T) {
	env := newCacheEnv(t)
	otherOrgID := env.orgID + "-other"
	env.insertDirectly(t, env.orgID, "traits.city")
	env.insertDirectly(t, otherOrgID, "traits.country")

	assert.Equal(t, []string{"traits.city"}, env.names(t, env.orgID))
	assert.Equal(t, []string{"traits.country"}, env.names(t, otherOrgID))

	require.NoError(t, AddProfileSchemaAttributesForScope(env.ctx,
		[]model.ProfileSchemaAttribute{traitAttribute(env.orgID, "traits.zip")}, constants.Traits, env.orgID))
	env.insertDirectly(t, otherOrgID, "traits.region")

	assert.ElementsMatch(t, []string{"traits.city", "traits.zip"}, env.names(t, env.orgID))
	assert.Equal(t, []string{"traits.country"}, env.names(t, otherOrgID),
		"a write in one organization must not drop the entry of another")
}

func TestEveryStoreWriteInvalidatesTheCachedSchema(t *testing.T) {
	writes := map[string]func(env *cacheEnv) error{
		"AddProfileSchemaAttributesForScope": func(env *cacheEnv) error {
			return AddProfileSchemaAttributesForScope(env.ctx,
				[]model.ProfileSchemaAttribute{traitAttribute(env.orgID, "traits.added")}, constants.Traits, env.orgID)
		},
		"PatchProfileSchemaAttributeByID": func(env *cacheEnv) error {
			return PatchProfileSchemaAttributeByID(env.ctx, env.orgID, env.orgID+"-traits.city",
				map[string]interface{}{"display_name": "Town"})
		},
		"DeleteProfileSchemaAttributeByID": func(env *cacheEnv) error {
			return DeleteProfileSchemaAttributeByID(env.ctx, env.orgID, env.orgID+"-traits.city")
		},
		"DeleteProfileSchemaAttributes": func(env *cacheEnv) error {
			return DeleteProfileSchemaAttributes(env.ctx, env.orgID, constants.Traits)
		},
		"PatchProfileSchemaAttributesForScope": func(env *cacheEnv) error {
			return PatchProfileSchemaAttributesForScope(env.ctx, env.orgID, constants.Traits,
				[]model.ProfileSchemaAttribute{traitAttribute(env.orgID, "traits.city")})
		},
		"DeleteProfileSchema": func(env *cacheEnv) error {
			return DeleteProfileSchema(env.ctx, env.orgID)
		},
		"UpsertIdentityAttributes": func(env *cacheEnv) error {
			return UpsertIdentityAttributes(env.ctx, env.orgID, nil)
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			env := newCacheEnv(t)
			env.insertDirectly(t, env.orgID, "traits.city")
			require.Equal(t, []string{"traits.city"}, env.names(t, env.orgID))
			env.insertDirectly(t, env.orgID, "traits.country")

			// The result of the write is not important: the entry must be dropped also when the
			// write fails.
			_ = write(env)

			stored, err := loadProfileSchemaAttributesForOrg(env.ctx, env.orgID)
			require.NoError(t, err)
			cached, err := GetProfileSchemaAttributesForOrg(env.ctx, env.orgID)
			require.NoError(t, err)
			assert.ElementsMatch(t, stored, cached, "the read after the write must see the database")
		})
	}
}

func TestAFailedWriteStillInvalidatesTheCachedSchema(t *testing.T) {
	env := newCacheEnv(t)
	env.insertDirectly(t, env.orgID, "traits.city")
	require.Equal(t, []string{"traits.city"}, env.names(t, env.orgID))
	env.insertDirectly(t, env.orgID, "traits.country")

	// The same attribute ID again violates the primary key.
	err := AddProfileSchemaAttributesForScope(env.ctx,
		[]model.ProfileSchemaAttribute{traitAttribute(env.orgID, "traits.city")}, constants.Traits, env.orgID)
	require.Error(t, err)

	assert.ElementsMatch(t, []string{"traits.city", "traits.country"}, env.names(t, env.orgID))
}

func TestAnUnknownOrganizationIsNotCached(t *testing.T) {
	env := newCacheEnv(t)
	before := ownedSchemaCache.entries.Len()

	assert.Empty(t, env.names(t, ""))
	assert.Equal(t, before, ownedSchemaCache.entries.Len())
}

func TestInvalidateProfileSchemaCacheDropsEveryOrganization(t *testing.T) {
	env := newCacheEnv(t)
	env.insertDirectly(t, env.orgID, "traits.city")
	require.Equal(t, []string{"traits.city"}, env.names(t, env.orgID))
	env.insertDirectly(t, env.orgID, "traits.country")

	InvalidateProfileSchemaCache()

	assert.ElementsMatch(t, []string{"traits.city", "traits.country"}, env.names(t, env.orgID))
}

func TestCachedSchemaExpiresAfterTheTTL(t *testing.T) {
	c := newSchemaCache(20 * time.Millisecond)
	c.set("org", []model.ProfileSchemaAttribute{{AttributeName: "traits.city"}}, c.currentGeneration())
	_, found := c.get("org")
	require.True(t, found)

	time.Sleep(40 * time.Millisecond)

	_, found = c.get("org")
	assert.False(t, found)
}

func TestAReadThatStartedBeforeAWriteDoesNotFillTheCache(t *testing.T) {
	c := newSchemaCache(time.Minute)
	readGeneration := c.currentGeneration()
	// The write commits and invalidates while the read still has the old rows.
	c.invalidate("org")

	c.set("org", []model.ProfileSchemaAttribute{{AttributeName: "traits.old"}}, readGeneration)

	_, found := c.get("org")
	assert.False(t, found, "the rows read before the write must not be cached")
}

func TestCallersCannotChangeTheCachedSchema(t *testing.T) {
	c := newSchemaCache(time.Minute)
	loaded := []model.ProfileSchemaAttribute{{AttributeName: "traits.city"}}
	c.set("org", loaded, c.currentGeneration())
	loaded[0].AttributeName = "changed by the loader"

	first, found := c.get("org")
	require.True(t, found)
	first[0].Origin = model.OriginInherited
	_ = append(first[:0], model.ProfileSchemaAttribute{AttributeName: "traits.appended"})

	second, found := c.get("org")
	require.True(t, found)
	require.Len(t, second, 1)
	assert.Equal(t, "traits.city", second[0].AttributeName)
	assert.Empty(t, second[0].Origin)
}

func TestConcurrentReadsAndWritesKeepTheCacheConsistent(t *testing.T) {
	env := newCacheEnv(t)
	env.insertDirectly(t, env.orgID, "traits.city")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				attrs, err := GetProfileSchemaAttributesForOrg(env.ctx, env.orgID)
				assert.NoError(t, err)
				for k := range attrs {
					attrs[k].Origin = model.OriginInherited
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		require.NoError(t, AddProfileSchemaAttributesForScope(env.ctx,
			[]model.ProfileSchemaAttribute{traitAttribute(env.orgID, fmt.Sprintf("traits.extra%d", i))},
			constants.Traits, env.orgID))
	}
	wg.Wait()

	stored, err := loadProfileSchemaAttributesForOrg(env.ctx, env.orgID)
	require.NoError(t, err)
	cached, err := GetProfileSchemaAttributesForOrg(env.ctx, env.orgID)
	require.NoError(t, err)
	assert.Len(t, cached, 11)
	assert.ElementsMatch(t, stored, cached, "after the last write, the cache must agree with the database")
}
