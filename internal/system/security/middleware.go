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

// Package security authenticates and authorizes the requests to the CDS APIs.
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
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// AuthnWithAdminCredentials performs authentication using admin credentials from the request. With a sync
// listener of its own, the credentials are accepted on that listener only, so they are worth nothing on the
// public one.
func AuthnWithAdminCredentials(r *http.Request) error {
	authHeader := r.Header.Get("Authorization")
	logger := log.GetLogger()
	if config.GetCDSRuntime().Config.SyncSeparate() && !OnSyncListener(r) {
		logger.Debug("Admin credentials on the public listener while the sync endpoints have their own")
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.Unauthorized.Code,
			Message:     errors.Unauthorized.Message,
			Description: "The admin credentials are accepted on the sync port only",
		}, http.StatusUnauthorized)
	}
	if authHeader == "" || !strings.HasPrefix(authHeader, "Basic ") {
		logger.Debug("Missing or invalid Authorization header")
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.Unauthorized.Code,
			Message:     errors.Unauthorized.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Basic "))

	if !validateAdminCredentials(token) {
		logger.Debug("Invalid admin credentials")
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.Unauthorized.Code,
			Message:     errors.Unauthorized.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
	}

	// The deployment credentials identify a trusted back-channel caller, not a person. The record
	// names the credential type, not the configured user name.
	reqctx.From(r.Context()).SetActor(reqctx.Actor{
		Type: reqctx.ActorService,
		ID:   reqctx.ServiceAdminActorID,
	})
	return nil
}

func validateAdminCredentials(token string) bool {
	authServerConfig := config.GetCDSRuntime().Config.AuthServer
	username := strings.TrimSpace(authServerConfig.AdminUsername)
	password := strings.TrimSpace(authServerConfig.AdminPassword)
	logger := log.GetLogger()
	if username == "" || password == "" || token == "" {
		logger.Debug("Admin credentials are not set properly in the configuration.")
		return false
	}

	creds := username + ":" + password
	expected := base64.StdEncoding.EncodeToString([]byte(creds))

	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
		logger.Debug("Admin credentials validated successfully.")
		return true
	}

	return false
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
			Code:        errors.Unauthorized.Code,
			Message:     errors.Unauthorized.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
		return nil, clientError
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)

	token := strings.TrimPrefix(authHeader, "Bearer ")

	//  Validate token
	claims, err := authn.ValidateTokenForOrg(token, orgHandle, utils.ExtractPrimaryOrgHandle(r), pathOrgIDOf(r))
	if err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.Unauthorized.Code,
			Message:     errors.Unauthorized.Message,
			Description: "Missing or invalid Authorization header",
		}, http.StatusUnauthorized)
		return nil, clientError
	}
	reqctx.From(r.Context()).SetActor(actorOf(claims))
	return claims, nil
}

// actorOf derives the actor of a request from the claims of its token. The actor is an
// application, identified by its client ID, when the token was issued to the client itself. Else
// it is a user, and the client ID names the application that the user acted through. The log
// streams use the actor, so that every record of a request names the same principal.
func actorOf(claims map[string]interface{}) reqctx.Actor {
	subject, _ := claims[constants.SUBClaim].(string)
	clientID, _ := claims[constants.ClientIDClaim].(string)
	if clientID == "" {
		clientID, _ = claims[constants.AZPClaim].(string)
	}
	// An introspection response can name the user by username only. A username is personal data,
	// so the actor never holds it. It only helps to tell a user from an application.
	username, _ := claims[constants.UsernameClaim].(string)
	principal := subject
	if principal == "" {
		principal = username
	}

	tokenType, _ := claims[constants.AuthorizedUserTypeClaim].(string)
	if strings.EqualFold(tokenType, constants.AuthorizedUserTypeApplication) ||
		principal == "" || principal == clientID {
		// The ID of an application actor is its client ID, so the record does not repeat it.
		return reqctx.Actor{Type: reqctx.ActorApplication, ID: clientID}
	}
	// A user with no subject claim has no ID in the record.
	return reqctx.Actor{Type: reqctx.ActorUser, ID: subject, ClientID: clientID}
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

	if !authz.ValidatePermission(scope, authz.OperationFor(operation, pathOrgIDOf(r))) {
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
	if pathOrgIDOf(r) != "" {
		return errors.NewClientError(errors.ErrorMessage{
			Code:        errors.SubOrgConfigNotAllowed.Code,
			Message:     errors.SubOrgConfigNotAllowed.Message,
			Description: "Only the primary organization can perform this operation.",
		}, http.StatusBadRequest)
	}
	return authorize(r, claims, operation)
}

// pathOrgIDOf returns the organization ID of a sub organization path, or empty for a primary
// organization path.
func pathOrgIDOf(r *http.Request) string {
	pathOrgID, _ := r.Context().Value(constants.OrgIDContextKey).(string)
	return pathOrgID
}
