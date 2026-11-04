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
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	adminConfigModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentService "github.com/wso2/identity-customer-data-service/internal/consent/service"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	profileSchemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/services"
	systemUtils "github.com/wso2/identity-customer-data-service/internal/system/utils"
	unificationModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	unificationService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
	"github.com/wso2/identity-customer-data-service/test/integration/utils"
)

const (
	orgScopeAdminUser     = "org-scope-admin"
	orgScopeAdminPassword = "org-scope-admin-password"
)

// orgScopeFixture holds two organizations. The resources live in ownerOrg, and each
// call under test is made by otherOrg with a resource ID of ownerOrg.
type orgScopeFixture struct {
	router     *http.ServeMux
	ownerOrg   string
	otherOrg   string
	ownerToken string
	otherToken string

	ownerUserId       string
	ownerProfileId    string
	ownerAnonId       string
	ownerCookie       string
	ownerCategoryId   string
	ownerRuleId       string
	otherProfileId    string
	otherCategoryId   string
	ownerInterests    string
	ownerAnonInterest string
}

// Test_OrgScope_API calls every by-ID endpoint of one org with the IDs of another org.
// Each call must behave as if the ID does not exist, and must leave the data of the
// other org unchanged. Each group has its own fixture, so a group fails only for its own calls.
func Test_OrgScope_API(t *testing.T) {
	ctx := context.Background()

	t.Run("UnificationRules", func(t *testing.T) {
		f := setUpOrgScopeFixture(t)
		rulePath := "/unification-rules/" + f.ownerRuleId

		t.Run("Get_other_org_rule_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet, rulePath, "", nil)
			assert.Equal(t, http.StatusNotFound, status, string(body))
		})

		t.Run("Patch_other_org_rule_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPatch, rulePath,
				`{"rule_name":"Renamed","priority":77,"is_active":false}`, nil)
			assert.Equal(t, http.StatusNotFound, status, string(body))
		})

		t.Run("Delete_other_org_rule_returns_no_content", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodDelete, rulePath, "", nil)
			assert.Equal(t, http.StatusNoContent, status, string(body))
		})

		t.Run("Delete_unknown_rule_returns_no_content", func(t *testing.T) {
			status, body := f.call(t, f.ownerOrg, f.ownerToken, http.MethodDelete,
				"/unification-rules/"+uuid.New().String(), "", nil)
			assert.Equal(t, http.StatusNoContent, status, string(body))
		})

		t.Run("Owner_rule_is_unchanged", func(t *testing.T) {
			status, body := f.call(t, f.ownerOrg, f.ownerToken, http.MethodGet, rulePath, "", nil)
			require.Equal(t, http.StatusOK, status, string(body))
			var rule unificationModel.UnificationRuleAPIResponse
			require.NoError(t, json.Unmarshal(body, &rule))
			assert.Equal(t, "Owner loyalty rule", rule.RuleName)
			assert.Equal(t, 1, rule.Priority)
			assert.True(t, rule.IsActive)
		})
	})

	t.Run("Profiles", func(t *testing.T) {
		f := setUpOrgScopeFixture(t)
		profilePath := "/profiles/" + f.ownerProfileId

		t.Run("Get_other_org_profile_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet, profilePath, "", nil)
			assert.Equal(t, http.StatusNotFound, status, string(body))
			assert.NotContains(t, string(body), f.ownerInterests)
		})

		t.Run("Patch_other_org_profile_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPatch, profilePath,
				`{"traits":{"interests":"changed by other org"}}`, nil)
			assert.Equal(t, http.StatusNotFound, status, string(body))
		})

		t.Run("Get_other_org_profile_consents_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet, profilePath+"/consents", "", nil)
			assert.Equal(t, http.StatusNotFound, status, string(body))
			assert.NotContains(t, string(body), f.ownerCategoryId)
		})

		t.Run("Put_other_org_profile_consents_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPut, profilePath+"/consents",
				fmt.Sprintf(`[{"category_identifier":%q,"is_consented":false}]`, f.ownerCategoryId), nil)
			assert.Equal(t, http.StatusNotFound, status, string(body))
		})

		t.Run("Delete_other_org_profile_returns_no_content", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodDelete, profilePath, "", nil)
			assert.Equal(t, http.StatusNoContent, status, string(body))
		})

		t.Run("Delete_unknown_profile_returns_no_content", func(t *testing.T) {
			status, body := f.call(t, f.ownerOrg, f.ownerToken, http.MethodDelete,
				"/profiles/"+uuid.New().String(), "", nil)
			assert.Equal(t, http.StatusNoContent, status, string(body))
		})

		t.Run("Owner_profiles_are_unchanged", func(t *testing.T) {
			f.assertOwnerDataUnchanged(t)
		})
	})

	t.Run("CurrentUserProfile", func(t *testing.T) {
		f := setUpOrgScopeFixture(t)
		ownerCookie := &http.Cookie{Name: constants.ProfileCookie, Value: f.ownerCookie}
		unknownCookie := &http.Cookie{Name: constants.ProfileCookie, Value: uuid.New().String()}

		t.Run("Get_with_other_org_cookie_returns_not_found", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet, "/profiles/Me", "", ownerCookie)
			assert.Equal(t, http.StatusNotFound, status, string(body))
			assert.NotContains(t, string(body), f.ownerAnonInterest)
			assert.NotContains(t, string(body), f.ownerAnonId)
		})

		t.Run("Patch_with_other_org_cookie_matches_unknown_cookie", func(t *testing.T) {
			unknownStatus, unknownBody := f.call(t, f.otherOrg, f.otherToken, http.MethodPatch, "/profiles/Me",
				`{"traits":{"interests":"changed by other org"}}`, unknownCookie)
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPatch, "/profiles/Me",
				`{"traits":{"interests":"changed by other org"}}`, ownerCookie)
			assert.NotEqual(t, http.StatusOK, status, string(body))
			assert.Equal(t, unknownStatus, status, string(body))
			assert.Equal(t, string(unknownBody), string(body))
		})

		t.Run("Init_with_other_org_cookie_creates_a_new_profile", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPost, "/profiles", `{}`, ownerCookie)
			assert.Equal(t, http.StatusCreated, status, string(body))
			assert.NotContains(t, string(body), f.ownerAnonId)
			assert.NotContains(t, string(body), f.ownerAnonInterest)
			assert.NotContains(t, string(body), f.ownerCookie)
		})

		t.Run("Owner_profiles_are_unchanged", func(t *testing.T) {
			f.assertOwnerDataUnchanged(t)
		})
	})

	t.Run("ConsentCategoryIds", func(t *testing.T) {
		f := setUpOrgScopeFixture(t)
		consentsPath := "/profiles/" + f.otherProfileId + "/consents"

		t.Run("Put_other_org_category_returns_bad_request", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPut, consentsPath,
				fmt.Sprintf(`[{"category_identifier":%q,"is_consented":true}]`, f.ownerCategoryId), nil)
			assert.Equal(t, http.StatusBadRequest, status, string(body))
		})

		t.Run("Put_unknown_category_returns_bad_request", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodPut, consentsPath,
				fmt.Sprintf(`[{"category_identifier":%q,"is_consented":true}]`, uuid.New().String()), nil)
			assert.Equal(t, http.StatusBadRequest, status, string(body))
		})

		t.Run("Rejected_categories_are_not_stored", func(t *testing.T) {
			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet, consentsPath, "", nil)
			require.Equal(t, http.StatusOK, status, string(body))
			assert.NotContains(t, string(body), f.ownerCategoryId)
		})

		t.Run("Filter_uses_own_org_category", func(t *testing.T) {
			f.setOtherProfileConsent(t, f.otherCategoryId)

			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet,
				"/profiles/"+f.otherProfileId+"?consentCategoryId="+f.otherCategoryId, "", nil)
			require.Equal(t, http.StatusOK, status, string(body))
			assert.Contains(t, string(body), "other-org-interest")
		})

		t.Run("Filter_ignores_other_org_category", func(t *testing.T) {
			// The profile holds a consent record for the category of the owner org,
			// written directly to the store as a record that predates the API check.
			f.setOtherProfileConsent(t, f.ownerCategoryId)

			status, body := f.call(t, f.otherOrg, f.otherToken, http.MethodGet,
				"/profiles/"+f.otherProfileId+"?consentCategoryId="+f.ownerCategoryId, "", nil)
			require.Equal(t, http.StatusOK, status, string(body))
			assert.NotContains(t, string(body), "other-org-interest")
		})
	})

	t.Run("ProfileSync", func(t *testing.T) {
		f := setUpOrgScopeFixture(t)

		t.Run("Delete_user_event_does_not_reach_other_org_profile", func(t *testing.T) {
			f.sync(t, map[string]interface{}{
				"event":     constants.DeleteUserEvent,
				"userId":    f.ownerUserId,
				"orgHandle": f.otherOrg,
			})
			stored, err := profileStore.GetProfile(ctx, f.ownerProfileId, f.ownerOrg)
			require.NoError(t, err)
			assert.NotNil(t, stored, "owner profile must still exist")
		})

		t.Run("Authentication_event_does_not_link_other_org_cookie", func(t *testing.T) {
			f.sync(t, map[string]interface{}{
				"event":         constants.AuthenticationSuccess,
				"userId":        "other-org-user-" + uuid.New().String(),
				"profileCookie": f.ownerCookie,
				"orgHandle":     f.otherOrg,
			})
			anon, err := profileStore.GetProfile(ctx, f.ownerAnonId, f.ownerOrg)
			require.NoError(t, err)
			require.NotNil(t, anon)
			assert.Empty(t, anon.UserId, "owner anonymous profile must not be linked to a user of another org")
		})
	})
}

