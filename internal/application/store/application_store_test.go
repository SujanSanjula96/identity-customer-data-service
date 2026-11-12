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
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/application/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var applicationOrgCounter atomic.Int64

func newApplicationEnv(t *testing.T) string {
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
	return setup.TestOrg(t, fmt.Sprintf("acme-%d", applicationOrgCounter.Add(1))).ID
}

func TestAnApplicationIsResolvedByItsClientID(t *testing.T) {
	ctx := context.Background()
	orgID := newApplicationEnv(t)

	require.NoError(t, UpsertApplication(ctx, orgID, model.Application{AppID: "app-1", ClientID: "client-1"}))

	got, err := GetAppIdentifierByClientID(ctx, orgID, "client-1")
	require.NoError(t, err)
	assert.Equal(t, "app-1", got)
}

func TestAnUnknownClientIDResolvesToNothing(t *testing.T) {
	ctx := context.Background()
	orgID := newApplicationEnv(t)

	got, err := GetAppIdentifierByClientID(ctx, orgID, "nobody")

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestARegisteredApplicationRefreshesItsClientID(t *testing.T) {
	ctx := context.Background()
	orgID := newApplicationEnv(t)
	require.NoError(t, UpsertApplication(ctx, orgID, model.Application{AppID: "app-1", ClientID: "client-1"}))

	require.NoError(t, UpsertApplication(ctx, orgID, model.Application{AppID: "app-1", ClientID: "client-2"}))

	renewed, err := GetAppIdentifierByClientID(ctx, orgID, "client-2")
	require.NoError(t, err)
	assert.Equal(t, "app-1", renewed)
	old, err := GetAppIdentifierByClientID(ctx, orgID, "client-1")
	require.NoError(t, err)
	assert.Empty(t, old, "the previous client ID no longer resolves")
}

func TestApplicationsWithoutAClientIDDoNotCollide(t *testing.T) {
	ctx := context.Background()
	orgID := newApplicationEnv(t)

	// SAML applications have no client ID.
	require.NoError(t, UpsertApplication(ctx, orgID, model.Application{AppID: "saml-1"}))
	require.NoError(t, UpsertApplication(ctx, orgID, model.Application{AppID: "saml-2"}))
}

func TestAClientIDResolvesOnlyInItsOrganization(t *testing.T) {
	ctx := context.Background()
	orgID := newApplicationEnv(t)
	require.NoError(t, UpsertApplication(ctx, orgID, model.Application{AppID: "app-1", ClientID: "client-1"}))

	got, err := GetAppIdentifierByClientID(ctx, "another-org", "client-1")

	require.NoError(t, err)
	assert.Empty(t, got)
}
