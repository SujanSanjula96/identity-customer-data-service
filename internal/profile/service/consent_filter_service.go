/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
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

package service

import (
	"context"
	"fmt"
	"strings"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/identitysource"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// FilterProfileByConsent returns a ProfileResponse filtered to only the attributes
// the profile owner has consented to across all requested consent categories.
//
// Mandatory categories (e.g. identity-data) are always merged into the allowed set
// regardless of whether the caller listed them in consentCategoryIds.
// If consentCategoryIds is empty, only mandatory identity fields are returned.
func FilterProfileByConsent(ctx context.Context, response model.ProfileResponse, profileID string, orgID string,
	consentIDs []string) (model.ProfileResponse, error) {
	filter, err := newConsentFilter(ctx, orgID, consentIDs, []string{profileID})
	if err != nil {
		return model.ProfileResponse{}, err
	}
	return applyConsent(response, filter.allowed(profileID)), nil
}

// FilterProfilesForCaller restricts a page of listed profiles to what a caller that is not a
// system application may read, as GetProfile does for one profile: only the caller's own
// application data, and only the attributes each profile has consented to share under the
// requested and mandatory categories.
//
// A profile is left out when a term in filters tests an attribute the caller cannot read on
// that profile, so that the listing never selects a profile by a value it would not return.
// Leaving profiles out can make a page shorter than the page size; the cursors stay on the
// profiles the store returned.
func FilterProfilesForCaller(ctx context.Context, profiles []model.ProfileResponse, orgID string,
	callerAppID string, consentIDs []string, filters []string) ([]model.ProfileResponse, error) {
	if len(profiles) == 0 {
		return profiles, nil
	}

	profileIDs := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		profileIDs = append(profileIDs, profile.ProfileID)
	}
	filter, err := newConsentFilter(ctx, orgID, consentIDs, profileIDs)
	if err != nil {
		return nil, err
	}

	result := make([]model.ProfileResponse, 0, len(profiles))
	for _, profile := range profiles {
		allowed := filter.allowed(profile.ProfileID)
		if !filtersVisible(filters, allowed, callerAppID) {
			continue
		}
		profile.ApplicationData = filterForRegularApp(profile.ApplicationData, callerAppID)
		result = append(result, applyConsent(profile, allowed))
	}
	return result, nil
}

// FilterFuzzyResultsForCaller does for fuzzy search results what FilterProfilesForCaller does
// for a page of listed profiles. filters holds every term the search ran on, fuzzy and
// deterministic, so that no result is kept for a match on a value the caller cannot read.
func FilterFuzzyResultsForCaller(ctx context.Context, results []model.FuzzyMatchResult, orgID string,
	callerAppID string, consentIDs []string, filters []string) ([]model.FuzzyMatchResult, error) {
	profiles := make([]model.ProfileResponse, 0, len(results))
	for _, result := range results {
		profiles = append(profiles, result.Profile)
	}
	visible, err := FilterProfilesForCaller(ctx, profiles, orgID, callerAppID, consentIDs, filters)
	if err != nil {
		return nil, err
	}

	visibleByID := make(map[string]model.ProfileResponse, len(visible))
	for _, profile := range visible {
		visibleByID[profile.ProfileID] = profile
	}
	kept := make([]model.FuzzyMatchResult, 0, len(visible))
	for _, result := range results {
		if profile, ok := visibleByID[result.Profile.ProfileID]; ok {
			result.Profile = profile
			kept = append(kept, result)
		}
	}
	return kept, nil
}

// ScopeFiltersToCaller rewrites each application data filter term that names no application
// (application_data.<key>) to name the caller's (application_data.<callerAppID>.<key>), so
// that the term cannot select a profile by another application's data. Terms are left as
// they are when there is no caller application; FilterProfilesForCaller then leaves out
// every profile they select.
func ScopeFiltersToCaller(filters []string, callerAppID string) []string {
	if callerAppID == "" {
		return filters
	}
	prefix := constants.ApplicationData + "."
	scoped := make([]string, 0, len(filters))
	for _, f := range filters {
		field, rest, _ := strings.Cut(f, " ")
		if key, ok := strings.CutPrefix(field, prefix); ok && !strings.Contains(key, ".") {
			f = fmt.Sprintf("%s%s.%s %s", prefix, callerAppID, key, rest)
		}
		scoped = append(scoped, f)
	}
	return scoped
}

// consentFilter holds what filtering by consent needs for one request: the attributes of the
// requested and mandatory categories, and the categories each profile consented to. It is
// built once, so filtering a page of profiles costs the same few queries as filtering one.
type consentFilter struct {
	// categoryIDs are the requested categories of the org merged with the mandatory ones.
	categoryIDs []string
	mandatory   map[string]bool
	// attributeKeys holds, per category, the keys attributeKey builds for its attributes.
	attributeKeys map[string]map[string]bool
	// consents holds, per profile, the categories it consented to.
	consents map[string]map[string]bool
}

