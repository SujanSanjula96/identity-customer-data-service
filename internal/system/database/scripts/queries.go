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
	`INSERT INTO applications (app_id, {{org_cols}}, client_id, updated_at)
		VALUES ($1, {{org_val $2}}, $3, now())
		ON CONFLICT (app_id) DO UPDATE SET
			{{org_set}},
			client_id  = EXCLUDED.client_id,
			updated_at = now()`,
	// SQLite has no now(). strftime matches the format the schema defaults use.
	`INSERT INTO applications (app_id, {{org_cols}}, client_id, updated_at)
		VALUES ($1, {{org_val $2}}, $3, strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00')
		ON CONFLICT (app_id) DO UPDATE SET
			{{org_set}},
			client_id  = EXCLUDED.client_id,
			updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'`)

// GetAppIdentifierByClientID resolves an OAuth clientId to the app_id.
var GetAppIdentifierByClientID = newQuery("CDS-APP-02",
	`SELECT app_id FROM applications
		WHERE {{org}} = $1 AND client_id = $2 LIMIT 1`)

var DeleteProfileSchemaForOrg = newQuery("CDS-SCH-01",
	`
        DELETE FROM profile_schema WHERE {{org}} = $1 AND scope != 'identity_attributes' `)

var GetProfileSchemaByOrg = newQuery("CDS-SCH-02",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy , application_identifier, mutability, 
       multi_valued, sub_attributes::text, canonical_values::text FROM profile_schema WHERE {{org}} = $1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy , application_identifier, mutability,
       multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
       FROM profile_schema WHERE {{org}} = $1`)

// GetProfileSchemaAttributesByIds returns the attributes with the IDs. The %s is the IN list of
// attribute IDs.
var GetProfileSchemaAttributesByIds = newQuery("CDS-SCH-16",
	`SELECT attribute_id, attribute_name, scope, display_name, value_type, merge_strategy, mutability,
	application_identifier, multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes,
	CAST(canonical_values AS TEXT) AS canonical_values, {{org_handle_of profile_schema}} FROM profile_schema WHERE attribute_id IN (%s)`)

var DeleteIdentityClaimsOfProfileSchema = newQuery("CDS-SCH-03",
	`DELETE FROM profile_schema WHERE {{org}} = $1 AND scope = 'identity_attributes'`)

var InsertIdentityClaimsForProfileSchema = newQuery("CDS-SCH-04",
	`INSERT INTO profile_schema
	({{org_cols}}, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
	 multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name) VALUES `)

// UpsertIdentityClaimsForProfileSchema inserts or updates identity attributes in place,
// preserving the attribute_id so that FK references (e.g. unification_rules) are not broken.
var UpsertIdentityClaimsForProfileSchema = newQuery("CDS-SCH-05",
	`INSERT INTO profile_schema
	({{org_cols}}, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
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
       multi_valued, sub_attributes::text, canonical_values::text, scope FROM profile_schema WHERE {{org}} = $1
       AND attribute_name = $2 LIMIT 1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
       multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values, scope
       FROM profile_schema WHERE {{org}} = $1
       AND attribute_name = $2 LIMIT 1`)

var InsertProfileSchemaAttributesForScope = newQuery("CDS-SCH-07",
	`INSERT INTO profile_schema ({{org_cols}}, attribute_id, attribute_name, value_type, merge_strategy, 
                            application_identifier, mutability, multi_valued, sub_attributes, canonical_values, scope, display_name) VALUES `)
var GetProfileSchemaAttributeByScope = newQuery("CDS-SCH-08",
	`SELECT attribute_id, {{org_handle_of profile_schema}}, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,   sub_attributes::text,
  canonical_values::text FROM profile_schema WHERE {{org}} = $1 AND scope = $2`,
	`SELECT attribute_id, {{org_handle_of profile_schema}}, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
  FROM profile_schema WHERE {{org}} = $1 AND scope = $2`)

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
		WHERE {{org}} = $10 AND attribute_id = $11 AND scope = $12
	`)

var DeleteProfileSchemaAttributeForScope = newQuery("CDS-SCH-10",
	`DELETE FROM profile_schema WHERE {{org}} = $1 AND scope =  $2`)

var GetProfileSchemaAttributeById = newQuery("CDS-SCH-11",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued, sub_attributes::text,
  canonical_values::text, scope
	          FROM profile_schema WHERE {{org}} = $1 AND attribute_id = $2`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values, scope
	          FROM profile_schema WHERE {{org}} = $1 AND attribute_id = $2`)

var FilterProfileSchemaAttributes = newQuery("CDS-SCH-12",
	`SELECT attribute_id, {{org_handle_of profile_schema}}, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued, sub_attributes::text,
  canonical_values::text FROM profile_schema WHERE {{org}} = $1`,
	`SELECT attribute_id, {{org_handle_of profile_schema}}, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier, multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values FROM profile_schema WHERE {{org}} = $1`)

var DeleteProfileSchemaAttributeById = newQuery("CDS-SCH-13",
	`DELETE FROM profile_schema WHERE {{org}} = $1 AND attribute_id = $2`)

// DeleteStaleIdentityClaimsForProfileSchema removes the identity attributes the
// identity server no longer reports. The %s is the NOT IN list.
var DeleteStaleIdentityClaimsForProfileSchema = newQuery("CDS-SCH-14",
	`DELETE FROM profile_schema WHERE {{org}} = $1 AND scope = 'identity_attributes'
	 AND attribute_id NOT IN (%s)`)

// UpdateProfileSchemaAttributeFields is the prefix of a partial update. The
// caller appends the SET assignments and the WHERE clause.
var UpdateProfileSchemaAttributeFields = newQuery("CDS-SCH-15",
	`UPDATE profile_schema SET `)

var GetUnificationRules = newQuery("CDS-UNR-01",
	`SELECT rule_id, {{org_handle_of unification_rules}}, rule_name, property_name, property_id, priority, is_active, attribute_type, unification_method,
	 match_strength, mismatch_strength, created_at, updated_at
FROM unification_rules WHERE {{org}} = $1`)

// GetUnificationRulesByIds returns the rules with the IDs. The %s is the IN list of rule IDs.
var GetUnificationRulesByIds = newQuery("CDS-UNR-06",
	`SELECT rule_id, {{org_handle_of unification_rules}}, rule_name, property_name, property_id, priority, is_active, attribute_type,
	 unification_method, match_strength, mismatch_strength, created_at, updated_at
	 FROM unification_rules WHERE rule_id IN (%s)`)

var GetUnificationRule = newQuery("CDS-UNR-02",
	`SELECT rule_id, {{org_handle_of unification_rules}}, rule_name, property_name, property_id, priority, is_active, attribute_type, unification_method,
	 match_strength, mismatch_strength, created_at, updated_at
	 FROM unification_rules WHERE rule_id = $1 AND {{org}} = $2`)

var DeleteUnificationRule = newQuery("CDS-UNR-03",
	`DELETE FROM unification_rules WHERE rule_id = $1 AND {{org}} = $2`)
var InsertUnificationRule = newQuery("CDS-UNR-04",
	`INSERT INTO unification_rules (rule_id, {{org_cols}}, rule_name, property_name, property_id, priority, is_active,
			attribute_type, unification_method, match_strength, mismatch_strength, created_at, updated_at)
			VALUES ($1, {{org_val $2}}, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`)

var UpdateUnificationRule = newQuery("CDS-UNR-05",
	`UPDATE unification_rules SET rule_name = $1, priority = $2, is_active = $3, attribute_type = $4,
		 unification_method = $5, match_strength = $6, mismatch_strength = $7, updated_at = $8
		 WHERE rule_id = $9 AND {{org}} = $10;`)

var InsertProfile = newQuery("CDS-PRF-01",
	`
		INSERT INTO profiles (
		profile_id, user_id, {{org_cols}}, created_at, updated_at, location, list_profile, delete_profile, traits, identity_attributes
	) VALUES ($1, $2, {{org_val $3}}, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (profile_id) DO NOTHING;`)

var InsertProfileReference = newQuery("CDS-PRF-02",
	`
		INSERT INTO profile_reference (profile_id, profile_status, reference_profile_id, reference_reason, {{org_cols}}, {{ref_org_cols}})
		VALUES ($1,$2,$3,$4, {{org_val $5}}, {{ref_org_val $6}})
		ON CONFLICT (profile_id) DO NOTHING;`)

var GetProfileById = newQuery("CDS-PRF-03",
	`
		SELECT p.profile_id, p.user_id, p.created_at, p.updated_at,p.location, {{org_handle_of p}}, p.list_profile, p.delete_profile, 
		       p.traits, p.identity_attributes, r.profile_status, r.reference_profile_id, r.reference_reason
		FROM 
			profiles p
		LEFT JOIN 
			profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			p.profile_id = $1
			AND p.{{org}} = $2;`)

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
		 WHERE profile_id = $7 AND {{org}} = $8;`)

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
			{{org_handle_of p}},
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
			AND p.{{org}} = $1
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
			{{org_handle_of p}},
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
			AND p.{{org}} = $1
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

// GetProfileIDsWithFiltersBase is the prefix of the hybrid-search id lookup. The caller
// appends the WHERE clause it builds from the request filters.
var GetProfileIDsWithFiltersBase = newQuery("CDS-PRF-21",
	`SELECT DISTINCT p.profile_id FROM profiles p LEFT JOIN profile_reference r ON p.profile_id = r.profile_id`)

var GetAllProfilesWithFilterBase = newQuery("CDS-PRF-14",
	`SELECT DISTINCT p.profile_id,
                p.user_id,
                {{org_handle_of p}},
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

var FetchReferencedProfiles = newQuery("CDS-PRF-16",
	`
		SELECT profile_id, reference_reason, profile_status 
		FROM profile_reference 
		WHERE reference_profile_id = $1;`)

var GetProfileByUserId = newQuery("CDS-PRF-17",
	`
		SELECT p.profile_id, p.user_id, p.created_at, p.updated_at,p.location, {{org_handle_of p}}, p.list_profile, p.delete_profile, 
		       p.traits, p.identity_attributes, r.profile_status, r.reference_profile_id, r.reference_reason
		FROM 
			profiles p
		LEFT JOIN 
			profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			p.user_id = $1
			AND p.{{org}} = $2
			AND r.profile_status = 'REFERENCE_PROFILE';`)

// FindOldestReferenceProfileIDByUserID returns one profile ID using explicit
// ordering so the result is independent of the database's scan order.
var FindOldestReferenceProfileIDByUserID = newQuery("CDS-PRF-19",
	`SELECT p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.{{org}} = $1 AND r.{{org}} = $1
	AND r.profile_status = 'REFERENCE_PROFILE'
	AND p.profile_id != $2 AND p.user_id = $3
	ORDER BY p.created_at, p.profile_id LIMIT 1`)

// FindOldestReferenceProfileIDByAttributeValues is completed with a validated
// JSON column name. All attribute paths and values are bound parameters.
var FindOldestReferenceProfileIDByAttributeValues = newQuery("CDS-PRF-20",
	// The indexable JSONPath predicate is followed by an exact check: lax
	// JSONPath equality alone can also match strings inside nested arrays.
	`SELECT p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.{{org}} = $1 AND r.{{org}} = $1
	AND r.profile_status = 'REFERENCE_PROFILE'
	AND p.profile_id != $2
	AND ($3 = '' OR COALESCE(p.user_id, '') = '' OR p.user_id = $3)
	AND %[1]s @? $4::jsonpath AND EXISTS (
		SELECT 1 FROM jsonb_path_query(%[1]s, $5::jsonpath) leaf(value),
		LATERAL jsonb_array_elements(CASE jsonb_typeof(leaf.value)
			WHEN 'array' THEN leaf.value ELSE jsonb_build_array(leaf.value) END) element(value)
		WHERE jsonb_typeof(element.value) = 'string'
		AND element.value IN (SELECT value FROM jsonb_array_elements($6::jsonb))
	)
	ORDER BY p.created_at, p.profile_id LIMIT 1`,
	// Arrays of objects do not consume a path component; terminal arrays
	// contribute only their immediate string elements.
	`SELECT p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.{{org}} = $1 AND r.{{org}} = $1
	AND r.profile_status = 'REFERENCE_PROFILE'
	AND p.profile_id != $2
	AND ($3 = '' OR COALESCE(p.user_id, '') = '' OR p.user_id = $3)
	AND EXISTS (
		WITH RECURSIVE field(value, type, depth) AS (
			SELECT %[1]s, json_type(%[1]s), 0
			UNION ALL
			SELECT child.value, child.type,
				field.depth + CASE WHEN field.type = 'object' THEN 1 ELSE 0 END
			FROM field, json_each(CASE WHEN field.type IN ('object', 'array')
				THEN field.value ELSE '[]' END) child
			WHERE (field.type = 'object' AND field.depth < json_array_length($4)
				AND child.key = json_extract($4, '$[' || field.depth || ']'))
			OR (field.type = 'array' AND child.type != 'array'
				AND field.depth <= json_array_length($4))
		)
		SELECT 1 FROM field WHERE depth = json_array_length($4)
		AND type = 'text' AND value COLLATE BINARY IN (SELECT value FROM json_each($5))
	)
	ORDER BY p.created_at, p.profile_id LIMIT 1`)

var InsertConsentCategory = newQuery("CDS-CON-04",
	`INSERT INTO consent_categories (category_name, category_identifier, {{org_cols}}, purpose, destinations, is_mandatory)
				VALUES ($1, $2, {{org_val $3}}, $4, $5, $6)`)

var UpsertDefaultIdentityDataCategory = newQuery("CDS-CON-05",
	`INSERT INTO consent_categories (category_name, category_identifier, {{org_cols}}, purpose, destinations, is_mandatory)
				SELECT $1::VARCHAR, $2::VARCHAR, {{org_val $3::VARCHAR}}, $4::VARCHAR, $5::TEXT[], TRUE
				WHERE NOT EXISTS (
					SELECT 1 FROM consent_categories WHERE {{org}} = $3::VARCHAR AND is_mandatory = TRUE
				)`,
	// Same statement without the casts, which SQLite infers.
	`INSERT INTO consent_categories (category_name, category_identifier, {{org_cols}}, purpose, destinations, is_mandatory)
				SELECT $1, $2, {{org_val $3}}, $4, $5, TRUE
				WHERE NOT EXISTS (
					SELECT 1 FROM consent_categories WHERE {{org}} = $3 AND is_mandatory = TRUE
				)`)

var GetAllConsentCategories = newQuery("CDS-CON-06",
	`SELECT category_name, category_identifier, {{org_handle_of consent_categories}}, purpose, destinations, is_mandatory FROM consent_categories WHERE {{org}} = $1`)

var GetConsentCategoryById = newQuery("CDS-CON-07",
	`SELECT category_name, category_identifier, {{org_handle_of consent_categories}}, purpose, destinations, is_mandatory FROM consent_categories WHERE category_identifier = $1 AND {{org}} = $2`)

var GetConsentCategoryByName = newQuery("CDS-CON-08",
	`SELECT category_name, category_identifier, {{org_handle_of consent_categories}}, purpose, destinations, is_mandatory FROM consent_categories WHERE category_name = $1 AND {{org}} = $2`)

var GetConsentCategoryIdsByOrg = newQuery("CDS-CON-16",
	`SELECT category_identifier FROM consent_categories WHERE {{org}} = $1`)

var GetMandatoryConsentCategoryIdsByOrg = newQuery("CDS-CON-09",
	`SELECT category_identifier FROM consent_categories WHERE {{org}} = $1 AND is_mandatory = TRUE`)

var UpdateConsentCategory = newQuery("CDS-CON-10",
	`UPDATE consent_categories SET category_name=$1, purpose=$2, destinations=$3 WHERE category_identifier=$4 AND {{org}}=$5`)

var DeleteConsentCategory = newQuery("CDS-CON-11",
	`DELETE FROM consent_categories WHERE category_identifier=$1 AND {{org}}=$2`)

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
	`SELECT c.cookie_id, c.profile_id, c.is_active FROM profile_cookies c
		JOIN profiles p ON p.profile_id = c.profile_id
		WHERE c.cookie_id = $1 AND p.{{org}} = $2`)

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
	`SELECT config, value FROM cds_config WHERE {{org}} = $1`)

