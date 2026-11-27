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

// Package authn validates the bearer tokens of incoming requests against the identity server.
package authn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

var (
	expectedAudience = "iam-cds"

	// signingKeys holds the keys of the JWKS endpoints, so the signature of a JWT is checked
	// without a call to the identity provider.
	signingKeys = newJWKSCache()

	// allowedSigningMethods are the asymmetric algorithms a JWT may be signed with. A token
	// that is not signed, or signed with a shared secret, is refused before any key is read.
	allowedSigningMethods = []string{
		"RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
	}
)

// ValidateAuthenticationAndReturnClaims validates Authorization: Bearer token from the HTTP request
func ValidateAuthenticationAndReturnClaims(token, orgHandle string) (map[string]interface{}, error) {
	return ValidateTokenForOrg(token, orgHandle, orgHandle, "")
}

// ValidateTokenForOrg validates the token for the organization of the path, on every request. When
// the identity provider publishes its signing keys (idp.SigningKeyPublisher), the signature of a
// JWT is checked with the cached keys of the primary organization primaryHandle. Then the token is
// introspected at the primary organization. For a sub organization, the org_id claim must match
// pathOrgID. For a primary organization, the org_handle claim must match the path handle.
func ValidateTokenForOrg(token, orgHandle, primaryHandle, pathOrgID string) (map[string]interface{}, error) {
	logger := log.GetLogger()

	if IsJWT(token) {
		logger.Debug(fmt.Sprintf("Token is identified as JWT. Validating JWT for organization: '%s'", orgHandle))

		claims, err := jwtClaims(token, primaryHandle)
		if err != nil {
			logger.Debug(fmt.Sprintf("Failed to read a valid JWT for organization: '%s'", orgHandle),
				log.Error(err))
			return nil, unauthorizedError()
		}

		// Validate org claims against request org handle
		if !validateClaims(orgHandle, pathOrgID, claims) {
			logger.Debug("JWT claims validation failed")
			return nil, unauthorizedError()
		}

		// Introspect to check if token is still active (not revoked)
		introspectionClaims, err := introspect(token, orgHandle, primaryHandle)
		if err != nil {
			logger.Error(fmt.Sprintf("JWT token introspection failed for organization: '%s'", orgHandle),
				log.Error(err))
			return nil, unauthorizedError()
		}

		active, ok := introspectionClaims[constants.ActiveClaim].(bool)
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

	introspectionClaims, err := introspect(token, orgHandle, primaryHandle)
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

	// The token must be issued for the organization of the path.
	if pathOrgID != "" {
		if tokenOrgID, _ := introspectionClaims[constants.OrgIDClaim].(string); tokenOrgID != pathOrgID {
			logger.Debug(fmt.Sprintf("Opaque token is for organization '%s', not for '%s'", tokenOrgID, pathOrgID))
			return nil, unauthorizedError()
		}
	} else if tokenOrg, ok := introspectionClaims[constants.OrgHandleClaim].(string); ok && tokenOrg != "" &&
		tokenOrg != orgHandle {
		logger.Debug(fmt.Sprintf("Opaque token is for organization '%s', not for '%s'", tokenOrg, orgHandle))
		return nil, unauthorizedError()
	}

	clientApp, ok := introspectionClaims[constants.ClientIDClaim].(string)
	if !ok || clientApp == "" {
		logger.Debug(fmt.Sprintf("Introspected token does not have a valid client_id claim for "+
			"organization: '%s'", orgHandle))
		return nil, unauthorizedError()
	}
	if clientApp != constants.ConsoleApp {
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

// jwtClaims returns the claims of the JWT. When the identity provider publishes its signing keys,
// the signature is checked first, so a forged token never reaches introspection. Otherwise the
// claims are read without a check, and introspection alone decides whether the token is valid.
func jwtClaims(token, primaryHandle string) (map[string]interface{}, error) {
	publisher, ok := idp.GetAdapter().(idp.SigningKeyPublisher)
	if !ok {
		return ParseJWTClaims(token)
	}
	return verifyJWT(publisher, token, primaryHandle)
}

// verifyJWT checks the signature of the JWT with the key its kid names, from the JWKS endpoint of
// the primary organization, and returns its claims. Only the signature is checked here.
// validateClaims checks the claims.
func verifyJWT(publisher idp.SigningKeyPublisher, token, primaryHandle string) (map[string]interface{}, error) {
	endpoint, err := publisher.JWKSEndpoint(primaryHandle)
	if err != nil {
		return nil, err
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods(allowedSigningMethods), jwt.WithoutClaimsValidation())
	_, err = parser.ParseWithClaims(token, claims, func(parsed *jwt.Token) (interface{}, error) {
		kid, _ := parsed.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("the token header has no kid")
		}
		return signingKeys.key(endpoint, kid, parsed.Method.Alg(), publisher.FetchJWKS)
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
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
			Code:        errors2.ParsingError.Code,
			Message:     errors2.ParsingError.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	return claims, nil
}

// validateClaims checks the organization, the expiry, and the audience of the token.
func validateClaims(orgHandle, pathOrgID string, claims map[string]interface{}) bool {
	logger := log.GetLogger()
	if pathOrgID != "" {
		if orgIDInClaim, _ := claims[constants.OrgIDClaim].(string); orgIDInClaim != pathOrgID {
			logger.Debug("Token does not have the org_id claim of the path.")
			return false
		}
	} else if orgHandleInClaim, _ := claims[constants.OrgHandleClaim].(string); orgHandleInClaim != orgHandle {
		logger.Debug("Token does not have the expected org_handle claim.")
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

	// A token is not used before its not-before time, allowing for clock skew.
	if nbfRaw, ok := claims[constants.NotBeforeClaim]; ok {
		nbfFloat, ok := nbfRaw.(float64)
		if !ok {
			logger.Debug("Token does not have a valid not-before time.", log.Any("nbf", nbfRaw))
			return false
		}
		if time.Unix(int64(nbfFloat), 0).After(time.Now().Add(constants.JWTClockSkew)) {
			logger.Debug("Token is not valid yet.", log.String("nbf", time.Unix(int64(nbfFloat), 0).String()))
			return false
		}
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
		Code:        errors2.Unauthorized.Code,
		Message:     errors2.Unauthorized.Message,
		Description: errors2.Unauthorized.Description,
	}, http.StatusUnauthorized)
}

// IntrospectToken asks the identity provider about the token of a request in the
// organization orgHandle, whose primary organization is primaryHandle, the same way
// as the authentication of the request does.
func IntrospectToken(token, orgHandle, primaryHandle string) (map[string]interface{}, error) {
	return introspect(token, orgHandle, primaryHandle)
}

// introspect asks the identity provider whether the token is active. The adapter
// of the identity provider introspects it when it can, as with the service
// credentials of the organization. Otherwise the configured introspection client
// introspects it for the primary organization primaryHandle.
func introspect(token, orgHandle, primaryHandle string) (map[string]interface{}, error) {
	if introspector, ok := idp.GetAdapter().(idp.TokenIntrospector); ok {
		return introspector.IntrospectToken(context.Background(), token, orgHandle, primaryHandle)
	}
	return introspectionClient().IntrospectToken(token, primaryHandle)
}

// fallbackClient is the identity client that introspects tokens when the adapter of the identity
// provider does not. It is built once for each runtime configuration, so a request does not build
// an HTTP client and open a new connection.
var fallbackClient struct {
	mu      sync.Mutex
	runtime *config.CDSRuntime
	client  *client.IdentityClient
}

func introspectionClient() *client.IdentityClient {
	fallbackClient.mu.Lock()
	defer fallbackClient.mu.Unlock()
	if current := config.GetCDSRuntime(); fallbackClient.client == nil || fallbackClient.runtime != current {
		fallbackClient.client = client.NewIdentityClient(current.Config)
		fallbackClient.runtime = current
	}
	return fallbackClient.client
}
