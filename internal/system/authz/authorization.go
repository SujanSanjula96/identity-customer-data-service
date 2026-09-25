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

package authz

import (
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"

	"slices"
	"strings"
)

// ValidatePermission checks if the provided scopes match the expected scopes for a use case
func ValidatePermission(scopeStr string, operation string) bool {

	logger := log.GetLogger()
	if scopeStr == "" {
		logger.Debug(fmt.Sprintf("No scopes provided for operation: %s", operation))
		return false
	}

	logger.Info(fmt.Sprintf("Validating scopes for operation: %s with scopes: %s", operation, scopeStr))
	requiredScopes := config.GetCDSRuntime().Config.AuthServer.RequiredScopes
	if requiredScopes == nil {
		logger.Debug(fmt.Sprintf("No scopes available for operation: %s", operation))
		return false
	}

	grantedScopes := strings.Split(scopeStr, " ")
	logger.Debug(fmt.Sprintf("granted scopes: %s", grantedScopes))
	expectedScopes, err := requiredScopes[operation]
	logger.Debug(fmt.Sprintf("expected scopes: %s", expectedScopes))
	if !err {
		return false
	}

	orgScopePrefix := config.GetCDSRuntime().Config.AuthServer.OrgScopePrefix
	for _, expected := range expectedScopes {
		if !slices.Contains(grantedScopes, expected) && !slices.Contains(grantedScopes,
			orgScopeOf(expected, orgScopePrefix)) {
			return false
		}
	}
	return true
}

// orgScopeOf returns the name of the scope in a token of a sub organization, or "" when no
// prefix mapping applies.
func orgScopeOf(scope string, prefixes map[string]string) string {

	for from, to := range prefixes {
		if from != "" && strings.HasPrefix(scope, from) {
			return to + strings.TrimPrefix(scope, from)
		}
	}
	return ""
}