var UpdateOrgConfiguration = newQuery("CDS-CFG-02",
	`INSERT INTO cds_config ({{org_cols}}, config, value) 
                 VALUES ({{org_val $1}}, $2, $3) 
                 ON CONFLICT ({{org}}, config) 
                 DO UPDATE SET value = EXCLUDED.value`)

var GetOrgConfiguration = newQuery("CDS-CFG-03",
	`SELECT value FROM cds_config WHERE {{org}} = $1 AND config = $2`)

var UpdateInitialSchemaSyncDoneConfig = newQuery("CDS-CFG-04",
	`INSERT INTO cds_config ({{org_cols}}, config, value) 
                 VALUES ({{org_val $1}}, 'initial_schema_sync_done', $2) 
                 ON CONFLICT ({{org}}, config) 
                 DO UPDATE SET value = EXCLUDED.value`)

// HealthCheckPing checks that the datasource answers.
var HealthCheckPing = newQuery("CDS-SYS-01", `SELECT 1;`)

// Identity resolution: blocking index, review tasks, rejection pairs and merge audit.
// DeleteBlockingKeysSQL is scoped by org_handle as well as profile_id. The profile id alone
// identifies the rows, but blocking_keys is partitioned by org_handle, so without it the
// delete probes every partition instead of pruning to one.
var DeleteBlockingKeysSQL = newQuery("CDS-IDR-01",
	`DELETE FROM blocking_keys WHERE org_handle = $1 AND profile_id = $2`)

