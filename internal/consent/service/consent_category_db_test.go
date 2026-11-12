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
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	"github.com/wso2/identity-customer-data-service/internal/consent/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var consentOrgCounter atomic.Int64

type consentEnv struct {
	ctx   context.Context
	db    *sql.DB
	orgID string
	svc   *ConsentCategoryService
}

func newConsentEnv(t *testing.T) *consentEnv {
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

	handle := fmt.Sprintf("acme-%d", consentOrgCounter.Add(1))
	env := &consentEnv{ctx: context.Background(), db: testDB.DB, orgID: setup.TestOrgId(handle),
		svc: &ConsentCategoryService{}}
	require.NoError(t, setup.InsertTestOrganization(testDB.DB, env.orgID, handle))
	env.addAttribute(t, constants.Traits, "traits.age", "")
	env.addAttribute(t, constants.Traits, "traits.city", "")
	env.addAttribute(t, constants.ApplicationData, "application_data.last_purchase", "crm_app")
	return env
}

func (e *consentEnv) addAttribute(t *testing.T, scope, name, appIdentifier string) {
	t.Helper()
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(e.ctx, []schemaModel.ProfileSchemaAttribute{{
		AttributeId:           "attr-" + name,
		AttributeName:         name,
		Scope:                 scope,
		ApplicationIdentifier: appIdentifier,
		ValueType:             constants.StringDataType,
		MergeStrategy:         "overwrite",
		Mutability:            constants.MutabilityReadWrite,
	}}, scope, e.orgID))
}

func category(name, purpose string, attributes ...consentModel.ConsentAttribute) consentModel.ConsentCategory {
	return consentModel.ConsentCategory{CategoryName: name, Purpose: purpose, Attributes: attributes}
}

func (e *consentEnv) mustAdd(t *testing.T, c consentModel.ConsentCategory) *consentModel.ConsentCategory {
	t.Helper()
	added, err := e.svc.AddConsentCategory(e.ctx, e.orgID, c)
	require.NoError(t, err)
	require.NotNil(t, added)
	return added
}

func requireClientStatus(t *testing.T, err error, status int) *errors2.ClientError {
	t.Helper()
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	require.Equal(t, status, clientError.StatusCode)
	return clientError
}

func TestAddConsentCategoryGeneratesTheIDAndResolvesTheAttributes(t *testing.T) {
	env := newConsentEnv(t)

	added := env.mustAdd(t, consentModel.ConsentCategory{
		CategoryName:       "Analytics",
		CategoryIdentifier: "caller-supplied-id",
		Purpose:            "profiling",
		Destinations:       []string{"warehouse"},
		Attributes: []consentModel.ConsentAttribute{
			{AttributeName: "traits.age"},
			{AttributeName: "application_data.last_purchase", ApplicationIdentifier: "crm_app"},
		},
	})

	assert.NotEqual(t, "caller-supplied-id", added.CategoryIdentifier, "the identifier is always server generated")
	require.Len(t, added.Attributes, 2)
	assert.Equal(t, constants.Traits, added.Attributes[0].Scope)
	assert.Equal(t, "attr-traits.age", added.Attributes[0].AttributeId)
	assert.Equal(t, constants.ApplicationData, added.Attributes[1].Scope)

	got, err := env.svc.GetConsentCategory(env.ctx, added.CategoryIdentifier, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "Analytics", got.CategoryName)
	assert.Equal(t, []string{"warehouse"}, got.Destinations)
	assert.Len(t, got.Attributes, 2)
	assert.False(t, got.IsMandatory)
}

