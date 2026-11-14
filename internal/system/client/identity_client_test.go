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
package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

const (
	tokenValue = "stub-access-token"
	clientID   = "cds-client"
	clientPass = "cds-secret"
)

// identityServer is a stub identity server. A test adds the routes it needs to routes.
type identityServer struct {
	server *httptest.Server
	mux    *http.ServeMux
	// tokenRequests holds the form of every token request, in order.
	tokenRequests []url.Values
	tokenPaths    []string
	tokenUsers    []string
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// newIdentityServer starts the stub with a token endpoint, and returns a client of it.
func newIdentityServer(t *testing.T, systemAppGrant bool) (*identityServer, *IdentityClient) {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	config.OverrideCDSRuntime(config.Config{AuthServer: config.AuthServerConfig{
		TokenEndpoint:             "/oauth2/token",
		IntrospectionEndPoint:     "/oauth2/introspect",
		ClientID:                  clientID,
		ClientSecret:              clientPass,
		IntrospectionClientId:     "introspect-client",
		IntrospectionClientSecret: "introspect-secret",
		IsSystemAppGrantEnabled:   systemAppGrant,
	}})

	is := &identityServer{mux: http.NewServeMux()}
	is.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/token") {
			require.NoError(t, r.ParseForm())
			user, _, _ := r.BasicAuth()
			is.tokenRequests = append(is.tokenRequests, r.PostForm)
			is.tokenPaths = append(is.tokenPaths, r.URL.Path)
			is.tokenUsers = append(is.tokenUsers, user)
			writeJSON(w, http.StatusOK, map[string]string{"access_token": tokenValue})
			return
		}
		http.NotFound(w, r)
	})
	is.server = httptest.NewTLSServer(is.mux)
	t.Cleanup(is.server.Close)
	return is, &IdentityClient{BaseURL: strings.TrimPrefix(is.server.URL, "https://"), HTTPClient: is.server.Client()}
}

func (is *identityServer) handle(pattern string, handler http.HandlerFunc) {
	is.mux.HandleFunc(pattern, handler)
}

// requireToken fails the request unless it carries the token the stub issued.
func requireToken(t *testing.T, r *http.Request, systemAppGrant bool) {
	t.Helper()
	want := "Bearer " + tokenValue
	if systemAppGrant {
		want = constants.SystemAppHeader + " " + tokenValue
	}
	require.Equal(t, want, r.Header.Get("Authorization"))
}

func requireServerError(t *testing.T, err error) {
	t.Helper()
	var serverError *errors2.ServerError
	require.ErrorAs(t, err, &serverError)
}

func TestFetchTokenUsesClientCredentialsPerOrganization(t *testing.T) {
	is, c := newIdentityServer(t, false)

	token, err := c.FetchToken("acme")

	require.NoError(t, err)
	assert.Equal(t, tokenValue, token)
	require.Len(t, is.tokenRequests, 1)
	assert.Equal(t, "/t/acme/oauth2/token", is.tokenPaths[0])
	assert.Equal(t, clientID, is.tokenUsers[0])
	assert.Equal(t, "client_credentials", is.tokenRequests[0].Get("grant_type"))
	assert.Contains(t, is.tokenRequests[0].Get("scope"), "internal_application_mgt_view")
}

func TestFetchTokenUsesTheSystemAppGrantWhenEnabled(t *testing.T) {
	is, c := newIdentityServer(t, true)

	token, err := c.FetchToken("acme")

	require.NoError(t, err)
	assert.Equal(t, tokenValue, token)
	require.Len(t, is.tokenRequests, 1)
	assert.Equal(t, "/t/carbon.super/oauth2/token", is.tokenPaths[0], "the grant is requested from the super tenant")
	assert.Equal(t, "system_app_grant", is.tokenRequests[0].Get("grant_type"))
	assert.Equal(t, "acme", is.tokenRequests[0].Get("organizationHandle"))
}

func TestFetchTokenForASubOrganizationUsesTheSuperTenantPath(t *testing.T) {
	is, c := newIdentityServer(t, false)
	subOrg := uuid.New().String()

	_, err := c.FetchToken(subOrg)

	require.NoError(t, err)
	assert.Equal(t, "/t/carbon.super/o/"+subOrg+"/oauth2/token", is.tokenPaths[0])
}