var DeleteBlockingKeysByAttributeSQL = newQuery("CDS-IDR-02",
	`DELETE FROM blocking_keys WHERE org_handle = $1 AND attribute_name = $2`)

// IRGetProfilesForOrgAfter walks an org's profiles by key rather than by offset. OFFSET
// pagination re-runs the query for each page, so a profile inserted with a lower id while
// the scan is in flight shifts every later row back one place and one row is never read —
// leaving a silent hole in the index. Seeking past the last id already seen cannot skip.
var IRGetProfilesForOrgAfter = newQuery("CDS-IDR-03",
	`SELECT profile_id, user_id, {{org_handle_of profiles}}, traits, identity_attributes
				 FROM profiles
				 WHERE {{org}} = $1 AND delete_profile = FALSE AND profile_id > $2
				 ORDER BY profile_id
				 LIMIT $3`)

var IRGetProfilesByIDs = newQuery("CDS-IDR-04",
	`SELECT p.profile_id, p.user_id, {{org_handle_of p}}, p.traits, p.identity_attributes,
				        pr.reference_profile_id
				 FROM profiles p
				 LEFT JOIN profile_reference pr ON p.profile_id = pr.profile_id
				 WHERE p.profile_id IN (%s) AND p.delete_profile = FALSE`)

var IRInsertBlockingKeys = newQuery("CDS-IDR-05",
	`INSERT INTO blocking_keys (profile_id, org_handle, attribute_name, key_value)
				 VALUES %s ON CONFLICT DO NOTHING`)

