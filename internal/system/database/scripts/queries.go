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

package scripts

// UpsertApplication inserts or updates the application information.
var UpsertApplication = newQuery("CDS-APP-01",
	`INSERT INTO applications (app_id, org_handle, client_id, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (app_id) DO UPDATE SET
			org_handle = EXCLUDED.org_handle,
			client_id  = EXCLUDED.client_id,
			updated_at = now()`,
	// SQLite has no now(). strftime matches the format the schema defaults use.
	`INSERT INTO applications (app_id, org_handle, client_id, updated_at)
		VALUES ($1, $2, $3, strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00')
		ON CONFLICT (app_id) DO UPDATE SET
			org_handle = EXCLUDED.org_handle,
			client_id  = EXCLUDED.client_id,
			updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'`)

// GetAppIdentifierByClientID resolves an OAuth clientId to the app_id.
var GetAppIdentifierByClientID = newQuery("CDS-APP-02",
	`SELECT app_id FROM applications
		WHERE org_handle = $1 AND client_id = $2 LIMIT 1`)

var DeleteProfileSchemaForOrg = newQuery("CDS-SCH-01",
	`
        DELETE FROM profile_schema WHERE org_handle = $1 AND scope != 'identity_attributes' `)

var GetProfileSchemaByOrg = newQuery("CDS-SCH-02",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy , application_identifier, mutability, 
       multi_valued, sub_attributes::text, canonical_values::text FROM profile_schema WHERE org_handle = $1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy , application_identifier, mutability,
       multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
       FROM profile_schema WHERE org_handle = $1`)

var DeleteIdentityClaimsOfProfileSchema = newQuery("CDS-SCH-03",
	`DELETE FROM profile_schema WHERE org_handle = $1 AND scope = 'identity_attributes'`)

var InsertIdentityClaimsForProfileSchema = newQuery("CDS-SCH-04",
	`INSERT INTO profile_schema
	(org_handle, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
	 multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name) VALUES `)

// UpsertIdentityClaimsForProfileSchema inserts or updates identity attributes in place,
// preserving the attribute_id so that FK references (e.g. unification_rules) are not broken.
var UpsertIdentityClaimsForProfileSchema = newQuery("CDS-SCH-05",
	`INSERT INTO profile_schema
	(org_handle, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
	 multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name) VALUES
	%s
	ON CONFLICT (attribute_id) DO UPDATE SET
		attribute_name         = EXCLUDED.attribute_name,
		value_type             = EXCLUDED.value_type,
		merge_strategy         = EXCLUDED.merge_strategy,
		mutability             = EXCLUDED.mutability,
		application_identifier = EXCLUDED.application_identifier,
		multi_valued           = EXCLUDED.multi_valued,
		canonical_values       = EXCLUDED.canonical_values,
		sub_attributes         = EXCLUDED.sub_attributes,
		scim_dialect           = EXCLUDED.scim_dialect,
		display_name           = EXCLUDED.display_name`)

var GetProfileSchemaAttributeByName = newQuery("CDS-SCH-06",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
       multi_valued, sub_attributes::text, canonical_values::text, scope FROM profile_schema WHERE org_handle = $1
       AND attribute_name = $2 LIMIT 1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
       multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values, scope
       FROM profile_schema WHERE org_handle = $1
       AND attribute_name = $2 LIMIT 1`)

var InsertProfileSchemaAttributesForScope = newQuery("CDS-SCH-07",
	`INSERT INTO profile_schema (org_handle, attribute_id, attribute_name, value_type, merge_strategy, 
                            application_identifier, mutability, multi_valued, sub_attributes, canonical_values, scope, display_name) VALUES `)
var GetProfileSchemaAttributeByScope = newQuery("CDS-SCH-08",
	`SELECT attribute_id, org_handle, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,   sub_attributes::text,
  canonical_values::text FROM profile_schema WHERE org_handle = $1 AND scope = $2`,
	`SELECT attribute_id, org_handle, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
  FROM profile_schema WHERE org_handle = $1 AND scope = $2`)

var UpdateProfileSchemaAttributesForSchema = newQuery("CDS-SCH-09",
	`
		UPDATE profile_schema
		SET attribute_name = $1,
			value_type = $2,
			merge_strategy = $3,
			mutability = $4,
			application_identifier = $5,
			multi_valued = $6,
			canonical_values = $7,
			sub_attributes = $8,
			display_name = $9
		WHERE org_handle = $10 AND attribute_id = $11 AND scope = $12
	`)

