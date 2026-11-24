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

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func setUpOrganizationCacheDB(t *testing.T) *setup.SQLiteTestDB {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	return testDB
}

func TestTheOrganizationRowIsCachedAndClearedOnAChange(t *testing.T) {
	testDB := setUpOrganizationCacheDB(t)
	ctx := context.Background()
	orgID := setup.TestOrgID("cache-acme")
	require.NoError(t, setup.InsertTestOrganization(testDB.DB, orgID, "cache-acme"))

	byID, err := store.GetOrganizationByID(ctx, orgID)
	require.NoError(t, err)
	require.NotNil(t, byID)
	byHandle, err := store.GetOrganizationByHandle(ctx, "cache-acme")
	require.NoError(t, err)
	require.NotNil(t, byHandle)

	// A change that another node makes is not seen until the entry expires.
	_, err = testDB.DB.Exec(`UPDATE organizations SET org_name = 'Changed' WHERE org_id = $1`, orgID)
	require.NoError(t, err)
	byID, err = store.GetOrganizationByID(ctx, orgID)
	require.NoError(t, err)
	assert.Empty(t, byID.OrgName, "the row comes from the cache")
	byID.OrgName = "changed by a caller"
	again, err := store.GetOrganizationByID(ctx, orgID)
	require.NoError(t, err)
	assert.Empty(t, again.OrgName, "a caller cannot change the cached row")

	// A change on this node clears the row.
	require.NoError(t, store.UpdateOrganizationStatus(ctx, orgID, "DISABLED"))
	byID, err = store.GetOrganizationByID(ctx, orgID)
	require.NoError(t, err)
	assert.Equal(t, "DISABLED", byID.Status)
	assert.Equal(t, "Changed", byID.OrgName)
	byHandle, err = store.GetOrganizationByHandle(ctx, "cache-acme")
	require.NoError(t, err)
	assert.Equal(t, "DISABLED", byHandle.Status)

	require.NoError(t, store.UpsertOrganization(ctx, model.Organization{OrgID: orgID, OrgHandle: "cache-acme",
		OrgName: "Acme", PrimaryOrgID: orgID, Status: "ACTIVE"}))
	byHandle, err = store.GetOrganizationByHandle(ctx, "cache-acme")
	require.NoError(t, err)
	assert.Equal(t, "Acme", byHandle.OrgName)
	assert.Equal(t, "ACTIVE", byHandle.Status)
}

func TestAnOrganizationThatIsNotStoredIsNotCached(t *testing.T) {
	testDB := setUpOrganizationCacheDB(t)
	ctx := context.Background()
	orgID := setup.TestOrgID("cache-late")

	missing, err := store.GetOrganizationByHandle(ctx, "cache-late")
	require.NoError(t, err)
	assert.Nil(t, missing)

	require.NoError(t, setup.InsertTestOrganization(testDB.DB, orgID, "cache-late"))
	found, err := store.GetOrganizationByHandle(ctx, "cache-late")
	require.NoError(t, err)
	require.NotNil(t, found, "a row that another node stores is found on the next read")
	assert.Equal(t, orgID, found.OrgID)
}