var IRFindCandidateIDsByKeys = newQuery("CDS-IDR-06",
	`SELECT DISTINCT profile_id FROM blocking_keys
				 WHERE org_handle = $1 AND attribute_name = $2 AND key_value IN (%s)
				   AND profile_id != $%d LIMIT $%d`)

// IRCountProfilesByBlockingKey reports how many profiles in an org share one exact key
// value, which is how common that value is within the tenant.
var IRCountProfilesByBlockingKey = newQuery("CDS-IDR-07",
	`SELECT COUNT(DISTINCT profile_id) AS profile_count FROM blocking_keys
				 WHERE org_handle = $1 AND attribute_name = $2 AND key_value = $3`)

// IRInsertReviewTask raises a task, reopening the pair's existing row where there is one. A
// REJECTED row is reopened too: the worker raises a task for a rejected pair only once the
// rejection is reconsidered, and the decision itself is kept in rejection_pairs.
var IRInsertReviewTask = newQuery("CDS-IDR-08",
	`INSERT INTO review_tasks (id, org_handle, incoming_profile_id, candidate_profile_id, match_score, status, score_breakdown)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)
				 ON CONFLICT (incoming_profile_id, candidate_profile_id)
				 DO UPDATE SET match_score = $5, score_breakdown = $7, status = $6,
				               resolved_at = NULL, resolved_by = NULL, resolution_notes = NULL
				 WHERE review_tasks.status IN ('PENDING', 'CANCELLED', 'REJECTED')`)

