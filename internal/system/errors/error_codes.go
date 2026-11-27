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

package errors

const errorPrefix = "CDS-"

// The error messages of CDS. Server errors use 15xxx codes and client errors use the 1xxxx ranges below.
var (

	// Server (15xxx) sub-grouping
	//   150xx - Admin Config & Server Operations
	//   151xx - Profile Schema & Schema Sync / IS Integration
	//   152xx - Unification Rules Management
	//   153xx - Consent Management
	//   154xx - Profiles & Cookie Management
	//   159xx - Other Server Errors

	GetAdminConfig = ErrorMessage{
		Code:    errorPrefix + "15001",
		Message: "Error while fetching tenant configurations.",
	}
	UpdateAdminConfig = ErrorMessage{
		Code:    errorPrefix + "15002",
		Message: "Error while updating tenant configurations.",
	}
	EncodeError = ErrorMessage{
		Code:    errorPrefix + "15003",
		Message: "Error while encoding data.",
	}

	AddProfileSchema = ErrorMessage{
		Code:    errorPrefix + "15101",
		Message: "Error while adding profile schema.",
	}

	GetProfileSchema = ErrorMessage{
		Code:    errorPrefix + "15102",
		Message: "Error while fetching profile schema.",
	}

	UpdateProfileSchema = ErrorMessage{
		Code:    errorPrefix + "15103",
		Message: "Error while updating profile schema.",
	}

	DeleteProfileSchema = ErrorMessage{
		Code:    errorPrefix + "15104",
		Message: "Error while deleting profile schema attribute(s).",
	}

	SyncProfileSchema = ErrorMessage{
		Code:    errorPrefix + "15105",
		Message: "Error while syncing identity attributes of profile schema.",
	}

	TokenFetchFailed = ErrorMessage{
		Code:    errorPrefix + "15106",
		Message: "Fetching token failed.",
	}

	GetSCIMDialects = ErrorMessage{
		Code:    errorPrefix + "15107",
		Message: "Error while fetching SCIM dialects.",
	}

	GetDialectClaims = ErrorMessage{
		Code:    errorPrefix + "15108",
		Message: "Error while fetching dialect claims.",
	}

	GetLocalClaimsFailed = ErrorMessage{
		Code:    errorPrefix + "15109",
		Message: "Error while fetching local claims.",
	}

	GetSCIMUserFailed = ErrorMessage{
		Code:    errorPrefix + "15110",
		Message: "Error while fetching SCIM user.",
	}

	GetApplicationsFailed = ErrorMessage{
		Code:    errorPrefix + "15111",
		Message: "Error while fetching applications.",
	}

	UpsertApplicationFailed = ErrorMessage{
		Code:    errorPrefix + "15112",
		Message: "Error while persisting application information.",
	}

	GetApplicationFailed = ErrorMessage{
		Code:    errorPrefix + "15113",
		Message: "Error while resolving application information.",
	}

	GetServiceCredentialsFailed = ErrorMessage{
		Code:    errorPrefix + "15114",
		Message: "Error while reading the service credentials of the organization.",
	}

	SaveServiceCredentialsFailed = ErrorMessage{
		Code:    errorPrefix + "15115",
		Message: "Error while persisting the service credentials of the organization.",
	}

	AddUnificationRule = ErrorMessage{
		Code:    errorPrefix + "15201",
		Message: "Error while adding unification rules.",
	}

	GetUnificationRule = ErrorMessage{
		Code:    errorPrefix + "15202",
		Message: "Error while fetching unification rules.",
	}

	UpdateUnificationRule = ErrorMessage{
		Code:    errorPrefix + "15203",
		Message: "Error while updating unification rules.",
	}

	DeleteUnificationRule = ErrorMessage{
		Code:    errorPrefix + "15204",
		Message: "Error while deleting unification rule(s).",
	}

	AddConsentCategory = ErrorMessage{
		Code:    errorPrefix + "15301",
		Message: "Adding consent category failed.",
	}

	AddConsentCategoryBadRequest = ErrorMessage{
		Code:    errorPrefix + "15302",
		Message: "Invalid request payload for adding consent category.",
	}

	FetchConsentCategories = ErrorMessage{
		Code:    errorPrefix + "15303",
		Message: "Fetching consent category failed.",
	}

	UpdateConsentCategory = ErrorMessage{
		Code:    errorPrefix + "15304",
		Message: "Updating consent category failed.",
	}

	GetCookie = ErrorMessage{
		Code:    errorPrefix + "15401",
		Message: "Fetching profile cookie failed.",
	}

	UpdateCookie = ErrorMessage{
		Code:    errorPrefix + "15402",
		Message: "Updating profile cookie failed.",
	}

	DeleteCookie = ErrorMessage{
		Code:    errorPrefix + "15403",
		Message: "Updating profile cookie failed.",
	}

	GetProfileServerError = ErrorMessage{
		Code:    errorPrefix + "15404",
		Message: "Fetching profile(s) failed.",
	}

	ParsingError = ErrorMessage{
		Code:    errorPrefix + "15901",
		Message: "Parsing token failed.",
	}

	//   100xx - Auth / Access
	//   110xx - Profile & App Data & cookies
	//   120xx - Unification Rules
	//   130xx - Profile Schema
	//   140xx - Consent Management
	//   160xx - Admin Configurations
	//   190xx - Other Client Errors
	BadRequest = ErrorMessage{
		Code:    errorPrefix + "10001",
		Message: "Invalid request.",
	}

	Unauthorized = ErrorMessage{
		Code:        errorPrefix + "10002",
		Message:     "Unauthorized",
		Description: "Authorization failure. Authorization information was invalid or missing from your request.",
	}

	FORBIDDEN = ErrorMessage{
		Code:        errorPrefix + "10003",
		Message:     "Forbidden",
		Description: "You do not have enough permission to access this resource.",
	}

	ProfileNotFound = ErrorMessage{
		Code:        errorPrefix + "11001",
		Message:     "Profile not found.",
		Description: "No user profile record found for the given profile_id",
	}

	AddProfile = ErrorMessage{
		Code:    errorPrefix + "11002",
		Message: "Profile addition failed.",
	}

	GetProfile = ErrorMessage{
		Code:    errorPrefix + "11003",
		Message: "Fetching profile(s) failed.",
	}

	GetProfileConsent = ErrorMessage{
		Code:    errorPrefix + "11004",
		Message: "Fetching profile consent failed.",
	}

	CreateProfileCookie = ErrorMessage{
		Code:    errorPrefix + "11005",
		Message: "Creating profile cookie failed.",
	}
	GetProfileCookie = ErrorMessage{
		Code:    errorPrefix + "11006",
		Message: "Fetching profile cookie failed.",
	}

	UpdateProfileCookie = ErrorMessage{
		Code:    errorPrefix + "11007",
		Message: "Updating profile cookie failed.",
	}

	ProfileCookieNotFound = ErrorMessage{
		Code:    errorPrefix + "11008",
		Message: "Profile cookie not found.",
	}

	DeleteProfileCookie = ErrorMessage{
		Code:    errorPrefix + "11009",
		Message: "Deleting profile cookie failed.",
	}

	FilterProfile = ErrorMessage{
		Code:    errorPrefix + "11010",
		Message: "Filtering profiles failed.",
	}

	UpdateProfile = ErrorMessage{
		Code:    errorPrefix + "11011",
		Message: "Profile update failed.",
	}

	DeleteProfile = ErrorMessage{
		Code:    errorPrefix + "11012",
		Message: "Profile deletion failed.",
	}

	AddAppData = ErrorMessage{
		Code:    errorPrefix + "11013",
		Message: "Add app data failed.",
	}

	GetAppData = ErrorMessage{
		Code:    errorPrefix + "11014",
		Message: "Fetching app data failed.",
	}

	UpdateAppData = ErrorMessage{
		Code:    errorPrefix + "11015",
		Message: "Updating app data failed.",
	}

	MultipleProfileFound = ErrorMessage{
		Code:        errorPrefix + "11016",
		Message:     "Multiple Profiles found.",
		Description: "Multiple user profiles record found for the given user_id",
	}

	ProfileAlreadyLinked = ErrorMessage{
		Code:        errorPrefix + "11017",
		Message:     "Profile already linked.",
		Description: "The profile is already linked to a different user",
	}

	IdentityAttributesReadOnly = ErrorMessage{
		Code:        errorPrefix + "11018",
		Message:     "Identity attributes cannot be updated.",
		Description: "The identity attributes of a profile linked to a user come from the identity provider",
	}

	UnificationRuleNotFound = ErrorMessage{
		Code:    errorPrefix + "12001",
		Message: "No unification rule found.",
	}

	UnificationUpdateFailed = ErrorMessage{
		Code:    errorPrefix + "12002",
		Message: "Validation failed for unification rule update.",
	}

	UnificationRuleAlreadyExists = ErrorMessage{
		Code:    errorPrefix + "12003",
		Message: "Unification rule with same attribute already exists.",
	}
	UnificationRulePriorityExists = ErrorMessage{
		Code:    errorPrefix + "12004",
		Message: "Unification rule priority already taken.",
	}

	GetUnificationRuleWithoutID = ErrorMessage{
		Code:    errorPrefix + "12005",
		Message: "Unification rule Id is required.",
	}

	ProfileSchemaAddBadRequest = ErrorMessage{
		Code:    errorPrefix + "13001",
		Message: "Invalid request payload.",
	}

	InvalidAttribute = ErrorMessage{
		Code:    errorPrefix + "13002",
		Message: "Invalid attribute.",
	}

	SchemaAttributeAlreadyExists = ErrorMessage{
		Code:    errorPrefix + "13003",
		Message: "Schema attribute already exists.",
	}

	AttributeNotFound = ErrorMessage{
		Code:    errorPrefix + "13004",
		Message: "Attribute not found.",
	}

	InvalidAppIdentifier = ErrorMessage{
		Code:    errorPrefix + "13005",
		Message: "Invalid application identifier.",
	}

	AttributeUpdateNotSupported = ErrorMessage{
		Code:    errorPrefix + "13006",
		Message: "Attribute add/update not supported.",
	}

	ConsentCatValidation = ErrorMessage{
		Code:    errorPrefix + "14001",
		Message: "Consent category validation failed",
	}

	ConsentCatAlreadyExists = ErrorMessage{
		Code:    errorPrefix + "14002",
		Message: "Consent category already exist.",
	}

	ConsentCatNotFound = ErrorMessage{
		Code:    errorPrefix + "14003",
		Message: "Consent category not found.",
	}

	ConsentCatID = ErrorMessage{
		Code:    errorPrefix + "14004",
		Message: "Consent category Id is required.",
	}

	UpdateConsentCategoryBadRequest = ErrorMessage{
		Code:    errorPrefix + "14005",
		Message: "Invalid request payload for updating consent category.",
	}

	ConsentCatMandatory = ErrorMessage{
		Code:    errorPrefix + "14006",
		Message: "Consent category is mandatory and cannot be modified or deleted.",
	}

	UpdateConfigBadRequest = ErrorMessage{
		Code:    errorPrefix + "16002",
		Message: "Invalid request payload for updating admin configuration.",
	}

	CDSNotEnabled = ErrorMessage{
		Code:        errorPrefix + "16001",
		Message:     "Not enabled.",
		Description: "Customer data directory is not enabled for the organization",
	}

	ServiceCredentialsRejected = ErrorMessage{
		Code:    errorPrefix + "16003",
		Message: "Service credentials rejected.",
	}

	ServiceCredentialsNotConfigured = ErrorMessage{
		Code:    errorPrefix + "16004",
		Message: "Service credentials are not accepted.",
		Description: "CDD has no encryption key to store the credentials with. Set " +
			"idp.service_credentials.encryption_key in deployment.yaml.",
	}

	IRSearchFailed = ErrorMessage{
		Code:    errorPrefix + "15501",
		Message: "Identity resolution failed.",
	}

	IRMergeFailed = ErrorMessage{
		Code:    errorPrefix + "15502",
		Message: "Merge failed.",
	}

	IRReviewTaskFailed = ErrorMessage{
		Code:    errorPrefix + "15503",
		Message: "Review task operation failed.",
	}

	IRBlockingKeysFailed = ErrorMessage{
		Code:    errorPrefix + "15504",
		Message: "Blocking key operation failed.",
	}

	IRAuditLog = ErrorMessage{
		Code:    errorPrefix + "15505",
		Message: "Merge audit log failed.",
	}

	IRSampleValues = ErrorMessage{
		Code:    errorPrefix + "15506",
		Message: "Attribute sampling failed.",
	}

	IRWorkerFailed = ErrorMessage{
		Code:    errorPrefix + "15507",
		Message: "Identity resolution worker failed.",
	}

	IRCannotMerge = ErrorMessage{
		Code:    errorPrefix + "17001",
		Message: "Cannot merge profiles.",
	}

	IRReviewTaskNotFound = ErrorMessage{
		Code:    errorPrefix + "17002",
		Message: "Review task not found.",
	}

	IRReviewTaskResolved = ErrorMessage{
		Code:    errorPrefix + "17003",
		Message: "Review task already resolved.",
	}

	// Organizations and sharing.
	//   156xx - Server errors
	//   17xxx - Client errors

	OrganizationStore = ErrorMessage{
		Code:    errorPrefix + "15601",
		Message: "Error while accessing organizations.",
	}

	OrganizationSync = ErrorMessage{
		Code:    errorPrefix + "15602",
		Message: "Error while syncing organizations with the identity provider.",
	}

	ShareStore = ErrorMessage{
		Code:    errorPrefix + "15603",
		Message: "Error while accessing share policies.",
	}

	ShareBadRequest = ErrorMessage{
		Code:    errorPrefix + "17014",
		Message: "Invalid share request.",
	}

	SharedResourceReadOnly = ErrorMessage{
		Code:        errorPrefix + "17015",
		Message:     "Shared resource is read-only.",
		Description: "Only the owner organization can change a shared resource.",
	}

	SharePolicyNotFound = ErrorMessage{
		Code:    errorPrefix + "17016",
		Message: "Share policy not found.",
	}

	SharedNameConflict = ErrorMessage{
		Code:    errorPrefix + "17004",
		Message: "Name is used by a shared resource.",
	}

	OrganizationNotFound = ErrorMessage{
		Code:    errorPrefix + "17006",
		Message: "Organization not found.",
	}

	SubOrgConfigNotAllowed = ErrorMessage{
		Code:        errorPrefix + "17007",
		Message:     "Configuration is inherited.",
		Description: "A child organization inherits the CDD enablement from its primary organization.",
	}

	SharePolicyExists = ErrorMessage{
		Code:    errorPrefix + "17008",
		Message: "Policy already exists.",
	}

	OrganizationPathNotSupported = ErrorMessage{
		Code:        errorPrefix + "17009",
		Message:     "Organization path not supported.",
		Description: "The requested organization cannot be accessed using this API path.",
	}
	SubOrgWriteNotAllowed = ErrorMessage{
		Code:    errorPrefix + "17013",
		Message: "Child organization is read-only.",
		Description: "A child organization cannot change the profile schema, the unification rules, the consent " +
			"categories, the sharing policies, or the configuration.",
	}

	InheritedAttributeReadOnly = ErrorMessage{
		Code:        errorPrefix + "17010",
		Message:     "Inherited attribute is read-only.",
		Description: "A child organization only inherits the identity attributes from its primary organization.",
	}

	OrganizationsNotSynced = ErrorMessage{
		Code:        errorPrefix + "17011",
		Message:     "Organizations are not synced.",
		Description: "Sync the organizations with POST /organizations/sync, and wait until it completes.",
	}

	TreeSyncRunning = ErrorMessage{
		Code:        errorPrefix + "17012",
		Message:     "Organization sync is running.",
		Description: "Wait until the running sync completes. GET /organizations/sync-status returns its state.",
	}

	InvalidFilterFormat = ErrorMessage{
		Code:    errorPrefix + "19001",
		Message: "Invalid filter format.",
	}
)
