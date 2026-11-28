/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	sharingModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingStore "github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

const consentChildOrgID = "consent-child-org-id"

// disableCDS removes the CDS configuration of the organization of the environment.
func (e *consentHandlerEnv) disableCDS(t *testing.T) {
	t.Helper()
	_, err := e.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, e.orgID)
	require.NoError(t, err)
}

// withChild stores a sub organization of the organization of the environment. The access policy of the
// primary organization reaches the sub organization only when withAccess is true.
func (e *consentHandlerEnv) withChild(t *testing.T, withAccess bool) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO organizations (org_id, org_handle, parent_org_id, primary_org_id, status)
		VALUES ($1, 'consent-child', $2, $2, 'ACTIVE')`, consentChildOrgID, e.orgID)
	require.NoError(t, err)
	if !withAccess {
		return
	}
	require.NoError(t, sharingStore.CreatePolicy(context.Background(), sharingModel.Policy{
		PolicyID:        "access-policy",
		ResourceType:    sharingModel.ResourceOrganizationAccess,
		ResourceID:      sharingModel.OrganizationAccessResourceID,
		OwningOrgID:     e.orgID,
		InitiatingOrgID: e.orgID,
		Targets:         []sharingModel.Target{{Scope: sharingModel.ScopeAllChildren, OrgID: e.orgID}},
	}))
}

// callAsChild runs a handler on a request in the sub organization, with a token of that organization.
func (e *consentHandlerEnv) callAsChild(t *testing.T, handler http.HandlerFunc, method, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	token := e.auth.NewJWT(t, map[string]interface{}{"org_id": consentChildOrgID})
	request := httptest.NewRequest(method, "/t/"+e.handle+"/o/"+consentChildOrgID+"/cds/api/v1/consent-categories",
		strings.NewReader(body))
	ctx := context.WithValue(request.Context(), constants.TenantContextKey, "consent-child")
	ctx = context.WithValue(ctx, constants.OrgIDContextKey, consentChildOrgID)
	request = request.WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

// addChildCategory stores a consent category of the sub organization.
func (e *consentHandlerEnv) addChildCategory(t *testing.T, id string) {
	t.Helper()
	err := consentStore.AddConsentCategory(context.Background(), consentChildOrgID, consentModel.ConsentCategory{
		CategoryName:       "Child " + id,
		CategoryIdentifier: id,
		OrgID:              consentChildOrgID,
		Purpose:            "profiling",
	})
	require.NoError(t, err)
}

type consentCall struct {
	name    string
	handler http.HandlerFunc
	method  string
	body    string
}

func (e *consentHandlerEnv) everyCall() []consentCall {
	return []consentCall{
		{"list", e.h.GetAllConsentCategories, http.MethodGet, ""},
		{"add", e.h.AddConsentCategory, http.MethodPost, `{"category_name":"New","purpose":"profiling"}`},
		{"get", e.h.GetConsentCategory, http.MethodGet, ""},
		{"update", e.h.UpdateConsentCategory, http.MethodPut, `{"category_name":"Renamed","purpose":"profiling"}`},
		{"delete", e.h.DeleteConsentCategory, http.MethodDelete, ""},
	}
}

func assertCDSNotEnabled(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, errors2.CDSNotEnabled.Code, body["code"])
}

func TestConsentCategoryHandlersRefuseAnOrganizationWithoutCDS(t *testing.T) {
	env := newConsentHandlerEnv(t)
	created := env.add(t, "Analytics")
	env.disableCDS(t)

	for _, tc := range env.everyCall() {
		t.Run(tc.name, func(t *testing.T) {
			assertCDSNotEnabled(t, env.call(t, tc.handler, tc.method, env.auth.ValidToken, tc.body,
				categoryID(created.CategoryIdentifier)))
		})
	}
	category, err := consentStore.GetConsentCategoryByID(context.Background(), created.CategoryIdentifier,
		env.orgID)
	require.NoError(t, err)
	require.NotNil(t, category, "the category is left alone")
	assert.Equal(t, "Analytics", category.CategoryName)
}

func TestConsentCategoryHandlersRefuseASubOrganizationOutsideTheAccessPolicy(t *testing.T) {
	env := newConsentHandlerEnv(t)
	env.withChild(t, false)
	env.addChildCategory(t, "child-category")

	for _, tc := range env.everyCall() {
		t.Run(tc.name, func(t *testing.T) {
			assertCDSNotEnabled(t, env.callAsChild(t, tc.handler, tc.method, tc.body, categoryID("child-category")))
		})
	}
}

func TestConsentCategoriesOfASubOrganizationInTheAccessPolicyAreListed(t *testing.T) {
	env := newConsentHandlerEnv(t)
	env.withChild(t, true)
	env.addChildCategory(t, "child-category")

	recorder := env.callAsChild(t, env.h.GetAllConsentCategories, http.MethodGet, "", nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "child-category")
}