// IRMirrorReviewTaskExists checks whether a PENDING task exists for the reverse pair (candidate→incoming).
var IRMirrorReviewTaskExists = newQuery("CDS-IDR-09",
	`SELECT COUNT(*) FROM review_tasks
				 WHERE incoming_profile_id = $1 AND candidate_profile_id = $2 AND status = $3`)

// IRUpdateMirrorReviewTask flips the direction of a mirror task and refreshes its score.
var IRUpdateMirrorReviewTask = newQuery("CDS-IDR-10",
	`UPDATE review_tasks
				 SET incoming_profile_id = $1, candidate_profile_id = $2, match_score = $3, score_breakdown = $4
				 WHERE incoming_profile_id = $5 AND candidate_profile_id = $6 AND status = $7`)

// IRCancelRelatedReviewTasks cancels all PENDING tasks that reference either profile.
var IRCancelRelatedReviewTasks = newQuery("CDS-IDR-11",
	`UPDATE review_tasks
				 SET status = $1, resolved_at = now(), resolved_by = $2, resolution_notes = $3
				 WHERE id != $4 AND status = $5
				   AND (incoming_profile_id IN ($6, $7) OR candidate_profile_id IN ($6, $7))`,
	`UPDATE review_tasks
				 SET status = $1, resolved_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00', resolved_by = $2, resolution_notes = $3
				 WHERE id != $4 AND status = $5
				   AND (incoming_profile_id IN ($6, $7) OR candidate_profile_id IN ($6, $7))`)

