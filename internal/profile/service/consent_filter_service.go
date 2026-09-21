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

package service

import (
	"context"
	"fmt"
	"strings"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// FilterProfileByConsent returns a ProfileResponse filtered to only the attributes
// the profile owner has consented to across all requested consent categories.
//
// Mandatory categories (e.g. identity-data) are always merged into the allowed set
// regardless of whether the caller listed them in consentCategoryIds.
// If consentCategoryIds is empty, only mandatory identity fields are returned.
func FilterProfileByConsent(ctx context.Context, response model.ProfileResponse, profileId string, orgHandle string, consentIds []string) (model.ProfileResponse, error) {

	filtered := model.ProfileResponse{
		ProfileId:  response.ProfileId,
		UserId:     response.UserId,
		Meta:       response.Meta,
		MergedTo:   response.MergedTo,
		MergedFrom: response.MergedFrom,
	}

	// Always include mandatory categories in the allowed set regardless of what
	// the caller requested.  This ensures identity attributes are never stripped.
	mandatoryIds, err := consentStore.GetMandatoryConsentCategoryIds(ctx, orgHandle)
	if err != nil {
		return filtered, err
	}
	seen := make(map[string]bool, len(consentIds)+len(mandatoryIds))
	merged := make([]string, 0, len(consentIds)+len(mandatoryIds))
	for _, id := range consentIds {
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	for _, id := range mandatoryIds {
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	if len(merged) == 0 {
		return filtered, nil
	}

	// Fetch attributes for categories the profile has actively consented to (mandatory categories always included).
	attrsByCategory, err := consentStore.GetConsentedCategoryAttributesByProfileId(ctx, profileId, orgHandle, merged)
	if err != nil {
		return filtered, err
	}

	// Build per-category attribute key sets, then compute the union.
	// Trait / identity-attribute keys:  "<scope>::<topLevelAttr>"
	// App-data keys (specific attr):    "<scope>::<appId>::<attrKey>"
	// App-data keys (whole bucket):     "<scope>::<appId>"
	categorySets := make([]map[string]bool, 0, len(merged))
	for _, id := range merged {
		attrs, ok := attrsByCategory[id]
		if !ok {
			// Profile has not consented to this category — skip it (union: contributes nothing).
			continue
		}
		set := make(map[string]bool, len(attrs))
		for _, attr := range attrs {
			set[attributeKey(attr)] = true
		}
		categorySets = append(categorySets, set)
	}

	allowed := unionSets(categorySets)
	if len(allowed) == 0 {
		return filtered, nil
	}

	// Filter identityAttributes
	if len(response.IdentityAttributes) > 0 {
		ia := make(map[string]interface{})
		for k, v := range response.IdentityAttributes {
			if allowed[fmt.Sprintf("%s::%s", constants.IdentityAttributes, k)] {
				ia[k] = v
			}
		}
		if len(ia) > 0 {
			filtered.IdentityAttributes = ia
		}
	}

	// Filter traits
	if len(response.Traits) > 0 {
		tr := make(map[string]interface{})
		for k, v := range response.Traits {
			if allowed[fmt.Sprintf("%s::%s", constants.Traits, k)] {
				tr[k] = v
			}
		}
		if len(tr) > 0 {
			filtered.Traits = tr
		}
	}

	// Filter applicationData.
	//
	// The profile stores app data keyed by the real app client ID (outer key).
	// Consent attributes use the logical data-group name as app_id — which corresponds
	// to the INNER key of the app's data map, not the outer client ID.
	//
	// Pre-build the set of allowed inner keys so we can check in O(1) per attribute:
	//   "application_data::events::event_name" → allowedInnerKeys["events"] = true
	//   "application_data::engagement_scores"  → allowedInnerKeys["engagement_scores"] = true
	if len(response.ApplicationData) > 0 {
		prefix := constants.ApplicationData + "::"
		allowedInnerKeys := make(map[string]bool)
		for key := range allowed {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := key[len(prefix):]
			if idx := strings.Index(rest, "::"); idx >= 0 {
				allowedInnerKeys[rest[:idx]] = true
			} else {
				allowedInnerKeys[rest] = true
			}
		}

		appData := make(map[string]map[string]interface{})
		for clientId, attrs := range response.ApplicationData {
			filteredAttrs := make(map[string]interface{})
			for innerKey, v := range attrs {
				if allowedInnerKeys[innerKey] {
					filteredAttrs[innerKey] = v
				}
			}
			if len(filteredAttrs) > 0 {
				appData[clientId] = filteredAttrs
			}
		}
		if len(appData) > 0 {
			filtered.ApplicationData = appData
		}
	}

	return filtered, nil
}

// attributeKey returns the canonical string key for a ConsentAttribute used for
// allowed-set lookups.  Scope is always derived from the attribute_name prefix so
// the key is correct regardless of whether the DB row has a stale Scope value.
//
// Key formats:
//
//	traits / identity_attributes:  "<scope>::<topLevelAttr>"
//	    e.g. "traits.product_interests.api-platform.score" → "traits::product_interests"
//
//	application_data (specific attr): "<scope>::<innerKey>::<attr>"
//	    e.g. "application_data.events.event_name" (appId="events") → "application_data::events::event_name"
//	    (appId == the inner data-group key in the profile's app_specific_data)
//
//	application_data (whole group):   "<scope>::<innerKey>"
//	    e.g. "application_data.engagement_scores" (appId="engagement_scores") → "application_data::engagement_scores"
func attributeKey(attr consentModel.ConsentAttribute) string {
	scope := scopeFromName(attr.AttributeName)
	if scope == constants.ApplicationData {
		// Strip "application_data.<appId>." to get the attribute key within the bucket.
		prefix := constants.ApplicationData + "." + attr.ApplicationIdentifier + "."
		if strings.HasPrefix(attr.AttributeName, prefix) {
			rest := attr.AttributeName[len(prefix):]
			if idx := strings.Index(rest, "."); idx >= 0 {
				rest = rest[:idx]
			}
			return fmt.Sprintf("%s::%s::%s", scope, attr.ApplicationIdentifier, rest)
		}
		// No sub-attribute: consent covers the entire app bucket.
		return fmt.Sprintf("%s::%s", scope, attr.ApplicationIdentifier)
	}
	return fmt.Sprintf("%s::%s", scope, topLevelKey(attr.AttributeName))
}

// scopeFromName derives the internal scope name from the attribute_name prefix.
// "traits.*"              → constants.Traits             ("traits")
// "identity_attributes.*" → constants.IdentityAttributes ("identity_attributes")
// "application_data.*"    → constants.ApplicationData    ("application_data")
func scopeFromName(attributeName string) string {
	switch {
	case strings.HasPrefix(attributeName, constants.Traits+"."):
		return constants.Traits
	case strings.HasPrefix(attributeName, constants.IdentityAttributes+"."):
		return constants.IdentityAttributes
	case strings.HasPrefix(attributeName, constants.ApplicationData+"."):
		return constants.ApplicationData
	default:
		return attributeName
	}
}

// topLevelKey extracts the first path segment after the scope prefix.
// Used for traits and identity_attributes only (application_data has its own logic).
// "traits.product_interests.api-platform.score" → "product_interests"
// "identity_attributes.emails.work"             → "emails"
// "traits.engagement_score"                     → "engagement_score"
func topLevelKey(attributeName string) string {
	idx := strings.Index(attributeName, ".")
	if idx < 0 {
		return attributeName
	}
	rest := attributeName[idx+1:]
	if idx2 := strings.Index(rest, "."); idx2 >= 0 {
		return rest[:idx2]
	}
	return rest
}

// unionSets returns the union of all provided sets.
func unionSets(sets []map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for _, s := range sets {
		for k := range s {
			result[k] = true
		}
	}
	return result
}
