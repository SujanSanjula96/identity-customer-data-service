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
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	adminConfigModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileSchema "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/services"
	systemUtils "github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/test/integration/utils"
)

const linkTestScope = "internal_cds_profile_link"

func Test_ProfileLink(t *testing.T) {

	org := fmt.Sprintf("carbon.super-link-%d", time.Now().UnixNano())
	profileSvc := profileService.GetProfilesService()
	profileSchemaSvc := schemaService.GetProfileSchemaService()

	restoreConfig := setUpLinkAuth(t)
	defer restoreConfig()

	router := newLinkRouter()

	t.Run("PreRequisite_EnableCDSAndAddSchema", func(t *testing.T) {
		err := adminConfigStore.UpdateAdminConfig(context.Background(), adminConfigModel.AdminConfig{
			OrgHandle:  org,
			CDSEnabled: true,
		}, org)
		require.NoError(t, err)

		identityAttributes := []profileSchema.ProfileSchemaAttribute{
			{
				OrgId:         org,
				AttributeId:   uuid.New().String(),
				AttributeName: "identity_attributes.email",
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				MultiValued:   true,
			},
		}
		_, err = profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttributes,
			constants.IdentityAttributes, org)
		require.NoError(t, err, "Failed to add identity schema attributes")
	})

	t.Run("Link_Anonymous_Profile_Sets_UserId", func(t *testing.T) {
		anonymous, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{
			IdentityAttributes: map[string]interface{}{"email": []interface{}{"anon-1@wso2.com"}},
		}, org)
		require.NoError(t, err)
		require.Empty(t, anonymous.UserId)

		userId := "user-" + uuid.New().String()
		status, body := doLink(t, router, org, linkBody(anonymous.ProfileId, userId))
		require.Equal(t, http.StatusOK, status, string(body))

		var linkResponse profileModel.ProfileLinkResponse
		require.NoError(t, json.Unmarshal(body, &linkResponse))
		require.Equal(t, anonymous.ProfileId, linkResponse.ProfileId)
		require.Equal(t, userId, linkResponse.UserId)

		linked, err := profileSvc.GetProfile(context.Background(), anonymous.ProfileId)
		require.NoError(t, err)
		require.Equal(t, userId, linked.UserId)
		// Nothing to unify against, so the profile stays a master of its own.
		require.Nil(t, linked.MergedTo)
	})

	t.Run("Link_With_Missing_Identifier_Is_Rejected", func(t *testing.T) {
		anonymous, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{
			IdentityAttributes: map[string]interface{}{"email": []interface{}{"anon-2@wso2.com"}},
		}, org)
		require.NoError(t, err)

		for name, payload := range map[string]string{
			"MissingUserId":    fmt.Sprintf(`{"profile_id":%q}`, anonymous.ProfileId),
			"EmptyUserId":      fmt.Sprintf(`{"profile_id":%q,"user_id":""}`, anonymous.ProfileId),
			"BlankUserId":      fmt.Sprintf(`{"profile_id":%q,"user_id":"   "}`, anonymous.ProfileId),
			"MissingProfileId": `{"user_id":"user-does-not-matter"}`,
			"BlankProfileId":   `{"profile_id":"   ","user_id":"user-does-not-matter"}`,
			"Garbage":          `not-json`,
		} {
			t.Run(name, func(t *testing.T) {
				status, body := doLink(t, router, org, payload)
				require.Equal(t, http.StatusBadRequest, status, string(body))

				unchanged, err := profileSvc.GetProfile(context.Background(), anonymous.ProfileId)
				require.NoError(t, err)
				require.Empty(t, unchanged.UserId)
			})
		}
	})

	t.Run("Link_Unknown_Profile_Is_Not_Found", func(t *testing.T) {
		status, body := doLink(t, router, org,
			linkBody(uuid.New().String(), "user-does-not-matter"))
		require.Equal(t, http.StatusNotFound, status, string(body))
	})

	t.Run("Relink_To_Different_User_Is_Rejected", func(t *testing.T) {
		anonymous, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{
			IdentityAttributes: map[string]interface{}{"email": []interface{}{"anon-4@wso2.com"}},
		}, org)
		require.NoError(t, err)

		firstUser := "user-" + uuid.New().String()
		status, body := doLink(t, router, org, linkBody(anonymous.ProfileId, firstUser))
		require.Equal(t, http.StatusOK, status, string(body))

		secondUser := "user-" + uuid.New().String()
		status, body = doLink(t, router, org, linkBody(anonymous.ProfileId, secondUser))
		require.Equal(t, http.StatusConflict, status, string(body))

		unchanged, err := profileSvc.GetProfile(context.Background(), anonymous.ProfileId)
		require.NoError(t, err)
		require.Equal(t, firstUser, unchanged.UserId)
	})

	t.Run("Relink_To_Same_User_Is_Accepted", func(t *testing.T) {
		anonymous, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{
			IdentityAttributes: map[string]interface{}{"email": []interface{}{"anon-5@wso2.com"}},
		}, org)
		require.NoError(t, err)

		userId := "user-" + uuid.New().String()
		payload := linkBody(anonymous.ProfileId, userId)

		status, body := doLink(t, router, org, payload)
		require.Equal(t, http.StatusOK, status, string(body))

		// A retry after a lost response must not fail.
		status, body = doLink(t, router, org, payload)
		require.Equal(t, http.StatusOK, status, string(body))

		var linkResponse profileModel.ProfileLinkResponse
		require.NoError(t, json.Unmarshal(body, &linkResponse))
		require.Equal(t, anonymous.ProfileId, linkResponse.ProfileId)
		require.Equal(t, userId, linkResponse.UserId)

		linked, err := profileSvc.GetProfile(context.Background(), anonymous.ProfileId)
		require.NoError(t, err)
		require.Equal(t, userId, linked.UserId)
	})

	t.Run("Link_UserId_With_Existing_Profile_Unifies", func(t *testing.T) {
		userId := "user-" + uuid.New().String()

		existing, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{
			UserId:             userId,
			IdentityAttributes: map[string]interface{}{"email": []interface{}{"known@wso2.com"}},
		}, org)
		require.NoError(t, err)
		time.Sleep(2 * time.Second)

		anonymous, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{
			IdentityAttributes: map[string]interface{}{"email": []interface{}{"anon-3@wso2.com"}},
		}, org)
		require.NoError(t, err)

		status, body := doLink(t, router, org, linkBody(anonymous.ProfileId, userId))
		require.Equal(t, http.StatusOK, status, string(body))

		time.Sleep(2 * time.Second)

		// The pre-existing permanent profile stays the master and the linked profile
		// becomes its child, matched on the userId the link supplied.
		merged, err := profileSvc.GetProfile(context.Background(), anonymous.ProfileId)
		require.NoError(t, err)
		require.NotNil(t, merged.MergedTo)
		require.Equal(t, existing.ProfileId, merged.MergedTo.ProfileId)
		require.Equal(t, constants.SystemUserIdMatchReason, merged.MergedTo.Reason)
		require.Equal(t, userId, merged.UserId)

		master, err := profileSvc.GetProfile(context.Background(), existing.ProfileId)
		require.NoError(t, err)
		require.Equal(t, userId, master.UserId)
		require.Contains(t, master.IdentityAttributes["email"].([]interface{}), "anon-3@wso2.com")
	})
}