var DeleteProfileSchemaAttributeForScope = newQuery("CDS-SCH-10",
	`DELETE FROM profile_schema WHERE org_handle = $1 AND scope =  $2`)

var GetProfileSchemaAttributeById = newQuery("CDS-SCH-11",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued, sub_attributes::text,
  canonical_values::text, scope
	          FROM profile_schema WHERE org_handle = $1 AND attribute_id = $2`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values, scope
	          FROM profile_schema WHERE org_handle = $1 AND attribute_id = $2`)

var FilterProfileSchemaAttributes = newQuery("CDS-SCH-12",
	`SELECT attribute_id, org_handle, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued, sub_attributes::text,
  canonical_values::text FROM profile_schema WHERE org_handle = $1`,
	`SELECT attribute_id, org_handle, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values FROM profile_schema WHERE org_handle = $1`)

var DeleteProfileSchemaAttributeById = newQuery("CDS-SCH-13",
	`DELETE FROM profile_schema WHERE org_handle = $1 AND attribute_id = $2`)

// DeleteStaleIdentityClaimsForProfileSchema removes the identity attributes the
// identity server no longer reports. The %s is the NOT IN list.
var DeleteStaleIdentityClaimsForProfileSchema = newQuery("CDS-SCH-14",
	`DELETE FROM profile_schema WHERE org_handle = $1 AND scope = 'identity_attributes'
	 AND attribute_id NOT IN (%s)`)

// UpdateProfileSchemaAttributeFields is the prefix of a partial update. The
// caller appends the SET assignments and the WHERE clause.
var UpdateProfileSchemaAttributeFields = newQuery("CDS-SCH-15",
	`UPDATE profile_schema SET `)

var GetUnificationRules = newQuery("CDS-UNR-01",
	`SELECT rule_id, rule_name, property_name, property_id, priority, is_active, created_at, updated_at 
FROM unification_rules WHERE org_handle = $1`)

var GetUnificationRule = newQuery("CDS-UNR-02",
	`SELECT rule_id, rule_name, property_name, property_id, priority, is_active, created_at, updated_at FROM unification_rules WHERE rule_id = $1`)

var DeleteUnificationRule = newQuery("CDS-UNR-03",
	`DELETE FROM unification_rules WHERE rule_id = $1`)
