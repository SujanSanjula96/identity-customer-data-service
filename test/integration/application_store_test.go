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

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	appModel "github.com/wso2/identity-customer-data-service/internal/application/model"
	appProvider "github.com/wso2/identity-customer-data-service/internal/application/provider"
	appStore "github.com/wso2/identity-customer-data-service/internal/application/store"
)

func Test_ApplicationStore(t *testing.T) {

	org := fmt.Sprintf("carbon.super-app-%d", time.Now().UnixNano())
	otherOrg := fmt.Sprintf("other-app-%d", time.Now().UnixNano())
	appService := appProvider.NewApplicationProvider().GetApplicationService()

	appUUID := "40497337-e8bf-4d92-9545-711894ab2af3"
	clientID := "PjR8xK2qClientId"

	t.Run("Upsert_And_Resolve", func(t *testing.T) {
		err := appStore.UpsertApplication(context.Background(), appModel.Application{
			AppID:     appUUID,
			OrgHandle: org,
			ClientID:  clientID,
		})
		require.NoError(t, err)

		got, err := appStore.GetAppIdentifierByClientID(context.Background(), org, clientID)
		require.NoError(t, err)
		require.Equal(t, appUUID, got)
	})

	t.Run("Resolve_Miss_Returns_Empty", func(t *testing.T) {
		got, err := appStore.GetAppIdentifierByClientID(context.Background(), org, "unknown-client")
		require.NoError(t, err)
		require.Equal(t, "", got)

		// Service wrapper also returns empty on miss without surfacing an error.
		resolved, err := appService.ResolveAppIdentifierByClientID(context.Background(), org, "unknown-client")
		require.NoError(t, err)
		require.Equal(t, "", resolved)
	})

	t.Run("Org_Scoped", func(t *testing.T) {
		otherUUID := "aaaa1111-e8bf-4d92-9545-bbbbbbbbbbbb"
		err := appStore.UpsertApplication(context.Background(), appModel.Application{
			AppID:     otherUUID,
			OrgHandle: otherOrg,
			ClientID:  clientID, // same clientId, different org
		})
		require.NoError(t, err)

		gotOrg, err := appStore.GetAppIdentifierByClientID(context.Background(), org, clientID)
		require.NoError(t, err)
		require.Equal(t, appUUID, gotOrg)

		gotOther, err := appStore.GetAppIdentifierByClientID(context.Background(), otherOrg, clientID)
		require.NoError(t, err)
		require.Equal(t, otherUUID, gotOther)
	})

	t.Run("ClientId_Rotation_Updates_Mapping", func(t *testing.T) {
		newClientID := "RotatedClientId"
		err := appStore.UpsertApplication(context.Background(), appModel.Application{
			AppID:     appUUID, // same app
			OrgHandle: org,
			ClientID:  newClientID, // rotated clientId
		})
		require.NoError(t, err)

		// New clientId resolves to the app.
		got, err := appStore.GetAppIdentifierByClientID(context.Background(), org, newClientID)
		require.NoError(t, err)
		require.Equal(t, appUUID, got)

		// Old clientId no longer resolves (single row per app was updated in place).
		old, err := appStore.GetAppIdentifierByClientID(context.Background(), org, clientID)
		require.NoError(t, err)
		require.Equal(t, "", old)
	})

	t.Run("Saml_Apps_Stored_With_Null_ClientID", func(t *testing.T) {
		samlA := "saml-1111-e8bf-4d92-9545-cccccccccccc"
		samlB := "saml-2222-e8bf-4d92-9545-dddddddddddd"

		// SAML-only apps have no clientId; two of them in the same org must both persist without colliding.
		require.NoError(t, appStore.UpsertApplication(context.Background(), appModel.Application{AppID: samlA, OrgHandle: org}))
		require.NoError(t, appStore.UpsertApplication(context.Background(), appModel.Application{AppID: samlB, OrgHandle: org}))
	})
}