// IRFindRelatedPendingReviewTasks finds incoming profile IDs of PENDING tasks affected by a cascade cancel.
var IRFindRelatedPendingReviewTasks = newQuery("CDS-IDR-12",
	`SELECT DISTINCT incoming_profile_id
				 FROM review_tasks
				 WHERE id != $1 AND status = $2
				   AND (incoming_profile_id IN ($3, $4) OR candidate_profile_id IN ($3, $4))`)

var IRGetReviewTaskByID = newQuery("CDS-IDR-13",
	`SELECT id, org_handle, incoming_profile_id, candidate_profile_id, match_score, status,
				        score_breakdown, created_at, resolved_at, resolved_by, resolution_notes
				 FROM review_tasks
				 WHERE id = $1`)

var IRGetPendingReviewTasks = newQuery("CDS-IDR-14",
	`SELECT id, org_handle, incoming_profile_id, candidate_profile_id, match_score, status,
				        score_breakdown, created_at, resolved_at, resolved_by, resolution_notes
				 FROM review_tasks
				 WHERE org_handle = $1 AND status = $2
				 ORDER BY created_at DESC
				 LIMIT $3`)

var IRCountPendingReviewTasks = newQuery("CDS-IDR-15",
	`SELECT COUNT(*) FROM review_tasks WHERE org_handle = $1 AND status = $2`)

var IRGetPendingReviewTasksByProfile = newQuery("CDS-IDR-16",
	`SELECT id, org_handle, incoming_profile_id, candidate_profile_id, match_score, status,
				        score_breakdown, created_at, resolved_at, resolved_by, resolution_notes
				 FROM review_tasks
				 WHERE org_handle = $1 AND status = $2
				   AND (incoming_profile_id = $3)
				 ORDER BY match_score DESC
				 LIMIT $4`)

var IRCountPendingReviewTasksByProfile = newQuery("CDS-IDR-17",
	`SELECT COUNT(*) FROM review_tasks WHERE org_handle = $1 AND status = $2
				   AND (incoming_profile_id = $3 OR candidate_profile_id = $3)`)

var IRUpdateReviewTaskStatus = newQuery("CDS-IDR-18",
	`UPDATE review_tasks
				 SET status = $1, resolved_at = now(), resolved_by = $2, resolution_notes = $3
				 WHERE id = $4`,
	// SQLite has no now(); strftime matches the format the schema defaults use.
	`UPDATE review_tasks
				 SET status = $1, resolved_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00', resolved_by = $2, resolution_notes = $3
				 WHERE id = $4`)

var IRInsertRejectionPair = newQuery("CDS-IDR-19",
	`INSERT INTO rejection_pairs (id, org_handle, profile_id_1, profile_id_2, match_score,
				 score_breakdown, rejected_by)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)
				 ON CONFLICT (profile_id_1, profile_id_2) DO UPDATE
				 SET match_score = $5, score_breakdown = $6, rejected_by = $7`)

// IRGetRejectionsForProfile returns every rejection involving a profile, with the evidence
// the decision was made against so a later, stronger match can be reconsidered.
var IRGetRejectionsForProfile = newQuery("CDS-IDR-20",
	`SELECT profile_id_1, profile_id_2, match_score, score_breakdown FROM rejection_pairs
				 WHERE org_handle = $1 AND (profile_id_1 = $2 OR profile_id_2 = $2)`)

var IRDeleteRejectionPairsForProfile = newQuery("CDS-IDR-21",
	`DELETE FROM rejection_pairs WHERE org_handle = $1 AND (profile_id_1 = $2 OR profile_id_2 = $2)`)

// IRRepointRejectionPairs moves a rejection from a profile that has become a child onto
// the master that now represents it, so the decision survives the merge.
var IRRepointRejectionPairs = newQuery("CDS-IDR-22",
	`UPDATE rejection_pairs
				 SET profile_id_1 = CASE WHEN profile_id_1 = $2 THEN $3 ELSE profile_id_1 END,
				     profile_id_2 = CASE WHEN profile_id_2 = $2 THEN $3 ELSE profile_id_2 END
				 WHERE org_handle = $1 AND ($2 IN (profile_id_1, profile_id_2))
				   AND profile_id_1 != $3 AND profile_id_2 != $3`)
