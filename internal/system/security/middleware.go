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

package security

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/authn"
	"github.com/wso2/identity-customer-data-service/internal/system/authz"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// AuthnWithAdminCredentials performs authentication using admin credentials from the request.
func AuthnWithAdminCredentials(r *http.Request) error {

	authHeader := r.Header.Get("Authorization")
	logger := log.GetLogger()
	if authHeader == "" || !strings.HasPrefix(authHeader, "Basic ") {
		logger.Debug("Missing or invalid Authorization header")
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UN_AUTHORIZED.Code,
			Message:     errors.UN_AUTHORIZED.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Basic "))

	isValidAdmin, err := validateAdminCredentials(token)
	if err != nil || !isValidAdmin {
		logger.Debug("Invalid admin credentials")
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UN_AUTHORIZED.Code,
			Message:     errors.UN_AUTHORIZED.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
	}

	return nil
}

func validateAdminCredentials(token string) (bool, error) {

	authServerConfig := config.GetCDSRuntime().Config.AuthServer
	username := strings.TrimSpace(authServerConfig.AdminUsername)
	password := strings.TrimSpace(authServerConfig.AdminPassword)
	logger := log.GetLogger()
	if username == "" || password == "" || token == "" {
		logger.Debug("Admin credentials are not set properly in the configuration.")
		return false, nil
	}

	creds := username + ":" + password
	expected := base64.StdEncoding.EncodeToString([]byte(creds))

	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
		logger.Debug("Admin credentials validated successfully.")
		return true, nil
	}

	return false, nil
}

// AuthnAndAuthz performs authentication and authorization for the given HTTP request and operation.
// In a sub organization, the `org:` form of the operation is authorized.
func AuthnAndAuthz(r *http.Request, operation string) error {

	claims, err := authenticate(r)
	if err != nil {
		return err
	}
	return authorize(r, claims, operation)
}

// AuthnAndAuthzPrimary authenticates the request and authorizes an operation that only a primary
// organization can run. A request in a sub organization is refused.
func AuthnAndAuthzPrimary(r *http.Request, operation string) error {

	claims, err := authenticate(r)
	if err != nil {
		return err
	}
	return authorizePrimary(r, claims, operation)
}

// authenticate validates the bearer token for the organization of the path and returns its claims.
func authenticate(r *http.Request) (map[string]interface{}, error) {

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") || authHeader == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UN_AUTHORIZED.Code,
			Message:     errors.UN_AUTHORIZED.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
		return nil, clientError
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)

	token := strings.TrimPrefix(authHeader, "Bearer ")

	//  Validate token
	claims, err := authn.ValidateTokenForOrg(token, orgHandle, pathOrgIdOf(r))
	if err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UN_AUTHORIZED.Code,
			Message:     errors.UN_AUTHORIZED.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
		return nil, clientError
	}
	return claims, nil
}

// authorize checks the scopes of the claims against the operation.
func authorize(r *http.Request, claims map[string]interface{}, operation string) error {

	//  Validate authorization
	scope, ok := claims["scope"].(string)
	if !ok {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.FORBIDDEN.Code,
			Message:     errors.FORBIDDEN.Message,
			Description: errors.FORBIDDEN.Description,
		}, http.StatusForbidden)
		return clientError
	}

	if !authz.ValidatePermission(scope, authz.OperationFor(operation, pathOrgIdOf(r))) {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.FORBIDDEN.Code,
			Message:     errors.FORBIDDEN.Message,
			Description: "Do not have permission to perform this operation",
		}, http.StatusForbidden)
		return clientError
	}
	return nil
}

// authorizePrimary refuses the operation in a sub organization, and authorizes it in a primary
// organization.
func authorizePrimary(r *http.Request, claims map[string]interface{}, operation string) error {

	if pathOrgIdOf(r) != "" {
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.SUB_ORG_CONFIG_NOT_ALLOWED.Code,
			Message:     errors.SUB_ORG_CONFIG_NOT_ALLOWED.Message,
			Description: "Only the primary organization can perform this operation.",
		}, http.StatusBadRequest)
	}
	return authorize(r, claims, operation)
}

// pathOrgIdOf returns the organization ID of a sub organization path, or empty for a primary
// organization path.
func pathOrgIdOf(r *http.Request) string {

	pathOrgId, _ := r.Context().Value(constants.OrgIdContextKey).(string)
	return pathOrgId
}