func setUpOrgScopeFixture(t *testing.T) *orgScopeFixture {
	ctx := context.Background()

	t.Helper()
	suffix := time.Now().UnixNano()
	f := &orgScopeFixture{
		ownerOrg:          fmt.Sprintf("scope-owner-org-%d", suffix),
		otherOrg:          fmt.Sprintf("scope-other-org-%d", suffix),
		ownerUserId:       fmt.Sprintf("owner-user-%d", suffix),
		ownerInterests:    fmt.Sprintf("owner-interest-%d", suffix),
		ownerAnonInterest: fmt.Sprintf("owner-anon-interest-%d", suffix),
	}

	scopes := orgScopeAllScopes(t)
	restore := setUpOrgScopeAuth(t)
	t.Cleanup(restore)
	f.ownerToken = orgScopeToken(t, f.ownerOrg, "owner-subject-"+uuid.New().String(), scopes)
	f.otherToken = orgScopeToken(t, f.otherOrg, "other-subject-"+uuid.New().String(), scopes)

	f.router = http.NewServeMux()
	profileMux := http.NewServeMux()
	profileSvc := services.NewProfileService(profileMux)
	ruleMux := http.NewServeMux()
	ruleSvc := services.NewUnificationRulesService(ruleMux)
	systemUtils.MountTenantDispatcher(f.router, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/unification-rules") {
			ruleSvc.Route(w, r)
			return
		}
		profileSvc.Route(w, r)
	})

	schemaSvc := schemaService.GetProfileSchemaService()
	consentSvc := consentService.GetConsentCategoryService()
	profiles := profileService.GetProfilesService()

	for _, org := range []string{f.ownerOrg, f.otherOrg} {
		require.NoError(t, adminConfigStore.UpdateAdminConfig(ctx, adminConfigModel.AdminConfig{
			OrgHandle:  org,
			CDSEnabled: true,
		}, org))
		_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx, []profileSchemaModel.ProfileSchemaAttribute{{
			OrgId: org, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.email",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
		}}, constants.IdentityAttributes, org)
		require.NoError(t, err)
		_, err = schemaSvc.AddProfileSchemaAttributesForScope(ctx, []profileSchemaModel.ProfileSchemaAttribute{
			{
				OrgId: org, AttributeId: uuid.New().String(), AttributeName: "traits.interests",
				ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
			},
			{
				OrgId: org, AttributeId: uuid.New().String(), AttributeName: "traits.loyalty_id",
				ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
			},
		}, constants.Traits, org)
		require.NoError(t, err)
		require.NoError(t, consentSvc.SeedDefaultConsentCategory(ctx, org))
	}

	ownerCategory, err := consentSvc.AddConsentCategory(ctx, consentModel.ConsentCategory{
		CategoryName: "Owner Marketing",
		OrgHandle:    f.ownerOrg,
		Purpose:      "personalization",
		Attributes:   []consentModel.ConsentAttribute{{AttributeName: "traits.interests"}},
	})
	require.NoError(t, err)
	f.ownerCategoryId = ownerCategory.CategoryIdentifier

	otherCategory, err := consentSvc.AddConsentCategory(ctx, consentModel.ConsentCategory{
		CategoryName: "Other Marketing",
		OrgHandle:    f.otherOrg,
		Purpose:      "personalization",
		Attributes:   []consentModel.ConsentAttribute{{AttributeName: "traits.interests"}},
	})
	require.NoError(t, err)
	f.otherCategoryId = otherCategory.CategoryIdentifier

	ownerProfile, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
		UserId:             f.ownerUserId,
		IdentityAttributes: map[string]interface{}{"email": "owner@example.com"},
		Traits:             map[string]interface{}{"interests": f.ownerInterests},
	}, f.ownerOrg)
	require.NoError(t, err)
	f.ownerProfileId = ownerProfile.ProfileId
	require.NoError(t, profiles.UpdateProfileConsents(ctx, f.ownerProfileId, f.ownerOrg,
		[]profileModel.ConsentRecord{{CategoryIdentifier: f.ownerCategoryId, IsConsented: true}}))

	ownerAnon, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
		Traits: map[string]interface{}{"interests": f.ownerAnonInterest},
	}, f.ownerOrg)
	require.NoError(t, err)
	f.ownerAnonId = ownerAnon.ProfileId
	cookie, err := profiles.CreateProfileCookie(ctx, f.ownerAnonId)
	require.NoError(t, err)
	f.ownerCookie = cookie.CookieId

	otherProfile, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
		Traits: map[string]interface{}{"interests": "other-org-interest"},
	}, f.otherOrg)
	require.NoError(t, err)
	f.otherProfileId = otherProfile.ProfileId
	require.NoError(t, profiles.UpdateProfileConsents(ctx, f.otherProfileId, f.otherOrg,
		[]profileModel.ConsentRecord{{CategoryIdentifier: f.otherCategoryId, IsConsented: true}}))

	now := time.Now().UTC()
	f.ownerRuleId = uuid.New().String()
	require.NoError(t, unificationService.GetUnificationRuleService().AddUnificationRule(ctx,
		unificationModel.UnificationRule{
			RuleId:       f.ownerRuleId,
			OrgHandle:    f.ownerOrg,
			RuleName:     "Owner loyalty rule",
			PropertyName: "traits.loyalty_id",
			Priority:     1,
			IsActive:     true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}, f.ownerOrg))

	return f
}

