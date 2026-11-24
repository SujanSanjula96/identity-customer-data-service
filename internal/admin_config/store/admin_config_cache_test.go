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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func TestTheAdminConfigIsCachedAndClearedOnAChange(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	ctx := context.Background()
	orgID := "cache-org"

	config, err := GetAdminConfig(ctx, orgID)
	require.NoError(t, err)
	assert.False(t, config.CDSEnabled)

	// A change that another node makes is not seen until the entry expires.
	_, err = testDB.DB.Exec(`INSERT INTO cds_config (org_id, config, value) VALUES ($1, $2, 'true')`,
		orgID, constants.ConfigCDSEnabled)
	require.NoError(t, err)
	config, err = GetAdminConfig(ctx, orgID)
	require.NoError(t, err)
	assert.False(t, config.CDSEnabled, "the configuration comes from the cache")

	config.SystemApplications = append(config.SystemApplications, "changed-by-a-caller")
	again, err := GetAdminConfig(ctx, orgID)
	require.NoError(t, err)
	assert.Empty(t, again.SystemApplications, "a caller cannot change the cached configuration")

	// A change on this node clears the entry.
	require.NoError(t, UpdateAdminConfig(ctx, model.AdminConfig{CDSEnabled: true, AutoMergeEnabled: true,
		DeterministicMatchDecisive: true}, orgID))
	config, err = GetAdminConfig(ctx, orgID)
	require.NoError(t, err)
	assert.True(t, config.CDSEnabled)
	assert.False(t, config.InitialSchemaSyncDone)

	require.NoError(t, UpdateInitialSchemaSyncConfig(ctx, true, orgID))
	config, err = GetAdminConfig(ctx, orgID)
	require.NoError(t, err)
	assert.True(t, config.InitialSchemaSyncDone)
}