func TestFetchTokenFailures(t *testing.T) {
	t.Run("an organization handle that is not a path segment", func(t *testing.T) {
		_, c := newIdentityServer(t, false)

		_, err := c.FetchToken("../etc")

		assert.Error(t, err)
	})
	t.Run("a status other than 200", func(t *testing.T) {
		is, c := newIdentityServer(t, false)
		is.handle("/t/denied/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		})

		_, err := c.FetchToken("denied")

		requireServerError(t, err)
		assert.Contains(t, err.Error(), errors2.TOKEN_FETCH_FAILED.Code)
	})
	t.Run("a body that is not JSON", func(t *testing.T) {
		is, c := newIdentityServer(t, false)
		is.handle("/t/garbled/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
			_,
				_ = w.Write([]byte("nope"))
		})

		_, err := c.FetchToken("garbled")

		requireServerError(t, err)
	})
	t.Run("a response without a token", func(t *testing.T) {
		is, c := newIdentityServer(t, false)
		is.handle("/t/empty/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{})
		})

		_, err := c.FetchToken("empty")

		requireServerError(t, err)
	})
	t.Run("a server that cannot be reached", func(t *testing.T) {
		is, c := newIdentityServer(t, false)
		is.server.Close()

		_, err := c.FetchToken("acme")

		requireServerError(t, err)
	})
}

func TestFetchApplicationIdentifier(t *testing.T) {
	for _, systemAppGrant := range []bool{false, true} {
		t.Run(fmt.Sprintf("system app grant %v", systemAppGrant), func(t *testing.T) {
			is, c := newIdentityServer(t, systemAppGrant)
			var filter string
			is.handle("/t/acme/api/server/v1/applications", func(w http.ResponseWriter, r *http.Request) {
				requireToken(t, r, systemAppGrant)
				filter = r.URL.Query().Get("filter")
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"totalResults": 1,
					"applications": []map[string]string{{"id": "app-1", "name": "Shop", "clientId": "client-1"}},
				})
			})

			got, err := c.FetchApplicationIdentifier("client-1", "acme")

			require.NoError(t, err)
			assert.Equal(t, "clientId eq client-1 or issuer eq client-1", filter)
			require.Len(t, got.Applications, 1)
			assert.Equal(t, "app-1", got.Applications[0].ID)
		})
	}
}

func TestFetchApplicationIdentifierFailures(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/broken/api/server/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	is.handle("/t/garbled/api/server/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	_, err := c.FetchApplicationIdentifier("x", "broken")
	requireServerError(t, err)
	_, err = c.FetchApplicationIdentifier("x", "garbled")
	requireServerError(t, err)
	_, err = c.FetchApplicationIdentifier("x", "bad/handle")
	assert.Error(t, err)
}

func TestGetApplication(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/applications/app-1", func(w http.ResponseWriter, r *http.Request) {
		requireToken(t, r, false)
		writeJSON(w, http.StatusOK, map[string]string{"id": "app-1", "name": "Shop", "clientId": "client-1"})
	})
	is.handle("/t/acme/api/server/v1/applications/broken", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	is.handle("/t/acme/api/server/v1/applications/garbled", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	app, found, err := c.GetApplication("app-1", "acme")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "client-1", app.ClientId)

	_, found, err = c.GetApplication("missing", "acme")
	require.NoError(t, err, "an application that does not exist is not an error")
	assert.False(t, found)

	_, _, err = c.GetApplication("broken", "acme")
	requireServerError(t, err)
	_, _, err = c.GetApplication("garbled", "acme")
	requireServerError(t, err)
	_, _, err = c.GetApplication("app-1", "bad/handle")
	assert.Error(t, err)
}

func TestIntrospectToken(t *testing.T) {
	is, c := newIdentityServer(t, false)
	var gotUser, gotToken string
	is.handle("/t/carbon.super/oauth2/introspect", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotUser, _, _ = r.BasicAuth()
		gotToken = r.PostForm.Get("token")
		writeJSON(w, http.StatusOK, map[string]interface{}{"active": true, "client_id": "console"})
	})

	claims, err := c.IntrospectToken("abc", "acme")

	require.NoError(t, err)
	assert.Equal(t, true, claims["active"])
	assert.Equal(t, "introspect-client", gotUser)
	assert.Equal(t, "abc", gotToken)
}

func TestIntrospectTokenFailures(t *testing.T) {
	is, c := newIdentityServer(t, false)
	status := http.StatusBadGateway
	body := "nope"
	is.handle("/t/carbon.super/oauth2/introspect", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})

	_, err := c.IntrospectToken("abc", "acme")
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	assert.Equal(t, http.StatusBadGateway, clientError.StatusCode)

	status = http.StatusOK
	_, err = c.IntrospectToken("abc", "acme")
	assert.Error(t, err, "a body that is not JSON")
}

