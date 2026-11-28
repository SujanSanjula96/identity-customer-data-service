/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

package utils

import (
	"context"
	"fmt"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// BuildProfileLocation returns the URL of a profile under the path of the organization of the request:
// /t/{org_handle}, or /t/{primary_handle}/o/{org_id} for a sub organization. Outside a request, the path is
// /t/{org_handle}.
func BuildProfileLocation(ctx context.Context, orgHandle, profileID string) string {
	basePath, ok := ctx.Value(constants.OrgBasePathContextKey).(string)
	if !ok || basePath == "" {
		basePath = "/t/" + orgHandle
	}
	serverURL := strings.TrimSuffix(config.GetCDSRuntime().Config.ServerURL, "/")
	return fmt.Sprintf("%s%s%s/v1/profiles/%s", serverURL, basePath, constants.APIBasePath, profileID)
}

// ResolveDisplayNameFromAttribute takes an attribute name (potentially in dot notation) and converts it to a
// human-readable display name. The result is guaranteed to comply with the display name character rules and is
// truncated to MaxAttributeDisplayNameLength characters.
func ResolveDisplayNameFromAttribute(attributeName string) string {
	parts := strings.Split(attributeName, ".")
	if len(parts) == 0 {
		return ""
	}

	// Take leaf attribute for display name
	leaf := parts[len(parts)-1]
	leaf = strings.NewReplacer("_", " ", "-", " ").Replace(leaf)
	leaf = constants.DisplayNameCamelCaseSplitter.ReplaceAllString(leaf, "${1} ${2}")

	// Title case each word
	words := strings.Fields(leaf)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}

	result := strings.Join(words, " ")

	// Strip any characters not allowed in display names.
	result = constants.DisplayNameRegex.ReplaceAllString(result, "")

	// Truncate to MaxAttributeDisplayNameLength characters.
	if len(result) > constants.MaxAttributeDisplayNameLength {
		result = result[:constants.MaxAttributeDisplayNameLength]
	}

	return result
}
