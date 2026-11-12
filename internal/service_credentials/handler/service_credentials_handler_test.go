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

package handler_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/servicecreds"
	"github.com/wso2/identity-customer-data-service/internal/system/services"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

const (
	usableKey   = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	syncUser    = "sync"
	syncPass    = "sync-password"
	goodClient  = "acme-client"
	goodSecret  = "acme-secret"
	issuedToken = "issued-token"
	syncPath    = "/t/acme/cds/api/v1/service-credentials/sync"
	acmeOrgID   = "acme-org-id"
)

// fixture is a CDS with the service credentials sync endpoint mounted, the WSO2
// IS adapter, an Identity Server token endpoint that issues a token to
// goodClient only, and a database.
type fixture struct {
	t      *testing.T
	cds    http.Handler
	testDB *setup.SQLiteTestDB
}

func newFixture(t *testing.T, encryptionKey string) *fixture {
	t.Helper()
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}

	is := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if r.URL.Path == "/t/acme/oauth2/introspect" || r.URL.Path == "/t/carbon.super/oauth2/introspect" {
			// Each endpoint takes only the client of its own tenant.
			want := map[string][2]string{
				"/t/acme/oauth2/introspect":         {goodClient, goodSecret},
				"/t/carbon.super/oauth2/introspect": {"introspection-client", "introspection-secret"},
			}[r.URL.Path]
			if !ok || id != want[0] || secret != want[1] {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"active": true, "introspected_at": r.URL.Path})
			return
		}
		if r.URL.Path == "/t/acme/api/server/v1/organizations/self" {
			if r.Header.Get("Authorization") != "Bearer "+issuedToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": acmeOrgID, "name": "Acme", "orgHandle": "acme",
				"status": "ACTIVE"})
			return
		}
		if r.URL.Path != "/t/acme/oauth2/token" || !ok || id != goodClient || secret != goodSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": issuedToken})
	}))
	t.Cleanup(is.Close)

	certDir := t.TempDir()
	trust := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: is.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(certDir, "is.pem"), trust, 0o600); err != nil {
		t.Fatalf("failed to write the trust store: %v", err)
	}
	isURL, _ := url.Parse(is.URL)

	config.OverrideCDSRuntime(config.Config{
		TLS: config.TLSConfig{CertDir: certDir, TrustStore: "is.pem"},
		AuthServer: config.AuthServerConfig{
			Host:          isURL.Hostname(),
			Port:          isURL.Port(),
			TokenEndpoint: "/oauth2/token",
			// The configured introspection client, which belongs to the super tenant.
			IntrospectionEndPoint:     "/oauth2/introspect",
			IntrospectionClientId:     "introspection-client",
			IntrospectionClientSecret: "introspection-secret",
			// The configured pair, which the token endpoint does not accept.
			ClientID:      "configured-client",
			ClientSecret:  "configured-secret",
			AdminUsername: syncUser,
			AdminPassword: syncPass,
		},
		IdP: config.IdPConfig{
			ServiceCredentials: config.ServiceCredentialsConfig{EncryptionKey: encryptionKey},
		},
	})

	testDB, err := setup.SetupTestSQLite()
	if err != nil {
		t.Fatalf("failed to set up the test database: %v", err)
	}
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})

	idp.SetAdapter(nil)
	t.Cleanup(func() { idp.SetAdapter(nil) })

	routes := http.NewServeMux()
	_ = services.NewServiceCredentialsService(routes)
	cds := http.NewServeMux()
	utils.MountTenantDispatcher(cds, routes.ServeHTTP)

	return &fixture{t: t, cds: cds, testDB: testDB}
}

func (f *fixture) call(path, body string, authenticated bool) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if authenticated {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(syncUser+":"+syncPass)))
	}
	rec := httptest.NewRecorder()
	f.cds.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Code
}

func credentials(clientID, clientSecret string) string {
	return credentialsOf(acmeOrgID, clientID, clientSecret)
}

