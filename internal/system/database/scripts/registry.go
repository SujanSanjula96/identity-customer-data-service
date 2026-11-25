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
		"UpsertApplication":                      UpsertApplication,
		"GetAppIdentifierByClientID":             GetAppIdentifierByClientID,
		"DeleteProfileSchemaForOrg":              DeleteProfileSchemaForOrg,
		"GetProfileSchemaByOrg":                  GetProfileSchemaByOrg,
		"DeleteIdentityClaimsOfProfileSchema":    DeleteIdentityClaimsOfProfileSchema,
		"GetProfileSchemaAttributeByName":        GetProfileSchemaAttributeByName,
		"GetProfileSchemaAttributeByScope":       GetProfileSchemaAttributeByScope,
		"UpdateProfileSchemaAttributesForSchema": UpdateProfileSchemaAttributesForSchema,
		"DeleteProfileSchemaAttributeForScope":   DeleteProfileSchemaAttributeForScope,
		"GetProfileSchemaAttributeByID":          GetProfileSchemaAttributeByID,
		"FilterProfileSchemaAttributes":          FilterProfileSchemaAttributes,
		"DeleteProfileSchemaAttributeByID":       DeleteProfileSchemaAttributeByID,
		"GetUnificationRules":                    GetUnificationRules,
		"GetUnificationRule":                     GetUnificationRule,
		"DeleteUnificationRule":                  DeleteUnificationRule,
		"InsertUnificationRule":                  InsertUnificationRule,
		"UpdateUnificationRule":                  UpdateUnificationRule,
		"InsertProfile":                          InsertProfile,
		"InsertProfileReference":                 InsertProfileReference,
		"GetProfileByID":                         GetProfileByID,
		"GetProfileConsentsByProfileID":          GetProfileConsentsByProfileID,
		"DeleteProfileConsentsByProfileID":       DeleteProfileConsentsByProfileID,
		"InsertProfileConsentsByProfileID":       InsertProfileConsentsByProfileID,
		"GetAppDataByProfileID":                  GetAppDataByProfileID,
		"GetAppDataByAppID":                      GetAppDataByAppID,
		"UpdateProfile":                          UpdateProfile,
		"UpsertProfileReference":                 UpsertProfileReference,
		"UpdateProfileReference":                 UpdateProfileReference,
		"GetProfilesByOrgID":                     GetProfilesByOrgID,
		"DeleteProfileByProfileID":               DeleteProfileByProfileID,
		"DeleteProfile":                          DeleteProfile,
		"InsertApplicationData":                  InsertApplicationData,
		"DeleteProfileReference":                 DeleteProfileReference,
		"GetAllProfilesWithFilterBase":           GetAllProfilesWithFilterBase,
		"FetchReferencedProfiles":                FetchReferencedProfiles,
		"GetProfileByUserID":                     GetProfileByUserID,

		"FindOldestReferenceProfileIDByUserID":          FindOldestReferenceProfileIDByUserID,
		"FindOldestReferenceProfileIDByAttributeValues": FindOldestReferenceProfileIDByAttributeValues,

		"InsertConsentCategory":                       InsertConsentCategory,
		"UpsertDefaultIdentityDataCategory":           UpsertDefaultIdentityDataCategory,
		"GetAllConsentCategories":                     GetAllConsentCategories,
		"GetConsentCategoryByID":                      GetConsentCategoryByID,
		"GetConsentCategoryByName":                    GetConsentCategoryByName,
		"GetMandatoryConsentCategoryIDsByOrg":         GetMandatoryConsentCategoryIDsByOrg,
		"GetConsentCategoryIDsByOrg":                  GetConsentCategoryIDsByOrg,
		"UpdateConsentCategory":                       UpdateConsentCategory,
		"DeleteConsentCategory":                       DeleteConsentCategory,
		"InsertConsentCategoryAttribute":              InsertConsentCategoryAttribute,
		"GetConsentCategoryAttributesByCategoryID":    GetConsentCategoryAttributesByCategoryID,
		"DeleteConsentCategoryAttributesByCategoryID": DeleteConsentCategoryAttributesByCategoryID,
		"InsertCookie":                                InsertCookie,
		"GetCookieByCookieID":                         GetCookieByCookieID,
		"GetCookieByProfileID":                        GetCookieByProfileID,
		"UpdateCookieStatusByProfileID":               UpdateCookieStatusByProfileID,
		"UpdateCookieStatusByCookieID":                UpdateCookieStatusByCookieID,
		"DeleteCookieByID":                            DeleteCookieByID,
		"DeleteCookieByProfileID":                     DeleteCookieByProfileID,
		"ListExpiredTemporaryProfiles":                ListExpiredTemporaryProfiles,
		"DeleteProfilesByIDs":                         DeleteProfilesByIDs,
		"LockProfileReference":                        LockProfileReference,
		"DeleteReviewTasksByCandidateProfileIDs":      DeleteReviewTasksByCandidateProfileIDs,
		"DeleteProfileReferencesByProfileIDs":         DeleteProfileReferencesByProfileIDs,
		"ListProfileOrgIDs":                           ListProfileOrgIDs,
		"IRDeleteRejectionPairsForProfiles":           IRDeleteRejectionPairsForProfiles,
		"GetOrgConfigurations":                        GetOrgConfigurations,
		"UpdateOrgConfiguration":                      UpdateOrgConfiguration,
		"ListOrgConfigurationValues":                  ListOrgConfigurationValues,
		"GetOrgConfiguration":                         GetOrgConfiguration,
		"UpdateInitialSchemaSyncDoneConfig":           UpdateInitialSchemaSyncDoneConfig,
		"GetOrgServiceCredentials":                    GetOrgServiceCredentials,
		"UpsertOrgServiceCredentials":                 UpsertOrgServiceCredentials,
		"CheckOrgServiceCredentials":                  CheckOrgServiceCredentials,
		"HealthCheckPing":                             HealthCheckPing,

		// Organization management statements.
		"UpsertOrganization":           UpsertOrganization,
		"GetOrganizationByID":          GetOrganizationByID,
		"GetOrganizationByHandle":      GetOrganizationByHandle,
		"GetOrganizationsByPrimaryOrg": GetOrganizationsByPrimaryOrg,
		"UpdateOrganizationStatus":     UpdateOrganizationStatus,
		"GetOrganizationChain":         GetOrganizationChain,
		"GetOrganizationAncestors":     GetOrganizationAncestors,
		"ListOrganizationPrimaries":    ListOrganizationPrimaries,
		"InsertSharePolicy":            InsertSharePolicy,
		"GetSharePolicyByID":           GetSharePolicyByID,
		"GetSharePolicy":               GetSharePolicy,
		"GetSharePolicyTargets":        GetSharePolicyTargets,
		"DeleteSharePolicyTargets":     DeleteSharePolicyTargets,
		"DeleteSharePolicy":            DeleteSharePolicy,
		"GetSharePolicyIDsByResource":  GetSharePolicyIDsByResource,

		// Templates the stores complete at runtime.
		"InsertIdentityClaimsForProfileSchema":      InsertIdentityClaimsForProfileSchema,
		"UpsertIdentityClaimsForProfileSchema":      UpsertIdentityClaimsForProfileSchema,
		"InsertProfileSchemaAttributesForScope":     InsertProfileSchemaAttributesForScope,
		"DeleteStaleIdentityClaimsForProfileSchema": DeleteStaleIdentityClaimsForProfileSchema,
		"GetAppDataByProfileIDs":                    GetAppDataByProfileIDs,
		"GetConsentCategoryAttributesByCategoryIDs": GetConsentCategoryAttributesByCategoryIDs,
		"GetProfileConsentsByProfileIDs":            GetProfileConsentsByProfileIDs,
		"DeleteInactiveCookies":                     DeleteInactiveCookies,
		"GetOrganizationsByIDs":                     GetOrganizationsByIDs,
		"InsertSharePolicyTargets":                  InsertSharePolicyTargets,
		"GetSharePoliciesByOwners":                  GetSharePoliciesByOwners,
		"GetProfileSchemaAttributesByIDs":           GetProfileSchemaAttributesByIDs,
		"GetUnificationRulesByIDs":                  GetUnificationRulesByIDs,

		// Identity resolution.
		"DeleteBlockingKeysByAttributeSQL":   DeleteBlockingKeysByAttributeSQL,
		"DeleteBlockingKeysSQL":              DeleteBlockingKeysSQL,
		"GetProfileIDsWithFiltersBase":       GetProfileIDsWithFiltersBase,
		"IRCancelRelatedReviewTasks":         IRCancelRelatedReviewTasks,
		"IRCountPendingReviewTasks":          IRCountPendingReviewTasks,
		"IRCountPendingReviewTasksByProfile": IRCountPendingReviewTasksByProfile,
		"IRCountProfilesByBlockingKey":       IRCountProfilesByBlockingKey,
		"IRDeleteRejectionPairsForProfile":   IRDeleteRejectionPairsForProfile,
		"IRFindCandidateIDsByKeys":           IRFindCandidateIDsByKeys,
		"IRFindRelatedPendingReviewTasks":    IRFindRelatedPendingReviewTasks,
		"IRGetPendingReviewTasks":            IRGetPendingReviewTasks,
		"IRGetPendingReviewTasksByProfile":   IRGetPendingReviewTasksByProfile,
		"IRGetProfilesByIDs":                 IRGetProfilesByIDs,
		"IRGetProfilesForOrgAfter":           IRGetProfilesForOrgAfter,
		"IRGetRejectionsForProfile":          IRGetRejectionsForProfile,
		"IRGetReviewTaskByID":                IRGetReviewTaskByID,
		"IRInsertBlockingKeys":               IRInsertBlockingKeys,
		"IRInsertRejectionPair":              IRInsertRejectionPair,
		"IRInsertReviewTask":                 IRInsertReviewTask,
		"IRMirrorReviewTaskExists":           IRMirrorReviewTaskExists,
		"IRRepointRejectionPairs":            IRRepointRejectionPairs,
		"IRUpdateMirrorReviewTask":           IRUpdateMirrorReviewTask,
		"IRUpdateReviewTaskStatus":           IRUpdateReviewTaskStatus,
		"IRLockReviewTask":                   IRLockReviewTask,
	}
}