var InsertUnificationRule = newQuery("CDS-UNR-04",
	`INSERT INTO unification_rules (rule_id, org_handle, rule_name, property_name, property_id, priority, is_active, created_at, updated_at) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)

var UpdateUnificationRule = newQuery("CDS-UNR-05",
	`UPDATE unification_rules SET rule_name = $1, priority = $2, is_active = $3,updated_at = $4
		 WHERE rule_id = $5;`)

var InsertProfile = newQuery("CDS-PRF-01",
	`
		INSERT INTO profiles (
		profile_id, user_id, org_handle, created_at, updated_at, location, list_profile, delete_profile, traits, identity_attributes
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (profile_id) DO NOTHING;`)

var InsertProfileReference = newQuery("CDS-PRF-02",
	`
		INSERT INTO profile_reference (profile_id, profile_status, reference_profile_id, reference_reason, org_handle, reference_profile_org_handle)
		VALUES ($1,$2,$3,$4, $5,$6)
		ON CONFLICT (profile_id) DO NOTHING;`)

var GetProfileById = newQuery("CDS-PRF-03",
	`
		SELECT p.profile_id, p.user_id, p.created_at, p.updated_at,p.location, p.org_handle, p.list_profile, p.delete_profile, 
		       p.traits, p.identity_attributes, r.profile_status, r.reference_profile_id, r.reference_reason
		FROM 
			profiles p
		LEFT JOIN 
			profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			p.profile_id = $1;`)

var GetProfileConsentsByProfileId = newQuery("CDS-CON-01",
	`SELECT profile_id, category_id, consent_status, consented_at FROM profile_consents WHERE profile_id = $1;`)

var DeleteProfileConsentsByProfileId = newQuery("CDS-CON-02",
	`DELETE FROM profile_consents WHERE profile_id = $1;`)

var InsertProfileConsentsByProfileId = newQuery("CDS-CON-03",
	`INSERT INTO profile_consents (profile_id, category_id, consent_status, consented_at) VALUES ($1, $2, $3, $4)`)

var GetAppDataByProfileId = newQuery("CDS-PRF-04",
	`SELECT app_id, application_data FROM application_data WHERE profile_id = $1;`)

var GetAppDataByProfileIds = newQuery("CDS-PRF-05",
	`SELECT profile_id, app_id, application_data FROM application_data WHERE profile_id IN (%s);`)

var GetAppDataByAppId = newQuery("CDS-PRF-06",
	`SELECT app_id, application_data FROM application_data WHERE profile_id = $1 AND app_id = $2;`)

var UpdateProfile = newQuery("CDS-PRF-07",
	`
		UPDATE profiles SET
			user_id = $1,
			list_profile = $2,
			delete_profile = $3,
			traits = $4,
			identity_attributes = $5,
			updated_at = $6
		 WHERE profile_id = $7;`)

var UpsertProfileReference = newQuery("CDS-PRF-08",
	`
		UPDATE profile_reference SET
			profile_id = $1,
			profile_status = $2,
			reference_profile_id = $3,
			reference_reason = $4
		 WHERE profile_id = $5;`)
var UpdateProfileReference = newQuery("CDS-PRF-09",
	`
		UPDATE profile_reference
		SET reference_profile_id = $1,
			reference_reason = $2,
			profile_status = $3
		WHERE profile_id = $4`)

var GetProfilesByOrgId = newQuery("CDS-PRF-10",
	`
		SELECT 
			p.profile_id, 
			p.org_handle, 
			p.created_at, 
			p.updated_at, 
			p.location, 
			p.user_id, 
			r.profile_status, 
			r.reference_profile_id, 
			r.reference_reason, 
			p.list_profile, 
			p.traits, 
			p.identity_attributes
		FROM profiles p
		LEFT JOIN profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			r.profile_status = 'REFERENCE_PROFILE'
			AND p.org_handle = $1
			AND (
				$2::timestamptz IS NULL
				OR (
					($4 = 'next' AND (p.created_at, p.profile_id) < ($2::timestamptz, $3::text))
					OR
					($4 = 'prev' AND (p.created_at, p.profile_id) > ($2::timestamptz, $3::text))
				)
			)
		ORDER BY 
			CASE WHEN $4 = 'prev' THEN p.created_at END ASC,
			CASE WHEN $4 = 'prev' THEN p.profile_id END ASC,
			CASE WHEN $4 <> 'prev' THEN p.created_at END DESC,
			CASE WHEN $4 <> 'prev' THEN p.profile_id END DESC
		LIMIT $5;`,
	// Same statement without the casts, which SQLite does not accept. The
	// comparison is unchanged: timestamps are stored as sortable UTC text.
	`
		SELECT
			p.profile_id,
			p.org_handle,
			p.created_at,
			p.updated_at,
			p.location,
			p.user_id,
			r.profile_status,
			r.reference_profile_id,
			r.reference_reason,
			p.list_profile,
			p.traits,
			p.identity_attributes
		FROM profiles p
		LEFT JOIN profile_reference r ON p.profile_id = r.profile_id
		WHERE
			r.profile_status = 'REFERENCE_PROFILE'
			AND p.org_handle = $1
			AND (
				$2 IS NULL
				OR (
					($4 = 'next' AND (p.created_at, p.profile_id) < ($2, $3))
					OR
					($4 = 'prev' AND (p.created_at, p.profile_id) > ($2, $3))
				)
			)
		ORDER BY
			CASE WHEN $4 = 'prev' THEN p.created_at END ASC,
			CASE WHEN $4 = 'prev' THEN p.profile_id END ASC,
			CASE WHEN $4 <> 'prev' THEN p.created_at END DESC,
			CASE WHEN $4 <> 'prev' THEN p.profile_id END DESC
		LIMIT $5;`)

var DeleteProfileByProfileId = newQuery("CDS-PRF-11",
	`DELETE FROM application_data WHERE profile_id = $1`)

// DeleteProfile removes the profile row, after its application data is gone.
var DeleteProfile = newQuery("CDS-PRF-18",
	`DELETE FROM profiles WHERE profile_id = $1`)

var InsertApplicationData = newQuery("CDS-PRF-12",
	`
		INSERT INTO application_data (profile_id, app_id, application_data)
		VALUES ($1, $2, $3)
		ON CONFLICT (profile_id, app_id)
		DO UPDATE SET application_data = EXCLUDED.application_data;
	`)

var DeleteProfileReference = newQuery("CDS-PRF-13",
	`DELETE FROM profile_reference WHERE reference_profile_id = $1 AND profile_id = $2;`)

var GetAllProfilesWithFilterBase = newQuery("CDS-PRF-14",
	`SELECT DISTINCT p.profile_id,
                p.user_id,
                p.org_handle,
                p.created_at,
                p.updated_at,
                p.location,
                r.profile_status,
                r.reference_profile_id,
                r.reference_reason,
                p.list_profile,
                p.traits,
                p.identity_attributes
FROM profiles p
LEFT JOIN profile_reference r
    ON p.profile_id = r.profile_id`)

var GetAllReferenceProfileExceptCurrent = newQuery("CDS-PRF-15",
	`
	SELECT 
		p.profile_id, 
		p.user_id, 
		r.profile_status, 
		r.reference_profile_id, 
		r.reference_reason, 
		p.org_handle,
		p.delete_profile,
		p.list_profile, 
		p.traits, 
		p.identity_attributes
	FROM 
		profiles p
	JOIN 
		profile_reference r ON p.profile_id = r.profile_id
	WHERE 
		r.profile_status = 'REFERENCE_PROFILE'
		AND p.profile_id != $1
		AND p.org_handle = $2;`,
	// Same statement, ordered by insertion. Unification stops at the first
	// match, so the row order decides which hierarchy a profile joins.
	// PostgreSQL returns these rows in insertion order in practice, while
	// SQLite is free to return them in any order, so rowid reproduces it.
	// created_at cannot be used: this statement does not select it.
	`
	SELECT
		p.profile_id,
		p.user_id,
		r.profile_status,
		r.reference_profile_id,
		r.reference_reason,
		p.org_handle,
		p.delete_profile,
		p.list_profile,
		p.traits,
		p.identity_attributes
	FROM
		profiles p
	JOIN
		profile_reference r ON p.profile_id = r.profile_id
	WHERE
		r.profile_status = 'REFERENCE_PROFILE'
		AND p.profile_id != $1
		AND p.org_handle = $2
	ORDER BY p.rowid ASC;`)

var FetchReferencedProfiles = newQuery("CDS-PRF-16",
	`
		SELECT profile_id, reference_reason, profile_status 
		FROM profile_reference 
		WHERE reference_profile_id = $1;`)

var GetProfileByUserId = newQuery("CDS-PRF-17",
	`
		SELECT p.profile_id, p.user_id, p.created_at, p.updated_at,p.location, p.org_handle, p.list_profile, p.delete_profile, 
		       p.traits, p.identity_attributes, r.profile_status, r.reference_profile_id, r.reference_reason
		FROM 
			profiles p
		LEFT JOIN 
			profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			p.user_id = $1
			AND r.profile_status = 'REFERENCE_PROFILE';`)

var LockProfile = newQuery("CDS-PRF-19",
	`SELECT profile_id FROM profiles WHERE profile_id = $1 FOR UPDATE;`,
	// SQLite has no row locks. The transaction holds the write lock of the
	// whole database from its start, so the statement only reads the row.
	`SELECT profile_id FROM profiles WHERE profile_id = $1;`)

var InsertConsentCategory = newQuery("CDS-CON-04",
	`INSERT INTO consent_categories (category_name, category_identifier, org_handle, purpose, destinations, is_mandatory)
				VALUES ($1, $2, $3, $4, $5, $6)`)

var UpsertDefaultIdentityDataCategory = newQuery("CDS-CON-05",
	`INSERT INTO consent_categories (category_name, category_identifier, org_handle, purpose, destinations, is_mandatory)
				SELECT $1::VARCHAR, $2::VARCHAR, $3::VARCHAR, $4::VARCHAR, $5::TEXT[], TRUE
				WHERE NOT EXISTS (
					SELECT 1 FROM consent_categories WHERE org_handle = $3::VARCHAR AND is_mandatory = TRUE
				)`,
	// Same statement without the casts, which SQLite infers.
	`INSERT INTO consent_categories (category_name, category_identifier, org_handle, purpose, destinations, is_mandatory)
				SELECT $1, $2, $3, $4, $5, TRUE
				WHERE NOT EXISTS (
					SELECT 1 FROM consent_categories WHERE org_handle = $3 AND is_mandatory = TRUE
				)`)

var GetAllConsentCategories = newQuery("CDS-CON-06",
	`SELECT category_name, category_identifier, org_handle, purpose, destinations, is_mandatory FROM consent_categories`)

var GetConsentCategoryById = newQuery("CDS-CON-07",
	`SELECT category_name, category_identifier, org_handle, purpose, destinations, is_mandatory FROM consent_categories WHERE category_identifier = $1`)

var GetConsentCategoryByName = newQuery("CDS-CON-08",
	`SELECT category_name, category_identifier, org_handle, purpose, destinations, is_mandatory FROM consent_categories WHERE category_name = $1 AND org_handle = $2`)

var GetMandatoryConsentCategoryIdsByOrg = newQuery("CDS-CON-09",
	`SELECT category_identifier FROM consent_categories WHERE org_handle = $1 AND is_mandatory = TRUE`)

var UpdateConsentCategory = newQuery("CDS-CON-10",
	`UPDATE consent_categories SET category_name=$1, purpose=$2, destinations=$3 WHERE category_identifier=$4`)

var DeleteConsentCategory = newQuery("CDS-CON-11",
	`DELETE FROM consent_categories WHERE category_identifier=$1`)

var InsertConsentCategoryAttribute = newQuery("CDS-CON-12",
	`INSERT INTO consent_category_attributes (category_id, scope, attribute_name, attribute_id, application_identifier)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (category_id, scope, attribute_name, application_identifier) DO NOTHING`)

var GetConsentCategoryAttributesByCategoryId = newQuery("CDS-CON-13",
	`SELECT scope, attribute_name, attribute_id, application_identifier FROM consent_category_attributes WHERE category_id = $1`)

var DeleteConsentCategoryAttributesByCategoryId = newQuery("CDS-CON-14",
	`DELETE FROM consent_category_attributes WHERE category_id = $1`)

// GetConsentCategoryAttributesByCategoryIds fetches the attributes of several
// categories in one round trip. The %s is the IN list.
var GetConsentCategoryAttributesByCategoryIds = newQuery("CDS-CON-15",
	`SELECT category_id, scope, attribute_name, attribute_id, application_identifier
	 FROM consent_category_attributes WHERE category_id IN (%s)`)

var InsertCookie = newQuery("CDS-CKI-01",
	`INSERT INTO profile_cookies (cookie_id, profile_id, is_active) VALUES ($1, $2, $3)`)

var GetCookieByCookieId = newQuery("CDS-CKI-02",
	`SELECT cookie_id, profile_id, is_active FROM profile_cookies WHERE cookie_id = $1`)

var GetCookieByProfileId = newQuery("CDS-CKI-03",
	`SELECT cookie_id, profile_id, is_active FROM profile_cookies WHERE profile_id = $1`)

var UpdateCookieStatusByProfileId = newQuery("CDS-CKI-04",
	`UPDATE profile_cookies SET is_active = $1 WHERE profile_id = $2`)

var UpdateCookieStatusByCookieId = newQuery("CDS-CKI-05",
	`UPDATE profile_cookies SET is_active = $1 WHERE cookie_id = $2`)

var DeleteCookieById = newQuery("CDS-CKI-06",
	`DELETE FROM profile_cookies WHERE cookie_id = $1`)

var DeleteCookieByProfileId = newQuery("CDS-CKI-07",
	`DELETE FROM profile_cookies WHERE profile_id = $1`)

var DeleteInactiveCookies = newQuery("CDS-CKI-08",
	`DELETE FROM profile_cookies WHERE cookie_id IN (SELECT cookie_id FROM profile_cookies 
                                                                 WHERE is_active = false LIMIT $1)`)

var GetOrgConfigurations = newQuery("CDS-CFG-01",
	`SELECT config, value FROM cds_config WHERE org_handle = $1`)

var UpdateOrgConfiguration = newQuery("CDS-CFG-02",
	`INSERT INTO cds_config (org_handle, config, value) 
                 VALUES ($1, $2, $3) 
                 ON CONFLICT (org_handle, config) 
                 DO UPDATE SET value = EXCLUDED.value`)

var GetOrgConfiguration = newQuery("CDS-CFG-03",
	`SELECT value FROM cds_config WHERE org_handle = $1 AND config = $2`)

var UpdateInitialSchemaSyncDoneConfig = newQuery("CDS-CFG-04",
	`INSERT INTO cds_config (org_handle, config, value) 
                 VALUES ($1, 'initial_schema_sync_done', $2) 
                 ON CONFLICT (org_handle, config) 
                 DO UPDATE SET value = EXCLUDED.value`)

// HealthCheckPing checks that the datasource answers.
var HealthCheckPing = newQuery("CDS-SYS-01", `SELECT 1;`)
