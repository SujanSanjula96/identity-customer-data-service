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
package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// introspectingIDP is an identity provider adapter that introspects tokens itself, and records the
// organizations it is asked for.
type introspectingIDP struct {
	idp.Adapter
	orgHandle     string
	primaryHandle string
}

func (f *introspectingIDP) IntrospectToken(_ context.Context, _, orgHandle,
	primaryHandle string) (map[string]interface{}, error) {
	f.orgHandle, f.primaryHandle = orgHandle, primaryHandle
	return map[string]interface{}{"active": true, "client_id": "CONSOLE"}, nil
}

// An opaque token is introspected through the adapter of the identity provider, for the organization
// of the request, as the authentication of the request does.
func TestGetCallerClientIDFromRequest_opaqueTokenIsIntrospectedThroughTheAdapter(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	config.OverrideCDSRuntime(config.Config{})
	fake := &introspectingIDP{}
	idp.SetAdapter(fake)
	t.Cleanup(func() { idp.SetAdapter(nil) })

	r := httptest.NewRequest(http.MethodGet, "/t/acme/o/sub-org-id/cds/api/v1/profiles/p1", nil)
	ctx := context.WithValue(r.Context(), constants.TenantContextKey, "region1")
	ctx = context.WithValue(ctx, constants.PrimaryOrgHandleContextKey, "acme")
	r = r.WithContext(ctx)
	r.Header.Set("Authorization", "Bearer opaque-token")

	assert.Equal(t, "CONSOLE", getCallerClientIDFromRequest(r))
	assert.Equal(t, "region1", fake.orgHandle)
	assert.Equal(t, "acme", fake.primaryHandle)
}