func TestGetProfileSchemaFromTheClaimDialects(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/claim-dialects", func(w http.ResponseWriter, r *http.Request) {
		requireToken(t, r, false)
		writeJSON(w, http.StatusOK, []map[string]string{
			{"id": "d-scim", "dialectURI": "urn:scim:wso2:schema"},
			{"id": "d-core", "dialectURI": "urn:scim:schemas:core:1.0"},
			{"id": "d-oidc", "dialectURI": "http://wso2.org/oidc/claim"},
			{"id": "d-broken", "dialectURI": "urn:ietf:params:scim:schemas:core:2.0:User"},
		})
	})
	is.handle("/t/acme/api/server/v1/claim-dialects/local/claims", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]interface{}{
			{"claimURI": "http://wso2.org/claims/givenname", "dataType": "string", "displayName": "First Name"},
			{"claimURI": "http://wso2.org/claims/age", "dataType": "integer", "readOnly": true},
			{"claimURI": "http://wso2.org/claims/addresses.work", "dataType": "string"},
			{"claimURI": "http://wso2.org/claims/emailaddress", "dataType": "string"},
		})
	})
	is.handle("/t/acme/api/server/v1/claim-dialects/d-scim/claims", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]string{
			{"claimURI": "urn:scim:wso2:schema:givenName", "mappedLocalClaimURI": "http://wso2.org/claims/givenname"},
			{"claimURI": "urn:scim:wso2:schema:age", "mappedLocalClaimURI": "http://wso2.org/claims/age"},
			{"claimURI": "urn:scim:wso2:schema:addresses.work",
				"mappedLocalClaimURI": "http://wso2.org/claims/addresses.work"},
			{"claimURI": "urn:scim:wso2:schema:unmapped", "mappedLocalClaimURI": "http://wso2.org/claims/unknown"},
		})
	})
	is.handle("/t/acme/api/server/v1/claim-dialects/d-broken/claims", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	attributes, err := c.GetProfileSchema("acme")

	require.NoError(t, err)
	byName := map[string]string{}
	for _, attribute := range attributes {
		byName[attribute.AttributeName] = attribute.ValueType
		assert.Equal(t, "acme", attribute.OrgId)
	}
	assert.Equal(t, "string", byName["identity_attributes.givenname"])
	assert.Equal(t, "integer", byName["identity_attributes.age"])
	assert.Equal(t, "string", byName["identity_attributes.addresses.work"])
	assert.Equal(t, "complex", byName["identity_attributes.addresses"], "the parent of a sub-attribute is generated")
	assert.NotContains(t, byName, "identity_attributes.unknown", "a claim with no local claim is skipped")
	assert.Len(t, byName, 4)
}

func TestGetProfileSchemaFailures(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/nolocal/api/server/v1/claim-dialects/local/claims", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	is.handle("/t/nodialects/api/server/v1/claim-dialects/local/claims", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]string{})
	})
	is.handle("/t/nodialects/api/server/v1/claim-dialects", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})
	is.handle("/t/garbledlocal/api/server/v1/claim-dialects/local/claims", func(w http.ResponseWriter,
		r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	_, err := c.GetProfileSchema("nolocal")
	requireServerError(t, err)
	_, err = c.GetProfileSchema("nodialects")
	requireServerError(t, err)
	_, err = c.GetProfileSchema("garbledlocal")
	requireServerError(t, err)
	_, err = c.GetLocalClaimsMap("bad/handle")
	assert.Error(t, err)
	_, err = c.GetAllDialects("bad/handle")
	assert.Error(t, err)
	_, err = c.GetClaimsByDialect("d", "bad/handle")
	assert.Error(t, err)
}

func TestGetSCIMUserFlattensTheNestedClaims(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/scim2/Users/u-1", func(w http.ResponseWriter, r *http.Request) {
		requireToken(t, r, false)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":       "u-1",
			"userName": "jane",
			"name":     map[string]interface{}{"givenName": "Jane", "familyName": "Doe"},
		})
	})
	is.handle("/t/acme/scim2/Users/garbled", func(w http.ResponseWriter, r *http.Request) {
		_,
			_ = w.Write([]byte("nope"))
	})

	user, err := c.GetSCIMUser("acme", "u-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{
		"id": "u-1", "userName": "jane", "name.givenName": "Jane", "name.familyName": "Doe",
	}, user)

	_, err = c.GetSCIMUser("acme", "garbled")
	requireServerError(t, err)
	_, err = c.GetSCIMUser("bad/handle", "u-1")
	assert.Error(t, err)
}

