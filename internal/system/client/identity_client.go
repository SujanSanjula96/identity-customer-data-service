/*
 * Copyright (c) 2025-2026, WSO2 LLC. (http://www.wso2.com).
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
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	idpModel "github.com/wso2/identity-customer-data-service/internal/identity_provider/model"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"

	"github.com/google/uuid"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/servicecreds"
)

// managementAPIScope is what CDS asks a token for: the Identity Server
// management APIs it calls, and nothing else.
var managementAPIScope = strings.Join([]string{
	"internal_application_mgt_view",
	"internal_claim_meta_view",
	"internal_user_mgt_list",
	"internal_user_mgt_view",
	"internal_organization_view",
}, constants.SpaceSeparator)

// tokenEndpointStatusError is the cause of a token request the token endpoint
// answered with a status other than 200.
type tokenEndpointStatusError struct {
	StatusCode int
}

func (e *tokenEndpointStatusError) Error() string {
	return fmt.Sprintf("token endpoint non-200: %d", e.StatusCode)
}

type IdentityClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewIdentityClient creates an IdentityClient with a TLS/mTLS-ready HTTP client.
func NewIdentityClient(cfg config.Config) *IdentityClient {
	baseHostPort := cfg.AuthServer.Host
	if cfg.AuthServer.Port != "" {
		baseHostPort = cfg.AuthServer.Host + ":" + cfg.AuthServer.Port
	}
	log.GetLogger().Info("Creating IdentityClient with base URL: " + baseHostPort)

	httpClient, err := newOutboundHTTPClient(cfg.TLS, cfg.AuthServer.Host)
	if err != nil {
		log.GetLogger().Error("Failed to create outbound HTTPS client for IS", log.Error(err))
		os.Exit(1)
	}

	return &IdentityClient{
		BaseURL:    baseHostPort,
		HTTPClient: httpClient,
	}
}

// Builds an HTTP client with TLS/mTLS configuration for outbound requests.
// Validates the server using CA, and optionally presents a client certificate if mTLS is enabled.
func newOutboundHTTPClient(tlsCfg config.TLSConfig, serverHostForSNI string) (*http.Client, error) {
	// Resolve cert dir to absolute to avoid CWD surprises
	certDir := tlsCfg.CertDir
	cdsHome := utils.GetCDSHome()
	if certDir == "" {
		certDir = filepath.Join(cdsHome, "etc", "certs")
	}
	if !filepath.IsAbs(certDir) {
		if abs, err := filepath.Abs(certDir); err == nil {
			certDir = abs
		}
	}

	// Root CAs: start with system roots, then append trust store if provided.
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	trustFile := tlsCfg.TrustStore

	if trustFile != "" {
		trustPath := filepath.Join(certDir, trustFile)
		trustPEM, err := os.ReadFile(trustPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read trust_store at %s: %w", trustPath, err)
		}
		if ok := rootCAs.AppendCertsFromPEM(trustPEM); !ok {
			return nil, fmt.Errorf("failed to append certs from trust_store: %s", trustPath)
		}
	}

	// Client cert/key for mTLS (optional)
	var certificates []tls.Certificate
	if tlsCfg.MTLSEnabled {
		cdsPublicCrt := filepath.Join(certDir, tlsCfg.CDSPublicCert)
		cdsPrivatekey := filepath.Join(certDir, tlsCfg.CDSPrivateKey)
		pair, err := tls.LoadX509KeyPair(cdsPublicCrt, cdsPrivatekey)
		if err != nil {
			return nil, fmt.Errorf("failed to load cert/key (%s, %s): %w", cdsPublicCrt, cdsPrivatekey, err)
		}
		certificates = []tls.Certificate{pair}
	}

	tcfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      rootCAs,      // nil means use system CA certs
		Certificates: certificates, // empty if mTLS disabled
	}

	tr := &http.Transport{
		TLSClientConfig:     tcfg,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     60 * time.Second,
		MaxIdleConns:        100,
		MaxConnsPerHost:     100,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
	}, nil
}

// FetchToken retrieves an access token for the given org.
// If system app grant is enabled, it uses system_app_grant
// Otherwise, it falls back to client_credentials per org.
func (c *IdentityClient) FetchToken(orgHandle string) (string, error) {
	logger := log.GetLogger()
	authCfg := config.GetCDSRuntime().Config.AuthServer
	scope := managementAPIScope

	if authCfg.IsSystemAppGrantEnabled {
		logger.Debug(fmt.Sprintf("Fetching token using system_app_grant for org: %s", orgHandle))
		return c.fetchOrganizationToken(orgHandle, authCfg, scope)
	}

	logger.Debug(fmt.Sprintf("Fetching token using client_credentials for org: %s", orgHandle))
	return c.fetchClientCredentialsToken(orgHandle, authCfg, scope)
}

// orgAPIPathPrefix returns the IS URL segment used for API calls (claims, apps, SCIM).
// Sub-org UUIDs use /o — the org context is carried by the token, no UUID in path.
// Regular tenant handles use /t/{handle}.
func (c *IdentityClient) orgAPIPathPrefix(orgHandle string) string {
	if _, err := uuid.Parse(orgHandle); err == nil {
		return "/o"
	}
	return "/t/" + orgHandle
}

// orgEndpoint returns the https URL of path on host for the given organization. It rejects an org handle that
// is not a single plain path segment and confirms the URL stays on host, so request input cannot move the call
// to another host or path. Callers must url.PathEscape any other request-derived segment they put in path.
func orgEndpoint(host, orgHandle, path string) (string, error) {
	if !utils.IsValidOrgHandle(orgHandle) {
		return "", fmt.Errorf("invalid organization handle: %q", orgHandle)
	}
	u, err := url.Parse("https://" + host + path)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" || u.Host != host || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("unexpected endpoint for organization: %q", orgHandle)
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("unexpected endpoint for organization: %q", orgHandle)
		}
	}
	return u.String(), nil
}

func (c *IdentityClient) buildTokenEndpoint(orgId, tokenEndpoint string) (string, error) {
	if _, err := uuid.Parse(orgId); err == nil {
		// Sub-org token endpoint lives under carbon.super: /t/carbon.super/o/{uuid}/oauth2/token
		return orgEndpoint(c.BaseURL, orgId, "/t/carbon.super/o/"+orgId+tokenEndpoint)
	}
	return orgEndpoint(c.BaseURL, orgId, "/t/"+orgId+tokenEndpoint)
}

// fetchOrganizationToken obtains an organization-scoped token via system_app_grant.
func (c *IdentityClient) fetchOrganizationToken(
	orgHandle string,
	authCfg config.AuthServerConfig,
	scope string,
) (string, error) {

	logger := log.GetLogger()

	// Fetch super-tenant token
	baseForm := url.Values{}
	baseForm.Set("grant_type", "system_app_grant")
	baseForm.Set("scope", scope)
	baseForm.Set("organizationHandle", orgHandle)
	endpoint, err := c.buildTokenEndpoint("carbon.super", authCfg.TokenEndpoint)
	if err != nil {
		return "", err
	}
	logger.Debug(fmt.Sprintf("Fetching super-tenant system_app_grant token for org: %s", orgHandle))
	// Note: The super-tenant token is not used directly — the grant exchange happens using client credentials.
	orgToken, err := c.requestToken(endpoint, authCfg.ClientID, authCfg.ClientSecret, baseForm, orgHandle)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch super-tenant token for the organization:%s", orgHandle)
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.TOKEN_FETCH_FAILED.Code,
			Message:     errors2.TOKEN_FETCH_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	logger.Debug(fmt.Sprintf("Successfully obtained organization-scoped token for org: %s", orgHandle))
	return orgToken, nil
}

// fetchClientCredentialsToken obtains an organization-scoped token directly using client_credentials grant.
//
// The credentials are the organization's own where the Identity Server has sent
// them. They have to be: the endpoint is that organization's token endpoint, and
// an Identity Server client can only authenticate at the token endpoint of its
// own tenant. An organization with none uses the configured pair, as before.
func (c *IdentityClient) fetchClientCredentialsToken(orgId string, authCfg config.AuthServerConfig, scope string) (string, error) {

	clientID, clientSecret := authCfg.ClientID, authCfg.ClientSecret
	if servicecreds.Enabled() {
		// FetchToken carries no context of its own, and this is a primary-key read that
		// the token request around it dwarfs.
		creds, found, err := servicecreds.Get(context.Background(), orgId)
		if err != nil {
			return "", err
		}
		if found {
			clientID, clientSecret = creds.ClientID, creds.ClientSecret
		}
	}

	endpoint, err := c.buildTokenEndpoint(orgId, authCfg.TokenEndpoint)
	if err != nil {
		return "", err
	}
	return c.requestToken(endpoint, clientID, clientSecret, clientCredentialsForm(scope), orgId)
}

// VerifyClientCredentials asks the token endpoint of orgHandle for a token with
// the given client, so that credentials are known to work before they are
// stored. It returns a client error when the token endpoint refuses the client,
// and a server error when the token endpoint cannot be reached.
func (c *IdentityClient) VerifyClientCredentials(orgHandle, clientID, clientSecret string) error {
	authCfg := config.GetCDSRuntime().Config.AuthServer
	endpoint, err := c.buildTokenEndpoint(orgHandle, authCfg.TokenEndpoint)
	if err != nil {
		return err
	}
	_, err = c.requestToken(endpoint, clientID, clientSecret, clientCredentialsForm(managementAPIScope), orgHandle)
	if err == nil {
		return nil
	}

	var serverErr *errors2.ServerError
	var statusErr *tokenEndpointStatusError
	if errors.As(err, &serverErr) && errors.As(serverErr.Err, &statusErr) &&
		statusErr.StatusCode >= 400 && statusErr.StatusCode < 500 {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.ServiceCredentialsRejected.Code,
			Message: errors2.ServiceCredentialsRejected.Message,
			Description: fmt.Sprintf("The token endpoint of the organization %s refused the client %s with "+
				"status %d.", orgHandle, clientID, statusErr.StatusCode),
		}, http.StatusBadRequest)
	}
	return err
}

func clientCredentialsForm(scope string) url.Values {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", scope)
	return form
}

// requestToken performs the actual HTTP POST and extracts access_token from JSON.
func (c *IdentityClient) requestToken(endpoint, clientID, clientSecret string,
	form url.Values, orgId string,
) (string, error) {

	logger := log.GetLogger()

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to create token request for the organization:%s in system_app_grant", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.TOKEN_FETCH_FAILED.Code,
			Message:     errors2.TOKEN_FETCH_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch token for the organization:%s in system_app_grant", orgId)
		logger.Debug(err.Error())
		// This is an internal communication. So for the clients of CDS, we treat this as a server error.
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.TOKEN_FETCH_FAILED.Code,
			Message:     errors2.TOKEN_FETCH_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf(
			"Token endpoint returned status %d for org:%s. Endpoint: %s. Response: %s",
			resp.StatusCode, orgId, endpoint, strings.TrimSpace(string(bodyBytes)),
		)
		logger.Debug(errorMsg)
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.TOKEN_FETCH_FAILED.Code,
			Message:     errors2.TOKEN_FETCH_FAILED.Message,
			Description: errorMsg,
		}, &tokenEndpointStatusError{StatusCode: resp.StatusCode})
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		errorMsg := fmt.Sprintf("Failed to parse token response for the organization:%s in system_app_grant", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.TOKEN_FETCH_FAILED.Code,
			Message:     errors2.TOKEN_FETCH_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	if result.AccessToken == "" {
		errorMsg := fmt.Sprintf("Failed to read token response for the organization:%s in system_app_grant", orgId)
		logger.Debug(errorMsg)
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.TOKEN_FETCH_FAILED.Code,
			Message:     errors2.TOKEN_FETCH_FAILED.Message,
			Description: errorMsg,
		}, nil)
	}

	return result.AccessToken, nil
}

// FetchApplicationIdentifier fetches the application ID for a given application identifier (clientId or issuer)
func (c *IdentityClient) FetchApplicationIdentifier(applicationIdentifier, orgHandle string) (idpModel.ApplicationsListResponse, error) {

	logger := log.GetLogger()
	var result idpModel.ApplicationsListResponse
	filter := fmt.Sprintf(`clientId eq %s or issuer eq %s`, applicationIdentifier, applicationIdentifier)
	base, err := orgEndpoint(c.BaseURL, orgHandle, c.orgAPIPathPrefix(orgHandle)+"/api/server/v1/applications")
	if err != nil {
		return result, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return result, err
	}

	q := u.Query()
	q.Set("filter", filter)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return result, err
	}

	token, err := c.FetchToken(orgHandle)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to get token for org: %s", orgHandle), log.Error(err))
		return result, err
	}

	authCfg := config.GetCDSRuntime().Config.AuthServer
	if authCfg.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	log.GetLogger().Info("Filtering applications at:" + u.String())
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch applications for org: %s", orgHandle)
		return result, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APPLICATIONS_FAILED.Code,
			Message:     errors2.GET_APPLICATIONS_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		errorMsg := fmt.Sprintf("Failed to fetch applications for org: %s", orgHandle)
		return result, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APPLICATIONS_FAILED.Code,
			Message:     errors2.GET_APPLICATIONS_FAILED.Message,
			Description: errorMsg,
		}, fmt.Errorf("applications endpoint returned status: %d", resp.StatusCode))
	}

	if err := json.Unmarshal(body, &result); err != nil {
		errorMsg := fmt.Sprintf("Failed to parse applications response for org: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return result, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APPLICATIONS_FAILED.Code,
			Message:     errors2.GET_APPLICATIONS_FAILED.Message,
			Description: errorMsg,
		}, err)
	}

	return result, nil
}

// GetApplication fetches an application by ID. exists is false when it is not found.
func (c *IdentityClient) GetApplication(appID, orgHandle string) (idpModel.ApplicationItem, bool, error) {

	logger := log.GetLogger()
	var app idpModel.ApplicationItem

	token, err := c.FetchToken(orgHandle)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to get token for resolving application:%s of org:%s", appID, orgHandle),
			log.Error(err))
		return app, false, err
	}

	appsFailed := func(desc string, cause error) error {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APPLICATIONS_FAILED.Code,
			Message:     errors2.GET_APPLICATIONS_FAILED.Message,
			Description: desc,
		}, cause)
	}

	appEndpoint, err := orgEndpoint(c.BaseURL, orgHandle,
		"/t/"+orgHandle+"/api/server/v1/applications/"+url.PathEscape(appID))
	if err != nil {
		return app, false, err
	}
	req, err := http.NewRequest("GET", appEndpoint, nil)
	if err != nil {
		return app, false, err
	}
	authCfg := config.GetCDSRuntime().Config.AuthServer
	if authCfg.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return app, false, appsFailed(fmt.Sprintf("Failed to fetch application:%s for org:%s", appID, orgHandle), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return app, false, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return app, false, appsFailed(fmt.Sprintf("Failed to read application response for app:%s org:%s",
			appID, orgHandle), err)
	}
	if resp.StatusCode != http.StatusOK {
		errorMsg := fmt.Sprintf("Applications endpoint returned status %d for app:%s org:%s. Response: %s",
			resp.StatusCode, appID, orgHandle, strings.TrimSpace(string(body)))
		return app, false, appsFailed(errorMsg, fmt.Errorf("applications endpoint non-200: %d", resp.StatusCode))
	}

	if err := json.Unmarshal(body, &app); err != nil {
		return app, false, appsFailed(fmt.Sprintf("Failed to parse application response for app:%s org:%s",
			appID, orgHandle), err)
	}
	return app, true, nil
}

// IntrospectToken introspects an opaque token using the introspection endpoint.
func (c *IdentityClient) IntrospectToken(token, orgHandle string) (map[string]interface{}, error) {

	form := url.Values{}
	form.Set("token", token)

	authConfig := config.GetCDSRuntime().Config.AuthServer
	var introspectionEndpoint string
	var err error
	if authConfig.IsSystemAppGrantEnabled {
		log.GetLogger().Debug("Token introspection with IS through internal host as system_app_grant is enabled")
		introspectionEndpoint, err = orgEndpoint(authConfig.ADUISHostname, orgHandle,
			c.orgAPIPathPrefix(orgHandle)+authConfig.IntrospectionEndPoint)
		form.Set("appTenant", "carbon.super")
	} else {
		// It is possible to introspect any token against super tenant introspection endpoint and super tenant client credentials
		introspectionEndpoint, err = orgEndpoint(c.BaseURL, "carbon.super",
			"/t/carbon.super"+authConfig.IntrospectionEndPoint)
	}
	if err != nil {
		return nil, err
	}

	return c.introspect(introspectionEndpoint, authConfig.IntrospectionClientId, authConfig.IntrospectionClientSecret,
		form)
}

// IntrospectTokenAsOrg introspects the token at the introspection endpoint of
// orgHandle, authenticated as the client of that organization. An Identity
// Server client can only authenticate at the endpoints of its own tenant.
func (c *IdentityClient) IntrospectTokenAsOrg(token, orgHandle, clientID, clientSecret string) (map[string]interface{},
	error) {
	authConfig := config.GetCDSRuntime().Config.AuthServer
	introspectionEndpoint, err := orgEndpoint(c.BaseURL, orgHandle,
		"/t/"+orgHandle+authConfig.IntrospectionEndPoint)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("token", token)
	return c.introspect(introspectionEndpoint, clientID, clientSecret, form)
}

func (c *IdentityClient) introspect(introspectionEndpoint, clientID, clientSecret string,
	form url.Values) (map[string]interface{}, error) {
	log.GetLogger().Info("Introspecting token at endpoint: " + introspectionEndpoint)
	req, err := http.NewRequest("POST", introspectionEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to introspect token"
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        "TOKEN_INTROSPECTION_FAILED",
			Message:     "Unable to introspect access token",
			Description: errorMsg,
		}, http.StatusUnauthorized)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("Introspection endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        "TOKEN_INTROSPECTION_INVALID_RESPONSE",
			Message:     "Token introspection failed",
			Description: errorMsg,
		}, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *IdentityClient) GetProfileSchema(orgHandle string) ([]model.ProfileSchemaAttribute, error) {

	logger := log.GetLogger()
	localClaimsMap, err := c.GetLocalClaimsMap(orgHandle)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to fetch local claims for the organization:%s", orgHandle), log.Error(err))
		return nil, err
	}

	dialects, err := c.GetAllDialects(orgHandle)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to fetch dialects for the organization:%s", orgHandle), log.Error(err))
		return nil, err
	}
	logger.Info(fmt.Sprintf("Fetched %d dialects for the organization:%s", len(dialects), orgHandle))
	var result []model.ProfileSchemaAttribute
	for _, dialect := range dialects {
		logger.Info("Processing dialect", log.String("dialectURI", fmt.Sprintf("%v", dialect["dialectURI"])))
		dialectURI := fmt.Sprintf("%v", dialect["dialectURI"])
		dialectID := fmt.Sprintf("%v", dialect["id"])

		// Only consider SCIM-based dialects
		if !(strings.HasPrefix(dialectURI, "urn:scim:") || strings.HasPrefix(dialectURI, "urn:ietf:")) {
			continue
		}
		// Skip SCIM core v1 dialect explicitly
		if dialectURI == "urn:scim:schemas:core:1.0" {
			continue
		}

		claims, err := c.GetClaimsByDialect(dialectID, orgHandle)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to fetch claims for dialect %s", dialectURI))
			continue
		}

		existingAttrs := map[string]bool{}
		pendingParents := map[string][]model.SubAttribute{}
		parentDialects := map[string]string{}

		for _, scimClaim := range claims {
			localURI := fmt.Sprintf("%v", scimClaim["mappedLocalClaimURI"])
			localClaim, ok := localClaimsMap[localURI]
			if !ok {
				continue
			}

			attr, subAttr, parent := ConvertSCIMClaimWithLocal(scimClaim, localClaim, claims, orgHandle, dialectURI)
			result = append(result, attr)
			existingAttrs[attr.AttributeName] = true

			if subAttr != nil {
				parentKey := parent
				pendingParents[parentKey] = append(pendingParents[parentKey], *subAttr)

				// Store dialect only if it's not already set
				if _, exists := parentDialects[parentKey]; !exists {
					parentDialects[parentKey] = attr.SCIMDialect
				}
			}
		}

		// Add synthetic parent objects if missing
		for parent, subs := range pendingParents {
			if !existingAttrs[parent] {
				if parent == "identity_attributes.emailaddress" {
					logger.Debug(fmt.Sprintf("Skip deriving complex parent attribute: '%s'", parent))
					continue // Skip as this has a separate attribute configuration
				}
				dialect := parentDialects[parent]
				if dialect == "" {
					dialect = "urn:synthetic" // fallback
				}
				logger.Warn(fmt.Sprintf("Adding synthetic parent attribute: %s", parent))
				result = append(result, model.ProfileSchemaAttribute{
					OrgId:         orgHandle,
					AttributeId:   uuid.New().String(),
					AttributeName: parent,
					ValueType:     constants.ComplexDataType,
					MergeStrategy: constants.MergeStrategyOverwrite,
					Mutability:    constants.MutabilityReadWrite,
					DisplayName:   utils.ResolveDisplayNameFromAttribute(parent),
					SubAttributes: subs,
					SCIMDialect:   dialect, // mark as generated
				})
			}
		}
	}

	logger.Info(fmt.Sprintf("Successfully built %d profile schema attributes for org: %s", len(result), orgHandle))
	return result, nil
}

func (c *IdentityClient) GetAllDialects(orgHandle string) ([]map[string]interface{}, error) {
	endpoint, err := orgEndpoint(c.BaseURL, orgHandle, c.orgAPIPathPrefix(orgHandle)+"/api/server/v1/claim-dialects")
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", endpoint, nil)
	logger := log.GetLogger()
	token, err := c.FetchToken(orgHandle)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to get token for fetching the all dialects of the organization:%s",
			orgHandle), log.Error(err))
		return nil, err
	}
	authCfg := config.GetCDSRuntime().Config.AuthServer
	if authCfg.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// This is an internal communication. So for the clients of CDS, we treat this as a server error.
		errorMsg := fmt.Sprintf("Failed to fetch all dialects for the organization:%s", orgHandle)
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_SCIM_DIALECTS.Code,
			Message:     errors2.GET_SCIM_DIALECTS.Message,
			Description: errorMsg,
		}, err)
	}
	defer resp.Body.Close()

	var dialects []map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(body, &dialects)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to parse dialects response for the organization:%s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_SCIM_DIALECTS.Code,
			Message:     errors2.GET_SCIM_DIALECTS.Message,
			Description: errorMsg,
		}, err)
	}
	return dialects, err
}

func (c *IdentityClient) GetClaimsByDialect(dialectID, orgId string) ([]map[string]interface{}, error) {
	endpoint, err := orgEndpoint(c.BaseURL, orgId,
		c.orgAPIPathPrefix(orgId)+"/api/server/v1/claim-dialects/"+url.PathEscape(dialectID)+"/claims")
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", endpoint, nil)
	token, err := c.FetchToken(orgId)
	logger := log.GetLogger()
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to get token for fetching the claims of dialectID:%s of the organization:%s", dialectID, orgId), log.Error(err))
		return nil, err
	}
	authCfg := config.GetCDSRuntime().Config.AuthServer
	if authCfg.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to fetch claims for dialectID:%s of the organization:%s", dialectID, orgId)
		logger.Debug(errMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_DIALECT_CLAIMS.Code,
			Message:     errors2.GET_DIALECT_CLAIMS.Message,
			Description: errMsg,
		}, err)
	}
	defer resp.Body.Close()

	var claims []map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(body, &claims)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to parse claims response for dialectID:%s of the organization:%s", dialectID, orgId)
		logger.Debug(errMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_DIALECT_CLAIMS.Code,
			Message:     errors2.GET_DIALECT_CLAIMS.Message,
			Description: errMsg,
		}, err)
	}
	return claims, err
}

func (c *IdentityClient) GetLocalClaimsMap(orgId string) (map[string]map[string]interface{}, error) {

	endpoint, err := orgEndpoint(c.BaseURL, orgId, c.orgAPIPathPrefix(orgId)+"/api/server/v1/claim-dialects/local/claims")
	if err != nil {
		return nil, err
	}
	logger := log.GetLogger()
	logger.Info("Fetching local claims from endpoint: " + endpoint)
	req, _ := http.NewRequest("GET", endpoint, nil)
	token, err := c.FetchToken(orgId)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to get token for fetching the local claims of the organization:%s",
			orgId), log.Error(err))
		return nil, err
	}
	authCfg := config.GetCDSRuntime().Config.AuthServer
	if authCfg.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch local claims for the organization:%s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_LOCAL_CLAIMS_FAILED.Code,
			Message:     errors2.GET_LOCAL_CLAIMS_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	defer resp.Body.Close()

	var claims []map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		errorMsg := fmt.Sprintf("Failed to fetch local claims, status code: %d, response: %s", resp.StatusCode, string(body))
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_LOCAL_CLAIMS_FAILED.Code,
			Message:     errors2.GET_LOCAL_CLAIMS_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	err = json.Unmarshal(body, &claims)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to parse local claims response for the organization:%s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_LOCAL_CLAIMS_FAILED.Code,
			Message:     errors2.GET_LOCAL_CLAIMS_FAILED.Message,
			Description: errorMsg,
		}, err)
	}

	// Build map using claimURI
	claimMap := make(map[string]map[string]interface{})
	for _, claim := range claims {
		uri := fmt.Sprintf("%v", claim["claimURI"])
		claimMap[uri] = claim
	}
	return claimMap, nil
}

func extractClaimKeyFromLocalURI(localURI string) string {
	parts := strings.Split(localURI, "/")
	return parts[len(parts)-1]
}

func ConvertSCIMClaimWithLocal(
	scim map[string]interface{},
	local map[string]interface{},
	allClaims []map[string]interface{},
	orgId, dialectURI string,
) (model.ProfileSchemaAttribute, *model.SubAttribute, string) {

	claimURI := fmt.Sprintf("%v", scim["claimURI"])
	localURI := fmt.Sprintf("%v", scim["mappedLocalClaimURI"])
	attrKey := extractClaimKeyFromLocalURI(localURI)

	readOnly := false
	multiValued := false
	if val, ok := local["readOnly"].(bool); ok {
		readOnly = val
	}
	valueType := "string" // Default
	if val, ok := local["dataType"].(string); ok {
		valueType = val
	}
	if props, ok := local["properties"].([]interface{}); ok {
		for _, p := range props {
			prop, ok := p.(map[string]interface{})
			if !ok {
				log.GetLogger().Debug(fmt.Sprintf("GetProfileSchema: skipping non-map property entry (type=%T) for org=%s", p, orgId))
				continue
			}
			if prop["key"] == "multiValued" && prop["value"] == "true" {
				multiValued = true
			}
		}
	}

	// Handle canonicalValues
	var canonicalValues []model.CanonicalValue
	if vals, ok := local["canonicalValues"].([]interface{}); ok {
		for _, val := range vals {
			if valMap, ok := val.(map[string]interface{}); ok {
				canonical := model.CanonicalValue{}
				if v, ok := valMap["value"].(string); ok {
					canonical.Value = v
				}
				if l, ok := valMap["label"].(string); ok {
					canonical.Label = l
				}
				canonicalValues = append(canonicalValues, canonical)
			}
		}
	}

	var displayName string
	if dn, ok := local["displayName"].(string); ok {
		displayName = dn
	} else {
		displayName = utils.ResolveDisplayNameFromAttribute(attrKey)
	}

	// Find sub-attributes for the current attribute (if it's a parent)
	var subAttrs []model.SubAttribute
	for _, otherClaim := range allClaims {
		otherURI := fmt.Sprintf("%v", otherClaim["claimURI"])

		if strings.HasPrefix(otherURI, claimURI+".") {
			mappedLocalURI := fmt.Sprintf("%v", otherClaim["mappedLocalClaimURI"])

			// Ensure mapped local URI is truly nested under the current local URI
			if strings.HasPrefix(mappedLocalURI, localURI+".") {
				subAttrKey := extractClaimKeyFromLocalURI(mappedLocalURI)

				if strings.HasPrefix(subAttrKey, attrKey+".") {
					subAttrKey = strings.TrimPrefix(subAttrKey, attrKey+".")
				}

				subAttrs = append(subAttrs, model.SubAttribute{
					AttributeId:   fmt.Sprintf("%v", uuid.New().String()),
					AttributeName: "identity_attributes." + attrKey + "." + subAttrKey,
				})
			}
		}
	}

	// Override to complex if it has sub-attributes
	if len(subAttrs) > 0 {
		valueType = "complex"
	}

	fullAttrName := "identity_attributes." + attrKey

	// Check if this is a sub-attribute (i.e., contains a dot after the prefix)
	if strings.Contains(attrKey, ".") {
		parentAttrName := "identity_attributes." + strings.Split(attrKey, ".")[0]
		subAttr := model.SubAttribute{
			AttributeId:   fmt.Sprintf("%v", uuid.New().String()),
			AttributeName: fullAttrName,
		}
		return model.ProfileSchemaAttribute{
			OrgId:           orgId,
			AttributeId:     subAttr.AttributeId,
			AttributeName:   fullAttrName,
			ValueType:       valueType,
			MergeStrategy:   "overwrite",
			DisplayName:     displayName,
			Mutability:      ifThenElse(readOnly, "readOnly", "readWrite"),
			MultiValued:     multiValued,
			CanonicalValues: canonicalValues,
			SubAttributes:   nil,
			SCIMDialect:     dialectURI,
		}, &subAttr, parentAttrName
	}

	// It's a top-level or parent attribute
	return model.ProfileSchemaAttribute{
		OrgId:           orgId,
		AttributeId:     fmt.Sprintf("%v", uuid.New().String()),
		AttributeName:   fullAttrName,
		ValueType:       valueType,
		MergeStrategy:   "overwrite",
		Mutability:      ifThenElse(readOnly, "readOnly", "readWrite"),
		DisplayName:     displayName,
		MultiValued:     multiValued,
		CanonicalValues: canonicalValues,
		SubAttributes:   subAttrs,
		SCIMDialect:     dialectURI,
	}, nil, ""
}

func ifThenElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// GetSCIMUser fetches a SCIM user by ID
func (c *IdentityClient) GetSCIMUser(orgId, userId string) (map[string]interface{}, error) {

	endpoint, err := orgEndpoint(c.BaseURL, orgId, c.orgAPIPathPrefix(orgId)+"/scim2/Users/"+url.PathEscape(userId))
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", endpoint, nil)
	token, err := c.FetchToken(orgId)
	logger := log.GetLogger()
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to get token for fetching the SCIM user:%s of the organization:%s",
			userId, orgId), log.Error(err))
		return nil, err
	}
	authCfg := config.GetCDSRuntime().Config.AuthServer
	if authCfg.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to fetch SCIM user:%s of the organization:%s", userId, orgId)
		logger.Debug(errMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_SCIM_USER_FAILED.Code,
			Message:     errors2.GET_SCIM_USER_FAILED.Message,
			Description: errMsg,
		}, err)
	}
	defer resp.Body.Close()

	var user map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &user); err != nil {
		errMsg := fmt.Sprintf("Failed to parse SCIM user response for user:%s of the organization:%s", userId, orgId)
		logger.Debug(errMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_SCIM_USER_FAILED.Code,
			Message:     errors2.GET_SCIM_USER_FAILED.Message,
			Description: errMsg,
		}, err)
	}

	return flattenSCIMClaims(user), nil
}

func flattenSCIMClaims(user map[string]interface{}) map[string]interface{} {
	flat := make(map[string]interface{})

	for key, val := range user {
		switch v := val.(type) {
		case map[string]interface{}:
			for subKey, subVal := range v {
				flat[key+"."+subKey] = subVal
			}
		default:
			flat[key] = val
		}
	}

	return flat
}
