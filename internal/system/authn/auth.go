/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

package authn

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

var (
	expectedAudience = "iam-cds"
)

// ValidateAuthenticationAndReturnClaims validates Authorization: Bearer token from the HTTP request
func ValidateAuthenticationAndReturnClaims(ctx context.Context, token, orgHandle string) (
	map[string]interface{}, error) {

	logger := log.GetLogger()
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	if IsJWT(token) {
		logger.Debug(fmt.Sprintf("Token is identified as JWT. Validating JWT for organization: '%s'", orgHandle))

		// Parse JWT to get claims (contains org information)
		claims, err := ParseJWTClaims(token)
		if err != nil {
			logger.Debug(fmt.Sprintf("Failed to parse JWT claims for organization: '%s'", orgHandle),
				log.Error(err))
			return nil, unauthorizedError()
		}

		// Validate org claims against request org handle
		if !validateClaims(orgHandle, claims) {
			logger.Debug("JWT claims validation failed")
			return nil, unauthorizedError()
		}

		// Introspect to check if token is still active (not revoked)
		introspectionClaims, err := identityClient.IntrospectToken(ctx, token, orgHandle)
		if err != nil {
			logger.Error(fmt.Sprintf("JWT token introspection failed for organization: '%s'", orgHandle),
				log.Error(err))
			return nil, unauthorizedError()
		}

		active, ok := introspectionClaims["active"].(bool)
		if !ok || !active {
			logger.Debug("JWT token is not active according to introspection")
			return nil, unauthorizedError()
		}

		// Return JWT claims (contains org info needed downstream)
		return claims, nil
	}

	// For opaque tokens, introspection is the only way to validate
	logger.Debug(fmt.Sprintf("Token is identified as opaque. Validating opaque token for organization: '%s' "+
		"using introspection", orgHandle))

	introspectionClaims, err := identityClient.IntrospectToken(ctx, token, orgHandle)
	if err != nil {
		logger.Error(fmt.Sprintf("Opaque token introspection failed for organization: '%s'", orgHandle),
			log.Error(err))
		return nil, unauthorizedError()
	}

	active, ok := introspectionClaims[constants.ActiveClaim].(bool)
	if !ok || !active {
		logger.Debug("Opaque token is not active according to introspection")
		return nil, unauthorizedError()
	}

	clientApp, ok := introspectionClaims[constants.ClientIdClaim].(string)
	if !ok || clientApp == "" {
		logger.Debug(fmt.Sprintf("Introspected token does not have a valid client_id claim for "+
			"organization: '%s'", orgHandle))
		return nil, unauthorizedError()
	}
	if clientApp != constants.CONSOLE_APP {
		logger.Debug(fmt.Sprintf("Opaque token is allowed only for console app, "+
			"but token is for client_id: '%s' for organization: '%s'", clientApp, orgHandle))
		return nil, unauthorizedError()
	}

	// Return introspection claims (no org info, but that's expected for opaque tokens)
	return introspectionClaims, nil
}

// IsJWT is a simple check to determine if the token is a JWT based on its structure
func IsJWT(tokenString string) bool {
	_, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	return err == nil
}

// ParseJWTClaims parses claims from a JWT without verifying the signature
func ParseJWTClaims(tokenString string) (map[string]interface{}, error) {

	logger := log.GetLogger()
	claims := jwt.MapClaims{}
	_, _, err := new(jwt.Parser).ParseUnverified(tokenString, claims)
	if err != nil {
		errMsg := "Error occurred when parsing claims from JWT token."
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.PARSING_ERROR.Code,
			Message:     errors2.PARSING_ERROR.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	return claims, nil
}

// validateClaims ensures the token has `active: true` and the expected audience and org_handle
func validateClaims(orgHandle string, claims map[string]interface{}) bool {

	logger := log.GetLogger()
	orgHandleInClaimRaw, ok := claims[constants.OrgHandleClaim]
	if !ok || orgHandleInClaimRaw != orgHandle {
		logger.Debug("Token does not have the expected org_handle claim.")
		return false
	}
	orgHandleInClaim, ok := orgHandleInClaimRaw.(string)
	if !ok || orgHandleInClaim != orgHandle {
		logger.Debug("Token org_handle claim is not valid.")
		return false
	}

	expRaw, ok := claims[constants.ExpiryClaim]
	if !ok {
		logger.Debug("Token does not have an expiration time.")
		return false
	}
	expFloat, ok := expRaw.(float64)
	if !ok {
		logger.Debug("Token does not have a valid expiration time.", log.Any("exp", expRaw))
		return false
	}
	expUnix := int64(expFloat)
	currentTime := time.Now().Unix()
	if expUnix < currentTime {
		logger.Debug("Token has expired.", log.String("exp", time.Unix(expUnix, 0).String()))
		return false
	}

	audRaw, ok := claims[constants.AudienceClaim]
	if !ok {
		logger.Debug("Token does not have an audience claim.")
		return false
	}

	var audList []string
	switch aud := audRaw.(type) {
	case []interface{}:
		for _, a := range aud {
			if s, ok := a.(string); ok {
				audList = append(audList, s)
			}
		}
	case string:
		audList = append(audList, aud)
	}

	for _, aud := range audList {
		if aud == expectedAudience {
			return true
		}
	}
	logger.Debug(fmt.Sprintf("Token audience does not match expected audience '%s'.", expectedAudience))
	return false
}

func unauthorizedError() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.UN_AUTHORIZED.Code,
		Message:     errors2.UN_AUTHORIZED.Message,
		Description: errors2.UN_AUTHORIZED.Description,
	}, http.StatusUnauthorized)
}