func credentialsOf(orgID, clientID, clientSecret string) string {
	body, _ := json.Marshal(map[string]string{
		"event":         "SERVICE_CREDENTIALS_UPDATED",
		"org_id":        orgID,
		"client_id":     clientID,
		"client_secret": clientSecret,
	})
	return string(body)
}

func (f *fixture) storedClientID() string {
	f.t.Helper()
	var clientID string
	err := f.testDB.DB.QueryRow(`SELECT c.client_id FROM org_service_credentials c
		JOIN organizations o ON o.org_id = c.org_id WHERE o.org_handle = 'acme'`).Scan(&clientID)
	if err != nil {
		return ""
	}
	return clientID
}

func TestStoresVerifiedCredentialsAndUsesThemForTokens(t *testing.T) {
	f := newFixture(t, usableKey)

	if rec := f.call(syncPath, credentials(goodClient, goodSecret), true); rec.Code !=
		http.StatusNoContent {
		t.Fatalf("sync: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	if got := f.storedClientID(); got != goodClient {
		t.Fatalf("stored client ID: got %q, want %q", got, goodClient)
	}

	var stored string
	if err := f.testDB.DB.QueryRow(
		"SELECT client_secret FROM org_service_credentials WHERE org_id = 'acme-org-id'").Scan(&stored); err != nil {
		t.Fatalf("failed to read the stored row: %v", err)
	}
	if stored == goodSecret || strings.Contains(stored, goodSecret) {
		t.Fatal("the client secret is stored in plaintext")
	}

	// The token endpoint refuses the configured pair, so a token means the stored
	// credentials were used.
	token, err := client.NewIdentityClient(config.GetCDSRuntime().Config).FetchToken("acme")
	if err != nil || token != issuedToken {
		t.Fatalf("FetchToken: got %q, %v, want %q", token, err, issuedToken)
	}
}

func TestRefusesCredentialsTheIdentityProviderRejects(t *testing.T) {
	f := newFixture(t, usableKey)

	rec := f.call(syncPath, credentials(goodClient, "wrong-secret"), true)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "CDS-16003" {
		t.Fatalf("sync: got %d %s, want 400 CDS-16003", rec.Code, rec.Body.String())
	}
	if got := f.storedClientID(); got != "" {
		t.Fatalf("rejected credentials were stored: %q", got)
	}
}

func TestRefusesARequestItCannotServe(t *testing.T) {
	f := newFixture(t, usableKey)

	previous := utils.SubOrgResolver
	utils.SubOrgResolver = func(context.Context, string, string) (string, error) { return "child", nil }
	t.Cleanup(func() { utils.SubOrgResolver = previous })

	cases := []struct {
		name          string
		path          string
		body          string
		authenticated bool
		status        int
		code          string
	}{
		{"no credentials", syncPath, credentials(goodClient, goodSecret), false, http.StatusUnauthorized,
			"CDS-10002"},
		{"sub organization", "/t/acme/o/child-id/cds/api/v1/service-credentials/sync",
			credentials(goodClient, goodSecret), true, http.StatusBadRequest, "CDS-17007"},
		{"no client secret", syncPath, `{"event":"SERVICE_CREDENTIALS_UPDATED","org_id":"o","client_id":"x"}`, true,
			http.StatusBadRequest, "CDS-10001"},
		{"no event", syncPath, `{"org_id":"o","client_id":"x","client_secret":"y"}`, true,
			http.StatusBadRequest, "CDS-10001"},
		{"no org ID", syncPath, `{"event":"SERVICE_CREDENTIALS_UPDATED","client_id":"x","client_secret":"y"}`, true,
			http.StatusBadRequest, "CDS-10001"},
		{"unknown event", syncPath,
			`{"event":"SERVICE_CREDENTIALS_DELETED","org_id":"o","client_id":"x","client_secret":"y"}`, true,
			http.StatusBadRequest, "CDS-10001"},
		{"unknown field", syncPath,
			`{"event":"SERVICE_CREDENTIALS_UPDATED","org_id":"o","client_id":"x","client_secret":"y","app_id":"z"}`,
			true, http.StatusBadRequest, "CDS-10001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.call(c.path, c.body, c.authenticated)
			if rec.Code != c.status || errorCode(t, rec) != c.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body.String(), c.status, c.code)
			}
		})
	}
}