func TestSubOrganizationCallsUseTheOrgPath(t *testing.T) {
	is, c := newIdentityServer(t, false)
	subOrg := uuid.New().String()
	var path string
	is.handle("/o/api/server/v1/claim-dialects/local/claims", func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeJSON(w, http.StatusOK, []map[string]string{{"claimURI": "http://wso2.org/claims/givenname"}})
	})

	claims, err := c.GetLocalClaimsMap(subOrg)

	require.NoError(t, err)
	assert.Contains(t, claims, "http://wso2.org/claims/givenname")
	assert.Equal(t, "/o/api/server/v1/claim-dialects/local/claims", path, "the organization is carried by the token")
	assert.Equal(t, "/o", c.orgAPIPathPrefix(subOrg))
	assert.Equal(t, "/t/acme", c.orgAPIPathPrefix("acme"))
}

func TestOrgEndpointRefusesURLsThatLeaveTheHost(t *testing.T) {
	good, err := orgEndpoint("is.example.com:9443", "acme", "/t/acme/api/x")
	require.NoError(t, err)
	assert.Equal(t, "https://is.example.com:9443/t/acme/api/x", good)

	for name, call := range map[string]func() (string, error){
		"a handle that is not a segment": func() (string, error) { return orgEndpoint("is.example.com", "a/b", "/x") },
		"an empty handle":                func() (string, error) { return orgEndpoint("is.example.com", "", "/x") },
		"a path that names another host": func() (string, error) {
			return orgEndpoint("is.example.com", "acme",
				"@evil.com/x")
		},
		"a path with a query": func() (string, error) {
			return orgEndpoint("is.example.com", "acme",
				"/x?y=1")
		},
		"a path with a fragment": func() (string, error) {
			return orgEndpoint("is.example.com", "acme",
				"/x#y")
		},
		"a path that climbs": func() (string, error) {
			return orgEndpoint("is.example.com", "acme",
				"/t/../x")
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := call()

			assert.Error(t, err)
		})
	}
}

func TestNewOutboundHTTPClient(t *testing.T) {
	dir := t.TempDir()

	client, err := newOutboundHTTPClient(config.TLSConfig{CertDir: dir}, "is.example.com")
	require.NoError(t, err)
	assert.NotNil(t, client.Transport)

	_, err = newOutboundHTTPClient(config.TLSConfig{CertDir: dir, TrustStore: "missing.pem"}, "is.example.com")
	assert.Error(t, err, "a trust store that cannot be read")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.pem"), []byte("not a certificate"), 0o600))
	_, err = newOutboundHTTPClient(config.TLSConfig{CertDir: dir, TrustStore: "bad.pem"}, "is.example.com")
	assert.Error(t, err, "a trust store without certificates")

	_, err = newOutboundHTTPClient(config.TLSConfig{CertDir: dir, MTLSEnabled: true, CDSPublicCert: "a.crt",
		CDSPrivateKey: "a.key"}, "is.example.com")
	assert.Error(t, err, "a client certificate that cannot be loaded")
}

func TestSharedOutboundHTTPClient(t *testing.T) {
	dir := t.TempDir()
	tlsCfg := config.TLSConfig{CertDir: dir}

	first, err := sharedOutboundHTTPClient(tlsCfg, "is.example.com")
	require.NoError(t, err)
	second, err := sharedOutboundHTTPClient(tlsCfg, "is.example.com")
	require.NoError(t, err)
	assert.Same(t, first, second, "the same TLS configuration shares one client and one connection pool")

	other, err := sharedOutboundHTTPClient(config.TLSConfig{CertDir: t.TempDir()}, "is.example.com")
	require.NoError(t, err)
	assert.NotSame(t, first, other, "a different TLS configuration gets its own client")

	badCfg := config.TLSConfig{CertDir: dir, TrustStore: "missing.pem"}
	_, err = sharedOutboundHTTPClient(badCfg, "is.example.com")
	assert.Error(t, err)
	_, err = sharedOutboundHTTPClient(badCfg, "is.example.com")
	assert.Error(t, err, "a failed build is not cached")
}