// assertOwnerDataUnchanged reads the data of the owner org with the owner token.
func (f *orgScopeFixture) assertOwnerDataUnchanged(t *testing.T) {
	ctx := context.Background()

	t.Helper()

	status, body := f.call(t, f.ownerOrg, f.ownerToken, http.MethodGet, "/profiles/"+f.ownerProfileId, "", nil)
	require.Equal(t, http.StatusOK, status, "owner profile must still exist: %s", string(body))
	var profile profileModel.ProfileResponse
	require.NoError(t, json.Unmarshal(body, &profile))
	assert.Equal(t, f.ownerUserId, profile.UserId)

	stored, err := profileStore.GetProfile(ctx, f.ownerProfileId, f.ownerOrg)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, f.ownerInterests, stored.Traits["interests"])

	anon, err := profileStore.GetProfile(ctx, f.ownerAnonId, f.ownerOrg)
	require.NoError(t, err)
	require.NotNil(t, anon, "owner anonymous profile must still exist")
	assert.Empty(t, anon.UserId)
	assert.Equal(t, f.ownerAnonInterest, anon.Traits["interests"])

	consents, err := profileStore.GetProfileConsents(ctx, f.ownerProfileId, f.ownerOrg)
	require.NoError(t, err)
	require.Len(t, consents, 1)
	assert.Equal(t, f.ownerCategoryId, consents[0].CategoryIdentifier)
	assert.True(t, consents[0].IsConsented)
}

