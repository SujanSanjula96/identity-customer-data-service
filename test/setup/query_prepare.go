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

package setup

import (
	"fmt"
	"strings"
)

// Preparing a statement validates its grammar and the tables and columns it
// references. Two suites do it, so the helpers live here rather than in either
// of them.

// QueryPlaceholders returns "($1, $2, ... $n)", the value tuple the stores
// build for the batch inserts.
func QueryPlaceholders(n int) string {

	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// CompleteStatement returns the statement as a store executes it, since a
// template cannot be prepared. name is its name in scripts.AllQueries.
func CompleteStatement(name, statement string) string {

	switch name {
	case "FindOldestReferenceProfileIDByAttributeValues":
		return fmt.Sprintf(statement, "p.identity_attributes")
	case "InsertIdentityClaimsForProfileSchema":
		return statement + QueryPlaceholders(13)
	case "InsertProfileSchemaAttributesForScope":
		return statement + QueryPlaceholders(12)
	case "UpdateProfileSchemaAttributeFields":
		return statement + "attribute_name = $1 WHERE org_handle = $2 AND attribute_id = $3"
	case "UpsertIdentityClaimsForProfileSchema":
		return fmt.Sprintf(statement, QueryPlaceholders(13))
	case "InsertSharePolicyTargets":
		return fmt.Sprintf(statement, QueryPlaceholders(3))
	case "DeleteStaleIdentityClaimsForProfileSchema", "GetSharePoliciesByOwners":
		return fmt.Sprintf(statement, "$2")
	case "GetAppDataByProfileIds", "GetConsentCategoryAttributesByCategoryIds", "GetOrganizationsByIds",
		"GetProfileSchemaAttributesByIds", "GetUnificationRulesByIds":
		return fmt.Sprintf(statement, "$1")
	case "IRGetProfilesByIDs":
		return fmt.Sprintf(statement, "$1")
	case "IRFindCandidateIDsByKeys":
		// An IN list plus the positions of the exclude and limit arguments.
		return fmt.Sprintf(statement, "$1", 2, 3)
	case "IRInsertBlockingKeys":
		return fmt.Sprintf(statement, "($1, $2, $3, $4)")
	case "GetProfileIDsWithFiltersBase":
		return statement + " WHERE p.org_handle = $1"
	default:
		return statement
	}
}