// newLinkRouter mounts the profile service behind the tenant dispatcher so tests
// exercise the registered route and the full /t/{org}/cds/api/v1/... path.
func newLinkRouter() *http.ServeMux {

	root := http.NewServeMux()
	profileMux := http.NewServeMux()
	profileSvc := services.NewProfileService(profileMux)
	systemUtils.MountTenantDispatcher(root, profileSvc.Route)
	return root
}

func linkBody(profileId, userId string) string {

	return fmt.Sprintf(`{"profile_id":%q,"user_id":%q}`, profileId, userId)
}

func doLink(t *testing.T, router *http.ServeMux, org, body string) (int, []byte) {

	t.Helper()

	target := fmt.Sprintf("/t/%s%s/v1/profiles/link", org, constants.ApiBasePath)
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+linkTestToken)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

const linkTestToken = "opaque-link-test-token"

// setUpLinkAuth points the runtime at a stub introspection endpoint that accepts
// linkTestToken with the profile:link scope, so AuthnAndAuthz runs unmodified.
// It returns a function that restores the previous runtime configuration.
func setUpLinkAuth(t *testing.T) func() {

	t.Helper()

	introspection := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		active := r.PostForm.Get("token") == linkTestToken
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			constants.ActiveClaim:   active,
			constants.ClientIdClaim: constants.CONSOLE_APP,
			"scope":                 linkTestScope,
		})
	}))

	certDir := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: introspection.Certificate().Raw,
	})
	require.NoError(t, os.WriteFile(filepath.Join(certDir, "trust.pem"), certPEM, 0o600))

	endpoint, err := url.Parse(introspection.URL)
	require.NoError(t, err)

	previous := config.GetCDSRuntime().Config
	updated := previous
	updated.AuthServer = config.AuthServerConfig{
		Host:                  endpoint.Hostname(),
		Port:                  endpoint.Port(),
		IntrospectionEndPoint: "/oauth2/introspect",
		RequiredScopes:        requiredScopesFromDeploymentConfig(t),
	}
	updated.TLS = config.TLSConfig{
		CertDir:    certDir,
		TrustStore: "trust.pem",
	}
	config.OverrideCDSRuntime(updated)

	return func() {
		introspection.Close()
		config.OverrideCDSRuntime(previous)
	}
}

// requiredScopesFromDeploymentConfig reads the shipped deployment configuration so
// the test fails if profile:link is dropped from it.
func requiredScopesFromDeploymentConfig(t *testing.T) map[string][]string {

	t.Helper()

	path := filepath.Join(utils.GetTestHome(), "config", "repository", "conf", "deployment.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var deployment struct {
		AuthServer struct {
			RequiredScopes map[string][]string `yaml:"required_scopes"`
		} `yaml:"auth_server"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &deployment))
	require.Contains(t, deployment.AuthServer.RequiredScopes, "profile:link",
		"profile:link is missing from config/repository/conf/deployment.yaml")

	require.Equal(t, []string{linkTestScope}, deployment.AuthServer.RequiredScopes["profile:link"])
	return deployment.AuthServer.RequiredScopes
}