func TestConvertSCIMClaimWithLocal(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))

	t.Run("a plain claim", func(t *testing.T) {
		attr, sub, parent := ConvertSCIMClaimWithLocal(
			map[string]interface{}{"claimURI": "urn:x:givenName",
				"mappedLocalClaimURI": "http://wso2.org/claims/givenname"},
			map[string]interface{}{"dataType": "string", "displayName": "First Name"},
			nil, "acme", "urn:x")

		assert.Equal(t, "identity_attributes.givenname", attr.AttributeName)
		assert.Equal(t, "string", attr.ValueType)
		assert.Equal(t, "First Name", attr.DisplayName)
		assert.Equal(t, "readWrite", attr.Mutability)
		assert.Equal(t, "overwrite", attr.MergeStrategy)
		assert.Equal(t, "urn:x", attr.SCIMDialect)
		assert.Equal(t, "acme", attr.OrgId)
		assert.NotEmpty(t, attr.AttributeId)
		assert.Nil(t, sub)
		assert.Empty(t, parent)
	})
	t.Run("read only, multi valued, with canonical values", func(t *testing.T) {
		attr, _, _ := ConvertSCIMClaimWithLocal(
			map[string]interface{}{"claimURI": "urn:x:country",
				"mappedLocalClaimURI": "http://wso2.org/claims/country"},
			map[string]interface{}{
				"dataType": "string",
				"readOnly": true,
				"properties": []interface{}{
					map[string]interface{}{"key": "multiValued", "value": "true"},
					"not a property",
				},
				"canonicalValues": []interface{}{
					map[string]interface{}{"value": "LK", "label": "Sri Lanka"},
					"not a value",
				},
			}, nil, "acme", "urn:x")

		assert.Equal(t, "readOnly", attr.Mutability)
		assert.True(t, attr.MultiValued)
		require.Len(t, attr.CanonicalValues, 1)
		assert.Equal(t, "LK", attr.CanonicalValues[0].Value)
		assert.Equal(t, "Sri Lanka", attr.CanonicalValues[0].Label)
		assert.NotEmpty(t, attr.DisplayName, "a name is made from the claim when there is none")
	})
	t.Run("a claim with a dot is a sub-attribute of its parent", func(t *testing.T) {
		attr, sub, parent := ConvertSCIMClaimWithLocal(
			map[string]interface{}{"claimURI": "urn:x:addresses.work",
				"mappedLocalClaimURI": "http://wso2.org/claims/addresses.work"},
			map[string]interface{}{"dataType": "string"}, nil, "acme", "urn:x")

		assert.Equal(t, "identity_attributes.addresses.work", attr.AttributeName)
		require.NotNil(t, sub)
		assert.Equal(t, "identity_attributes.addresses.work", sub.AttributeName)
		assert.Equal(t, attr.AttributeId, sub.AttributeId)
		assert.Equal(t, "identity_attributes.addresses", parent)
	})
	t.Run("a claim with children is complex", func(t *testing.T) {
		all := []map[string]interface{}{
			{"claimURI": "urn:x:name", "mappedLocalClaimURI": "http://wso2.org/claims/name"},
			{"claimURI": "urn:x:name.given", "mappedLocalClaimURI": "http://wso2.org/claims/name.given"},
			{"claimURI": "urn:x:name.family", "mappedLocalClaimURI": "http://wso2.org/claims/name.family"},
			{"claimURI": "urn:x:other", "mappedLocalClaimURI": "http://wso2.org/claims/other"},
		}

		attr, sub, _ := ConvertSCIMClaimWithLocal(all[0], map[string]interface{}{"dataType": "string"}, all, "acme",
			"urn:x")

		assert.Equal(t, "complex", attr.ValueType)
		assert.Nil(t, sub)
		names := make([]string, 0, len(attr.SubAttributes))
		for _, child := range attr.SubAttributes {
			names = append(names, child.AttributeName)
		}
		assert.ElementsMatch(t, []string{"identity_attributes.name.given", "identity_attributes.name.family"}, names)
	})
}

func TestSmallHelpers(t *testing.T) {
	assert.Equal(t, "emailaddress", extractClaimKeyFromLocalURI("http://wso2.org/claims/emailaddress"))
	assert.Equal(t, "plain", extractClaimKeyFromLocalURI("plain"))
	assert.Equal(t, "a", ifThenElse(true, "a", "b"))
	assert.Equal(t, "b", ifThenElse(false, "a", "b"))
	assert.Equal(t, map[string]interface{}{"a.b": 1, "c": 2, "d": []int{1}},
		flattenSCIMClaims(map[string]interface{}{"a": map[string]interface{}{"b": 1}, "c": 2, "d": []int{1}}))
}