func TestAddConsentCategoryRefusals(t *testing.T) {
	env := newConsentEnv(t)
	env.mustAdd(t, category("Analytics", "profiling"))

	tests := []struct {
		name   string
		cat    consentModel.ConsentCategory
		status int
	}{
		{"no name", category("", "profiling"), http.StatusBadRequest},
		{"no purpose", category("X", ""), http.StatusBadRequest},
		{"an unknown purpose", category("X", "marketing"), http.StatusBadRequest},
		{"an attribute without a name", category("X", "profiling", consentModel.ConsentAttribute{}),
			http.StatusBadRequest},
		{"an attribute that is not in the schema", category("X", "profiling",
			consentModel.ConsentAttribute{AttributeName: "traits.nothing"}), http.StatusBadRequest},
		{"application data without an application", category("X", "profiling",
			consentModel.ConsentAttribute{AttributeName: "application_data.last_purchase"}), http.StatusBadRequest},
		{"application data of another application", category("X", "profiling",
			consentModel.ConsentAttribute{AttributeName: "application_data.last_purchase",
				ApplicationIdentifier: "other"}),
			http.StatusBadRequest},
		{"a name that is taken", category("Analytics", "profiling"), http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			added, err := env.svc.AddConsentCategory(env.ctx, env.orgID, tc.cat)

			assert.Nil(t, added)
			_ = requireClientStatus(t, err, tc.status)
		})
	}
}

func TestGetAllConsentCategories(t *testing.T) {
	env := newConsentEnv(t)

	empty, err := env.svc.GetAllConsentCategories(env.ctx, env.orgID)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	env.mustAdd(t, category("Analytics", "profiling", consentModel.ConsentAttribute{AttributeName: "traits.age"}))
	env.mustAdd(t, category("Offers", "personalization"))

	all, err := env.svc.GetAllConsentCategories(env.ctx, env.orgID)
	require.NoError(t, err)
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.CategoryName)
	}
	assert.ElementsMatch(t, []string{"Analytics", "Offers"}, names)
}

func TestGetConsentCategoryNotFound(t *testing.T) {
	env := newConsentEnv(t)

	_, err := env.svc.GetConsentCategory(env.ctx, "missing", env.orgID)

	clientError := requireClientStatus(t, err, http.StatusNotFound)
	assert.Equal(t, errors2.CONSENT_CAT_NOT_FOUND.Code, clientError.Code)
}

func TestConsentCategoriesAreScopedToTheirOrganization(t *testing.T) {
	env := newConsentEnv(t)
	added := env.mustAdd(t, category("Analytics", "profiling"))

	_, err := env.svc.GetConsentCategory(env.ctx, added.CategoryIdentifier, "another-org")

	_ = requireClientStatus(t, err, http.StatusNotFound)
}

func TestUpdateConsentCategory(t *testing.T) {
	env := newConsentEnv(t)
	added := env.mustAdd(t, category("Analytics", "profiling",
		consentModel.ConsentAttribute{AttributeName: "traits.age"}))

	update := category("Analytics v2", "personalization", consentModel.ConsentAttribute{AttributeName: "traits.city"})
	update.CategoryIdentifier = added.CategoryIdentifier
	require.NoError(t, env.svc.UpdateConsentCategory(env.ctx, env.orgID, update))

	got, err := env.svc.GetConsentCategory(env.ctx, added.CategoryIdentifier, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "Analytics v2", got.CategoryName)
	assert.Equal(t, "personalization", got.Purpose)
	require.Len(t, got.Attributes, 1)
	assert.Equal(t, "traits.city", got.Attributes[0].AttributeName)
}

func TestUpdateConsentCategoryRefusals(t *testing.T) {
	env := newConsentEnv(t)
	added := env.mustAdd(t, category("Analytics", "profiling"))
	with := func(id string, c consentModel.ConsentCategory) consentModel.ConsentCategory {
		c.CategoryIdentifier = id
		return c
	}

	tests := []struct {
		name   string
		cat    consentModel.ConsentCategory
		status int
	}{
		{"no identifier", category("X", "profiling"), http.StatusBadRequest},
		{"an invalid purpose", with(added.CategoryIdentifier, category("X", "nope")), http.StatusBadRequest},
		{"an unknown category", with("missing", category("X", "profiling")), http.StatusNotFound},
		{"an attribute that is not in the schema", with(added.CategoryIdentifier, category("X", "profiling",
			consentModel.ConsentAttribute{AttributeName: "traits.nothing"})), http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_ = requireClientStatus(t, env.svc.UpdateConsentCategory(env.ctx, env.orgID, tc.cat), tc.status)
		})
	}
}