// Without a key CDS keeps the configured pair for every organization. The
// identity provider reads CDS-16004 as that, and sends no credentials.
func TestRefusesCredentialsWithoutAnEncryptionKey(t *testing.T) {
	f := newFixture(t, "")

	rec := f.call(syncPath, credentials(goodClient, goodSecret), true)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "CDS-16004" {
		t.Fatalf("sync: got %d %s, want 400 CDS-16004", rec.Code, rec.Body.String())
	}
}

// otherAdapter stands for the adapter of an identity provider other than WSO2
// IS. Only the methods the endpoint calls are implemented.
type otherAdapter struct {
	idp.Adapter
}

// verifyingAdapter is an otherAdapter whose identity provider sends service
// credentials, and accepts only acceptedClient.
type verifyingAdapter struct {
	otherAdapter
	acceptedClient string
	verifiedOrg    string
}

func (a *verifyingAdapter) VerifyServiceCredentials(_ context.Context, orgHandle, clientID,
	_ string) (idp.Organization, error) {
	a.verifiedOrg = orgHandle
	if clientID != a.acceptedClient {
		return idp.Organization{}, errors2.NewClientError(errors2.ServiceCredentialsRejected,
			http.StatusBadRequest)
	}
	return idp.Organization{Id: acmeOrgID, Handle: orgHandle, Name: "Acme"}, nil
}