func newConsentFilter(ctx context.Context, orgID string, consentIDs []string, profileIDs []string) (
	*consentFilter, error) {
	// Ignore requested categories that are not defined in the org.
	if len(consentIDs) > 0 {
		orgCategorySet, err := consentCategoryIDSet(ctx, orgID)
		if err != nil {
			return nil, err
		}
		orgConsentIDs := make([]string, 0, len(consentIDs))
		for _, id := range consentIDs {
			if orgCategorySet[id] {
				orgConsentIDs = append(orgConsentIDs, id)
			}
		}
		consentIDs = orgConsentIDs
	}

	// Always include mandatory categories in the allowed set regardless of what
	// the caller requested.  This ensures identity attributes are never stripped.
	// Mandatory categories belong to the organization that holds the identity attributes.
	source := identitysource.Of(ctx, orgID)
	mandatory, attrsByCategory, err := consentStore.GetConsentFilterAttributes(ctx, source, consentIDs)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(consentIDs)+len(mandatory))
	categoryIDs := make([]string, 0, len(consentIDs)+len(mandatory))
	for _, id := range consentIDs {
		if !seen[id] {
			seen[id] = true
			categoryIDs = append(categoryIDs, id)
		}
	}
	for id := range mandatory {
		if !seen[id] {
			seen[id] = true
			categoryIDs = append(categoryIDs, id)
		}
	}

	// Trait / identity-attribute keys:  "<scope>::<topLevelAttr>"
	// App-data keys (specific attr):    "<scope>::<appId>::<attrKey>"
	// App-data keys (whole bucket):     "<scope>::<appId>"
	attributeKeys := make(map[string]map[string]bool, len(attrsByCategory))
	for id, attrs := range attrsByCategory {
		keys := make(map[string]bool, len(attrs))
		for _, attr := range attrs {
			keys[attributeKey(attr)] = true
		}
		attributeKeys[id] = keys
	}

	consents := map[string]map[string]bool{}
	if len(categoryIDs) > len(mandatory) {
		consents, err = consentStore.GetProfileConsentsByProfileIDs(ctx, profileIDs)
		if err != nil {
			return nil, err
		}
	}

	return &consentFilter{
		categoryIDs:   categoryIDs,
		mandatory:     mandatory,
		attributeKeys: attributeKeys,
		consents:      consents,
	}, nil
}

// allowed returns the attribute keys the profile may disclose: the union of those of every
// mandatory category and of every requested category the profile consented to.
func (f *consentFilter) allowed(profileID string) map[string]bool {
	categorySets := make([]map[string]bool, 0, len(f.categoryIDs))
	for _, id := range f.categoryIDs {
		if !f.mandatory[id] && !f.consents[profileID][id] {
			// Profile has not consented to this category — skip it (union: contributes nothing).
			continue
		}
		if keys, ok := f.attributeKeys[id]; ok {
			categorySets = append(categorySets, keys)
		}
	}
	return unionSets(categorySets)
}

// applyConsent returns the response with only the attributes in allowed.
func applyConsent(response model.ProfileResponse, allowed map[string]bool) model.ProfileResponse {
	filtered := model.ProfileResponse{
		ProfileID:  response.ProfileID,
		UserID:     response.UserID,
		Meta:       response.Meta,
		MergedTo:   response.MergedTo,
		MergedFrom: response.MergedFrom,
	}
	if len(allowed) == 0 {
		return filtered
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
	if len(response.ApplicationData) > 0 {
		allowedInnerKeys := allowedApplicationDataKeys(allowed)

		appData := make(map[string]map[string]interface{})
		for clientID, attrs := range response.ApplicationData {
			filteredAttrs := make(map[string]interface{})
			for innerKey, v := range attrs {
				if allowedInnerKeys[innerKey] {
					filteredAttrs[innerKey] = v
				}
			}
			if len(filteredAttrs) > 0 {
				appData[clientID] = filteredAttrs
			}
		}
		if len(appData) > 0 {
			filtered.ApplicationData = appData
		}
	}

	return filtered
}

// allowedApplicationDataKeys returns the inner application data keys in allowed, so each
// can be checked in O(1):
//
//	"application_data::events::event_name" → "events"
//	"application_data::engagement_scores"  → "engagement_scores"
func allowedApplicationDataKeys(allowed map[string]bool) map[string]bool {
	prefix := constants.ApplicationData + "::"
	keys := make(map[string]bool)
	for key := range allowed {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		if idx := strings.Index(rest, "::"); idx >= 0 {
			keys[rest[:idx]] = true
		} else {
			keys[rest] = true
		}
	}
	return keys
}

// filtersVisible reports whether every term in filters tests an attribute the caller can read
// on a profile whose allowed attribute keys are allowed. A term on an unknown field is treated
// as not visible.
func filtersVisible(filters []string, allowed map[string]bool, callerAppID string) bool {
	for _, f := range filters {
		field, _, _ := strings.Cut(f, " ")
		if !filterFieldVisible(field, allowed, callerAppID) {
			return false
		}
	}
	return true
}

func filterFieldVisible(field string, allowed map[string]bool, callerAppID string) bool {
	if field == "user_id" || field == "profile_id" {
		return true
	}
	scope, key, ok := strings.Cut(field, ".")
	if !ok {
		return false
	}
	switch scope {
	case constants.IdentityAttributes, constants.Traits:
		topLevel, _, _ := strings.Cut(key, ".")
		return allowed[fmt.Sprintf("%s::%s", scope, topLevel)]
	case constants.ApplicationData:
		// A term naming no application matches any application's data; ScopeFiltersToCaller
		// has already named the caller's when there is one.
		appID, appKey, specific := strings.Cut(key, ".")
		if !specific || callerAppID == "" || appID != callerAppID {
			return false
		}
		innerKey, _, _ := strings.Cut(appKey, ".")
		return allowedApplicationDataKeys(allowed)[innerKey]
	default:
		return false
	}
}

// consentCategoryIDSet returns the IDs of all consent categories of the org.
func consentCategoryIDSet(ctx context.Context, orgID string) (map[string]bool, error) {
	ids, err := consentStore.GetConsentCategoryIDs(ctx, orgID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
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