func TestDeleteConsentCategory(t *testing.T) {
	env := newConsentEnv(t)
	added := env.mustAdd(t, category("Analytics", "profiling",
		consentModel.ConsentAttribute{AttributeName: "traits.age"}))

	require.NoError(t, env.svc.DeleteConsentCategory(env.ctx, added.CategoryIdentifier, env.orgID))

	_, err := env.svc.GetConsentCategory(env.ctx, added.CategoryIdentifier, env.orgID)
	_ = requireClientStatus(t, err, http.StatusNotFound)
	// A category that is gone is not an error.
	assert.NoError(t, env.svc.DeleteConsentCategory(env.ctx, added.CategoryIdentifier, env.orgID))
	_ = requireClientStatus(t, env.svc.DeleteConsentCategory(env.ctx, "", env.orgID), http.StatusBadRequest)
}

func TestTheMandatoryCategoryCannotBeChangedOrDeleted(t *testing.T) {
	env := newConsentEnv(t)
	require.NoError(t, env.svc.SeedDefaultConsentCategory(env.ctx, env.orgID))
	// Seeding again keeps the one category.
	require.NoError(t, env.svc.SeedDefaultConsentCategory(env.ctx, env.orgID))

	all, err := env.svc.GetAllConsentCategories(env.ctx, env.orgID)
	require.NoError(t, err)
	require.Len(t, all, 1)
	mandatory := all[0]
	assert.True(t, mandatory.IsMandatory)
	assert.Equal(t, constants.DefaultIdentityDataCategoryName, mandatory.CategoryName)

	update := category("Renamed", "profiling")
	update.CategoryIdentifier = mandatory.CategoryIdentifier
	clientError := requireClientStatus(t, env.svc.UpdateConsentCategory(env.ctx, env.orgID, update),
		http.StatusForbidden)
	assert.Equal(t, errors2.CONSENT_CAT_MANDATORY.Code, clientError.Code)
	_ = requireClientStatus(t, env.svc.DeleteConsentCategory(env.ctx, mandatory.CategoryIdentifier, env.orgID),
		http.StatusForbidden)

	mandatoryIDs, err := store.GetMandatoryConsentCategoryIds(env.ctx, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, []string{mandatory.CategoryIdentifier}, mandatoryIDs)
}

func TestConsentCategoryIDs(t *testing.T) {
	env := newConsentEnv(t)
	first := env.mustAdd(t, category("Analytics", "profiling"))
	second := env.mustAdd(t, category("Offers", "personalization"))

	ids, err := store.GetConsentCategoryIds(env.ctx, env.orgID)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{first.CategoryIdentifier, second.CategoryIdentifier}, ids)
	mandatoryIDs, err := store.GetMandatoryConsentCategoryIds(env.ctx, env.orgID)
	require.NoError(t, err)
	assert.Empty(t, mandatoryIDs)
}

func TestConsentWritesAreRefusedInASubOrganization(t *testing.T) {
	env := newConsentEnv(t)
	_, err := env.db.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('sub-org', 'sub', $1, 'ACTIVE')`, env.orgID)
	require.NoError(t, err)

	_, err = env.svc.AddConsentCategory(env.ctx, "sub-org", category("X", "profiling"))
	_ = requireClientStatus(t, err, http.StatusForbidden)
	update := category("X", "profiling")
	update.CategoryIdentifier = "any"
	_ = requireClientStatus(t, env.svc.UpdateConsentCategory(env.ctx, "sub-org", update), http.StatusForbidden)
	_ = requireClientStatus(t, env.svc.DeleteConsentCategory(env.ctx, "any", "sub-org"), http.StatusForbidden)
}