// setOtherProfileConsent stores one consent record on the profile of the other org.
func (f *orgScopeFixture) setOtherProfileConsent(t *testing.T, categoryId string) {
	ctx := context.Background()

	t.Helper()

	require.NoError(t, profileStore.UpdateProfileConsents(ctx, f.otherProfileId,
		[]profileModel.ConsentRecord{{CategoryIdentifier: categoryId, IsConsented: true,
			ConsentedAt: time.Now().UTC()}}))
}

func (f *orgScopeFixture) call(t *testing.T, org, token, method, path, body string,
	cookie *http.Cookie) (int, []byte) {

	t.Helper()

	target := fmt.Sprintf("/t/%s%s/v1%s", org, constants.ApiBasePath, path)
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

// sync sends a profile sync event with the admin credentials. The org is taken from the body.
func (f *orgScopeFixture) sync(t *testing.T, event map[string]interface{}) {

	t.Helper()

	payload, err := json.Marshal(event)
	require.NoError(t, err)
	target := fmt.Sprintf("/t/%s%s/v1/profiles/sync", f.otherOrg, constants.ApiBasePath)
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(payload)))
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte(orgScopeAdminUser+":"+orgScopeAdminPassword)))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	require.NotEqual(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
}

// orgScopeRequiredScopes reads the required scopes from the shipped deployment configuration.
func orgScopeRequiredScopes(t *testing.T) map[string][]string {

	t.Helper()

	raw, err := os.ReadFile(filepath.Join(utils.GetTestHome(), "config", "repository", "conf", "deployment.yaml"))
	require.NoError(t, err)
	var deployment struct {
		AuthServer struct {
			RequiredScopes map[string][]string `yaml:"required_scopes"`
		} `yaml:"auth_server"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &deployment))
	require.NotEmpty(t, deployment.AuthServer.RequiredScopes)
	return deployment.AuthServer.RequiredScopes
}

// orgScopeAllScopes returns every scope that the shipped configuration requires.
func orgScopeAllScopes(t *testing.T) string {

	t.Helper()

	seen := map[string]bool{}
	for _, scopes := range orgScopeRequiredScopes(t) {
		for _, scope := range scopes {
			seen[scope] = true
		}
	}
	all := make([]string, 0, len(seen))
	for scope := range seen {
		all = append(all, scope)
	}
	sort.Strings(all)
	return strings.Join(all, " ")
}

// orgScopeToken returns a JWT for the org. The service reads JWT claims without a
// signature check and asks the introspection endpoint whether the token is active.
func orgScopeToken(t *testing.T, org, subject, scopes string) string {

	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		constants.OrgHandleClaim: org,
		"sub":                    subject,
		"aud":                    "iam-cds",
		"exp":                    time.Now().Add(time.Hour).Unix(),
		"scope":                  scopes,
	})
	signed, err := token.SignedString([]byte("org-scope-test-key"))
	require.NoError(t, err)
	return signed
}

// setUpOrgScopeAuth points the runtime at a stub introspection endpoint that reports
// every token as active, and sets the admin credentials for profile sync.
func setUpOrgScopeAuth(t *testing.T) func() {

	t.Helper()

	introspection := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			constants.ActiveClaim:   true,
			constants.ClientIdClaim: constants.CONSOLE_APP,
		})
	}))

	certDir := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: introspection.Certificate().Raw})
	require.NoError(t, os.WriteFile(filepath.Join(certDir, "trust.pem"), certPEM, 0o600))

	endpoint, err := url.Parse(introspection.URL)
	require.NoError(t, err)

	previous := config.GetCDSRuntime().Config
	updated := previous
	updated.AuthServer = config.AuthServerConfig{
		Host:                  endpoint.Hostname(),
		Port:                  endpoint.Port(),
		IntrospectionEndPoint: "/oauth2/introspect",
		RequiredScopes:        orgScopeRequiredScopes(t),
		AdminUsername:         orgScopeAdminUser,
		AdminPassword:         orgScopeAdminPassword,
	}
	updated.TLS = config.TLSConfig{CertDir: certDir, TrustStore: "trust.pem"}
	config.OverrideCDSRuntime(updated)

	return func() {
		introspection.Close()
		config.OverrideCDSRuntime(previous)
	}
}
