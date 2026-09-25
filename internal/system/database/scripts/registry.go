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

package scripts

import "github.com/wso2/identity-customer-data-service/internal/system/database/model"

// AllQueries is every statement declared in queries.go, keyed by its Go name,
// so the tests can check each one without executing it.
//
// The list is maintained by hand: add every new statement, or it goes
// unchecked.
func AllQueries() map[string]model.DBQuery {

	return map[string]model.DBQuery{
		"UpsertApplication":                           UpsertApplication,
		"GetAppIdentifierByClientID":                  GetAppIdentifierByClientID,
		"DeleteProfileSchemaForOrg":                   DeleteProfileSchemaForOrg,
		"GetProfileSchemaByOrg":                       GetProfileSchemaByOrg,
		"DeleteIdentityClaimsOfProfileSchema":         DeleteIdentityClaimsOfProfileSchema,
		"GetProfileSchemaAttributeByName":             GetProfileSchemaAttributeByName,
		"GetProfileSchemaAttributeByScope":            GetProfileSchemaAttributeByScope,
		"UpdateProfileSchemaAttributesForSchema":      UpdateProfileSchemaAttributesForSchema,
		"DeleteProfileSchemaAttributeForScope":        DeleteProfileSchemaAttributeForScope,
		"GetProfileSchemaAttributeById":               GetProfileSchemaAttributeById,
		"FilterProfileSchemaAttributes":               FilterProfileSchemaAttributes,
		"DeleteProfileSchemaAttributeById":            DeleteProfileSchemaAttributeById,
		"GetUnificationRules":                         GetUnificationRules,
		"GetUnificationRule":                          GetUnificationRule,
		"DeleteUnificationRule":                       DeleteUnificationRule,
		"InsertUnificationRule":                       InsertUnificationRule,
		"UpdateUnificationRule":                       UpdateUnificationRule,
		"InsertProfile":                               InsertProfile,
		"InsertProfileReference":                      InsertProfileReference,
		"GetProfileById":                              GetProfileById,
		"GetProfileConsentsByProfileId":               GetProfileConsentsByProfileId,
		"DeleteProfileConsentsByProfileId":            DeleteProfileConsentsByProfileId,
		"InsertProfileConsentsByProfileId":            InsertProfileConsentsByProfileId,
		"GetAppDataByProfileId":                       GetAppDataByProfileId,
		"GetAppDataByAppId":                           GetAppDataByAppId,
		"UpdateProfile":                               UpdateProfile,
		"UpsertProfileReference":                      UpsertProfileReference,
		"UpdateProfileReference":                      UpdateProfileReference,
		"GetProfilesByOrgId":                          GetProfilesByOrgId,
		"DeleteProfileByProfileId":                    DeleteProfileByProfileId,
		"DeleteProfile":                               DeleteProfile,
		"InsertApplicationData":                       InsertApplicationData,
		"DeleteProfileReference":                      DeleteProfileReference,
		"GetAllProfilesWithFilterBase":                GetAllProfilesWithFilterBase,
		"GetAllReferenceProfileExceptCurrent":         GetAllReferenceProfileExceptCurrent,
		"FetchReferencedProfiles":                     FetchReferencedProfiles,
		"GetProfileByUserId":                          GetProfileByUserId,
		"InsertConsentCategory":                       InsertConsentCategory,
		"UpsertDefaultIdentityDataCategory":           UpsertDefaultIdentityDataCategory,
		"GetAllConsentCategories":                     GetAllConsentCategories,
		"GetConsentCategoryById":                      GetConsentCategoryById,
		"GetConsentCategoryByName":                    GetConsentCategoryByName,
		"GetMandatoryConsentCategoryIdsByOrg":         GetMandatoryConsentCategoryIdsByOrg,
		"UpdateConsentCategory":                       UpdateConsentCategory,
		"DeleteConsentCategory":                       DeleteConsentCategory,
		"InsertConsentCategoryAttribute":              InsertConsentCategoryAttribute,
		"GetConsentCategoryAttributesByCategoryId":    GetConsentCategoryAttributesByCategoryId,
		"DeleteConsentCategoryAttributesByCategoryId": DeleteConsentCategoryAttributesByCategoryId,
		"InsertCookie":                                InsertCookie,
		"GetCookieByCookieId":                         GetCookieByCookieId,
		"GetCookieByProfileId":                        GetCookieByProfileId,
		"UpdateCookieStatusByProfileId":               UpdateCookieStatusByProfileId,
		"UpdateCookieStatusByCookieId":                UpdateCookieStatusByCookieId,
		"DeleteCookieById":                            DeleteCookieById,
		"DeleteCookieByProfileId":                     DeleteCookieByProfileId,
		"GetOrgConfigurations":                        GetOrgConfigurations,
		"UpdateOrgConfiguration":                      UpdateOrgConfiguration,
		"GetOrgConfiguration":                         GetOrgConfiguration,
		"UpdateInitialSchemaSyncDoneConfig":           UpdateInitialSchemaSyncDoneConfig,
		"HealthCheckPing":                             HealthCheckPing,

		// B2B statements.
		"UpsertOrganization":              UpsertOrganization,
		"GetOrganizationById":             GetOrganizationById,
		"GetOrganizationByHandle":         GetOrganizationByHandle,
		"GetOrganizationsByRoot":          GetOrganizationsByRoot,
		"UpdateOrganizationStatus":        UpdateOrganizationStatus,
		"GetEnabledRootOrgHandles":        GetEnabledRootOrgHandles,
		"InsertSharePolicy":               InsertSharePolicy,
		"TouchSharePolicy":                TouchSharePolicy,
		"GetSharePolicy":                  GetSharePolicy,
		"GetSharePoliciesByRoot":          GetSharePoliciesByRoot,
		"GetSharePolicyTargetsByRoot":     GetSharePolicyTargetsByRoot,
		"GetSharePolicyExclusionsByRoot":  GetSharePolicyExclusionsByRoot,
		"InsertSharePolicyTarget":         InsertSharePolicyTarget,
		"InsertSharePolicyExclusion":      InsertSharePolicyExclusion,
		"DeleteSharePolicyTargets":        DeleteSharePolicyTargets,
		"DeleteSharePolicyExclusions":     DeleteSharePolicyExclusions,
		"DeleteSharePolicy":               DeleteSharePolicy,
		"GetSharePolicyIdsByResource":     GetSharePolicyIdsByResource,
		"DeleteShareStatesByRoot":         DeleteShareStatesByRoot,
		"InsertShareState":                InsertShareState,
		"GetShareStatesByResource":        GetShareStatesByResource,
		"GetShareStatesForOrg":            GetShareStatesForOrg,
		"GetSharedSchemaAttributesForOrg": GetSharedSchemaAttributesForOrg,
		"GetSharedUnificationRulesForOrg": GetSharedUnificationRulesForOrg,
		"GetSchemaAttributesByRoot":       GetSchemaAttributesByRoot,
		"GetUnificationRulesByRoot":       GetUnificationRulesByRoot,
		"DeleteShareStatesByResource":     DeleteShareStatesByResource,

		// Templates the stores complete at runtime.
		"InsertIdentityClaimsForProfileSchema":      InsertIdentityClaimsForProfileSchema,
		"UpsertIdentityClaimsForProfileSchema":      UpsertIdentityClaimsForProfileSchema,
		"InsertProfileSchemaAttributesForScope":     InsertProfileSchemaAttributesForScope,
		"UpdateProfileSchemaAttributeFields":        UpdateProfileSchemaAttributeFields,
		"DeleteStaleIdentityClaimsForProfileSchema": DeleteStaleIdentityClaimsForProfileSchema,
		"GetAppDataByProfileIds":                    GetAppDataByProfileIds,
		"GetConsentCategoryAttributesByCategoryIds": GetConsentCategoryAttributesByCategoryIds,
		"DeleteInactiveCookies":                     DeleteInactiveCookies,
	}
}