// Any identity provider whose adapter verifies service credentials can send
// them; the endpoint does not depend on WSO2 IS.
func TestVerifiesCredentialsThroughTheAdapterOfTheIdentityProvider(t *testing.T) {
	f := newFixture(t, usableKey)
	adapter := &verifyingAdapter{acceptedClient: "other-idp-client"}
	idp.SetAdapter(adapter)

	rec := f.call(syncPath, credentials("other-idp-client", "other-idp-secret"), true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sync: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	if adapter.verifiedOrg != "acme" {
		t.Fatalf("the adapter verified organization %q, want acme", adapter.verifiedOrg)
	}
	if got := f.storedClientID(); got != "other-idp-client" {
		t.Fatalf("stored client ID: got %q, want other-idp-client", got)
	}

	rec = f.call(syncPath, credentials(goodClient, goodSecret), true)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "CDS-16003" {
		t.Fatalf("sync of a client the adapter refuses: got %d %s, want 400 CDS-16003", rec.Code,
			rec.Body.String())
	}
}

func TestRefusesCredentialsForAnIdentityProviderWithoutThem(t *testing.T) {
	f := newFixture(t, usableKey)
	idp.SetAdapter(otherAdapter{})

	rec := f.call(syncPath, credentials(goodClient, goodSecret), true)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "CDS-16004" {
		t.Fatalf("sync: got %d %s, want 400 CDS-16004", rec.Code, rec.Body.String())
	}
}

// The WSO2 IS adapter introspects a token of an organization with the
// organization's service credentials, at its own endpoint, and uses the
// configured introspection client for an organization without them.
func TestIntrospectsWithTheServiceCredentialsOfTheOrganization(t *testing.T) {
	f := newFixture(t, usableKey)
	adapter := idp.NewWSO2ISAdapter()

	claims, err := adapter.IntrospectToken(context.Background(), "a-token", "acme", "acme")
	if err != nil || claims["introspected_at"] != "/t/carbon.super/oauth2/introspect" {
		t.Fatalf("before the sync: got %v, %v, want the configured client at the super tenant", claims, err)
	}

	if rec := f.call(syncPath, credentials(goodClient, goodSecret), true); rec.Code !=
		http.StatusNoContent {
		t.Fatalf("sync: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	claims, err = adapter.IntrospectToken(context.Background(), "a-token", "acme", "acme")
	if err != nil || claims["introspected_at"] != "/t/acme/oauth2/introspect" {
		t.Fatalf("after the sync: got %v, %v, want the credentials of acme at its own endpoint", claims, err)
	}
}

// Credentials sealed under a key the deployment no longer holds fall back to the
// configured pair instead of failing every call for the organization.
func TestCredentialsUnderAnotherKeyFallBackToTheConfiguredPair(t *testing.T) {
	f := newFixture(t, usableKey)
	if _, err := f.testDB.DB.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('acme-org-id', 'acme', 'acme-org-id', 'ACTIVE')`); err != nil {
		t.Fatalf("failed to insert the organization: %v", err)
	}
	if _, err := f.testDB.DB.Exec(`INSERT INTO org_service_credentials (org_id, client_id, client_secret, key_id)
		VALUES ('acme-org-id', 'old-client', 'c2VhbGVk', 'another-key')`); err != nil {
		t.Fatalf("failed to insert the row: %v", err)
	}

	_, found, err := servicecreds.Get(context.Background(), "acme")
	if err != nil || found {
		t.Fatalf("Get: got found %t, %v, want the configured pair", found, err)
	}
}

func TestCheckStore(t *testing.T) {
	f := newFixture(t, usableKey)
	if err := servicecreds.CheckStore(context.Background()); err != nil {
		t.Fatalf("CheckStore with the table: %v", err)
	}
	if _, err := f.testDB.DB.Exec("DROP TABLE org_service_credentials"); err != nil {
		t.Fatalf("failed to drop the table: %v", err)
	}
	if err := servicecreds.CheckStore(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "003-service-credentials-postgres.sql") {
		t.Fatalf("CheckStore without the table: got %v, want the migration named", err)
	}
}

// CDS keys the credentials by the org ID that the identity provider reports for
// the client, which has to be the one the event names.
func TestRefusesAnOrgIDThatIsNotTheOrganizationOfTheClient(t *testing.T) {
	f := newFixture(t, usableKey)

	rec := f.call(syncPath, credentialsOf("another-org-id", goodClient, goodSecret), true)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "CDS-10001" {
		t.Fatalf("sync: got %d %s, want 400 CDS-10001", rec.Code, rec.Body.String())
	}
	if got := f.storedClientID(); got != "" {
		t.Fatalf("credentials of a wrong org ID were stored: %q", got)
	}
}

// The credentials arrive before CDS knows the organization. CDS stores it, so that
// the next calls find the credentials by the handle.
func TestStoresTheOrganizationOfTheCredentials(t *testing.T) {
	f := newFixture(t, usableKey)

	if rec := f.call(syncPath, credentials(goodClient, goodSecret), true); rec.Code != http.StatusNoContent {
		t.Fatalf("sync: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	var orgID string
	if err := f.testDB.DB.QueryRow("SELECT org_id FROM organizations WHERE org_handle = 'acme'").
		Scan(&orgID); err != nil || orgID != acmeOrgID {
		t.Fatalf("stored organization: got %q, %v, want %q", orgID, err, acmeOrgID)
	}
	creds, found, err := servicecreds.Get(context.Background(), "acme")
	if err != nil || !found || creds.OrgID != acmeOrgID || creds.ClientID != goodClient {
		t.Fatalf("Get: got %+v, %t, %v", creds, found, err)
	}
}

func TestRefusesCredentialsForAnOrganizationCDSKnowsUnderAnotherID(t *testing.T) {
	f := newFixture(t, usableKey)
	if _, err := f.testDB.DB.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('earlier-org-id', 'acme', 'earlier-org-id', 'ACTIVE')`); err != nil {
		t.Fatalf("failed to insert the organization: %v", err)
	}

	rec := f.call(syncPath, credentials(goodClient, goodSecret), true)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "CDS-10001" {
		t.Fatalf("sync: got %d %s, want 400 CDS-10001", rec.Code, rec.Body.String())
	}
}
