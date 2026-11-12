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
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var thresholdOrgCounter atomic.Int64

type configEnv struct {
	ctx   context.Context
	orgID string
	org   orgModel.Ref
	svc   AdminConfigService
}

func newConfigEnv(t *testing.T) *configEnv {
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
	handle := fmt.Sprintf("acme-%d", thresholdOrgCounter.Add(1))
	org := setup.TestOrg(t, handle)
	return &configEnv{ctx: context.Background(), orgID: org.ID, org: org}
}

func TestGetAdminConfigDefaults(t *testing.T) {
	env := newConfigEnv(t)

	got, err := env.svc.GetAdminConfig(env.ctx, env.orgID)

	require.NoError(t, err)
	assert.Equal(t, env.orgID, got.OrgId)
	assert.False(t, got.CDSEnabled)
	assert.False(t, got.InitialSchemaSyncDone)
	assert.NotNil(t, got.SystemApplications)
	assert.Empty(t, got.SystemApplications)
	assert.False(t, env.svc.IsCDSEnabled(env.ctx, env.orgID))
	assert.False(t, env.svc.IsInitialSchemaSyncDone(env.ctx, env.orgID))
}

func TestUpdateAdminConfigStoresEverySetting(t *testing.T) {
	env := newConfigEnv(t)
	update := model.AdminConfig{
		SystemApplications:         []string{"app-1", "app-2"},
		AutoMergeEnabled:           true,
		AutoMergeThreshold:         0.9,
		ManualReviewThreshold:      0.7,
		DeterministicMatchDecisive: true,
		CORSAllowedOrigins:         []string{"https://app.example.com/"[:23], " https://other.example.com "},
	}

	require.NoError(t, env.svc.UpdateAdminConfig(env.ctx, &update, env.org))

	got, err := env.svc.GetAdminConfig(env.ctx, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, []string{"app-1", "app-2"}, got.SystemApplications)
	assert.True(t, got.AutoMergeEnabled)
	assert.InDelta(t, 0.9, got.AutoMergeThreshold, 1e-9)
	assert.InDelta(t, 0.7, got.ManualReviewThreshold, 1e-9)
	assert.True(t, got.DeterministicMatchDecisive)
	assert.Equal(t, []string{"https://app.example.com", "https://other.example.com"}, got.CORSAllowedOrigins)
	assert.Equal(t, got.CORSAllowedOrigins, update.CORSAllowedOrigins, "the update is normalized in place")

	origins, err := env.svc.GetCORSAllowedOrigins(env.ctx, env.orgID, "")
	require.NoError(t, err)
	assert.Equal(t, got.CORSAllowedOrigins, origins)
}

func TestUpdateAdminConfigRefusesInvalidThresholdsAndOrigins(t *testing.T) {
	env := newConfigEnv(t)

	err := env.svc.UpdateAdminConfig(env.ctx, &model.AdminConfig{AutoMergeThreshold: 2}, env.org)
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	assert.Equal(t, http.StatusBadRequest, clientError.StatusCode)

	err = env.svc.UpdateAdminConfig(env.ctx, &model.AdminConfig{CORSAllowedOrigins: []string{"*"}}, env.org)
	require.ErrorAs(t, err, &clientError)
	assert.Equal(t, http.StatusBadRequest, clientError.StatusCode)
}

func TestIsSystemApplication(t *testing.T) {
	env := newConfigEnv(t)

	none, err := env.svc.IsSystemApplication(env.ctx, env.orgID, "app-1")
	require.NoError(t, err)
	assert.False(t, none)

	require.NoError(t, env.svc.UpdateAdminConfig(env.ctx,
		&model.AdminConfig{SystemApplications: []string{"app-1"}}, env.org))

	yes, err := env.svc.IsSystemApplication(env.ctx, env.orgID, "app-1")
	require.NoError(t, err)
	assert.True(t, yes)
	other, err := env.svc.IsSystemApplication(env.ctx, env.orgID, "app-2")
	require.NoError(t, err)
	assert.False(t, other)
}

func TestUpdateInitialSchemaSync(t *testing.T) {
	env := newConfigEnv(t)

	require.NoError(t, env.svc.UpdateInitialSchemaSync(env.ctx, true, env.orgID))
	assert.True(t, env.svc.IsInitialSchemaSyncDone(env.ctx, env.orgID))

	require.NoError(t, env.svc.UpdateInitialSchemaSync(env.ctx, false, env.orgID))
	assert.False(t, env.svc.IsInitialSchemaSyncDone(env.ctx, env.orgID))
}
