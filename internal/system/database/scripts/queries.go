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

// Package scripts declares the SQL statements of CDS, with a variant for each supported datasource
// where the dialects differ.
package scripts

// UpsertApplication inserts or updates the application information.
var UpsertApplication = newQuery("CDS-APP-01",
	`INSERT INTO applications (app_id, org_id, client_id, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (app_id) DO UPDATE SET
			org_id = EXCLUDED.org_id,
			client_id  = EXCLUDED.client_id,
			updated_at = now()`,
	// SQLite has no now(). strftime matches the format the schema defaults use.
	`INSERT INTO applications (app_id, org_id, client_id, updated_at)
		VALUES ($1, $2, $3, strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00')
		ON CONFLICT (app_id) DO UPDATE SET
			org_id = EXCLUDED.org_id,
			client_id  = EXCLUDED.client_id,
			updated_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'`)

// GetAppIdentifierByClientID resolves an OAuth clientId to the app_id.
var GetAppIdentifierByClientID = newQuery("CDS-APP-02",
	`SELECT app_id FROM applications
		WHERE org_id = $1 AND client_id = $2 LIMIT 1`)

// DeleteProfileSchemaForOrg removes every schema attribute of an org except the identity attributes.
var DeleteProfileSchemaForOrg = newQuery("CDS-SCH-01",
	`
        DELETE FROM profile_schema WHERE org_id = $1 AND scope != 'identity_attributes' `)

// GetProfileSchemaByOrg returns every schema attribute of an org.
var GetProfileSchemaByOrg = newQuery(
	"CDS-SCH-02",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy , application_identifier,
       mutability, multi_valued, sub_attributes::text, canonical_values::text FROM profile_schema WHERE org_id = $1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy , application_identifier, mutability,
       multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
       FROM profile_schema WHERE org_id = $1`,
)

// GetProfileSchemaAttributesByIDs returns the attributes with the IDs. The %s is the IN list of
// attribute IDs.
var GetProfileSchemaAttributesByIDs = newQuery("CDS-SCH-16",
	`SELECT attribute_id, attribute_name, scope, display_name, value_type, merge_strategy, mutability,
	application_identifier, multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes,
	CAST(canonical_values AS TEXT) AS canonical_values, org_id FROM profile_schema WHERE attribute_id IN (%s)`)

// DeleteIdentityClaimsOfProfileSchema removes the identity attributes from an org's schema.
var DeleteIdentityClaimsOfProfileSchema = newQuery("CDS-SCH-03",
	`DELETE FROM profile_schema WHERE org_id = $1 AND scope = 'identity_attributes'`)

// InsertIdentityClaimsForProfileSchema is the prefix of a bulk identity attribute insert. The caller
// appends the VALUES list.
var InsertIdentityClaimsForProfileSchema = newQuery("CDS-SCH-04",
	`INSERT INTO profile_schema
	(org_id, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
	 multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name) VALUES `)

// UpsertIdentityClaimsForProfileSchema inserts or updates identity attributes in place,
// preserving the attribute_id so that FK references (e.g. unification_rules) are not broken.
var UpsertIdentityClaimsForProfileSchema = newQuery("CDS-SCH-05",
	`INSERT INTO profile_schema
	(org_id, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
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

// GetProfileSchemaAttributeByName returns the schema attribute of an org with the given name.
var GetProfileSchemaAttributeByName = newQuery("CDS-SCH-06",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
       multi_valued, sub_attributes::text, canonical_values::text, scope FROM profile_schema WHERE org_id = $1
       AND attribute_name = $2 LIMIT 1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
       multi_valued, CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values,
       scope
       FROM profile_schema WHERE org_id = $1
       AND attribute_name = $2 LIMIT 1`)

// InsertProfileSchemaAttributesForScope is the prefix of a bulk schema attribute insert. The caller
// appends the VALUES list.
var InsertProfileSchemaAttributesForScope = newQuery("CDS-SCH-07",
	`INSERT INTO profile_schema (org_id, attribute_id, attribute_name, value_type, merge_strategy, 
                            application_identifier, mutability, multi_valued, sub_attributes, canonical_values, scope,
                            display_name) VALUES `)

// GetProfileSchemaAttributeByScope returns the schema attributes of an org in one scope.
var GetProfileSchemaAttributeByScope = newQuery(
	"CDS-SCH-08",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
  multi_valued,   sub_attributes::text,
  canonical_values::text FROM profile_schema WHERE org_id = $1 AND scope = $2`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
  multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
  FROM profile_schema WHERE org_id = $1 AND scope = $2`,
)

// UpdateProfileSchemaAttributesForSchema replaces the fields of one schema attribute, matched by org, ID and scope.
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
		WHERE org_id = $10 AND attribute_id = $11 AND scope = $12
	`)

// DeleteProfileSchemaAttributeForScope removes every schema attribute of an org in one scope.
var DeleteProfileSchemaAttributeForScope = newQuery("CDS-SCH-10",
	`DELETE FROM profile_schema WHERE org_id = $1 AND scope =  $2`)

// GetProfileSchemaAttributeByID returns the schema attribute of an org with the given ID.
var GetProfileSchemaAttributeByID = newQuery(
	"CDS-SCH-11",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
  multi_valued, sub_attributes::text,
  canonical_values::text, scope
	          FROM profile_schema WHERE org_id = $1 AND attribute_id = $2`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
  multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values, scope
	          FROM profile_schema WHERE org_id = $1 AND attribute_id = $2`,
)

// FilterProfileSchemaAttributes is the base of a filtered schema attribute lookup for an org. The caller
// appends the filter conditions.
var FilterProfileSchemaAttributes = newQuery(
	"CDS-SCH-12",
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
  multi_valued, sub_attributes::text,
  canonical_values::text FROM profile_schema WHERE org_id = $1`,
	`SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability, application_identifier,
  multi_valued,
  CAST(sub_attributes AS TEXT) AS sub_attributes, CAST(canonical_values AS TEXT) AS canonical_values
  FROM profile_schema WHERE org_id = $1`,
)

// DeleteProfileSchemaAttributeByID removes the schema attribute of an org with the given ID.
var DeleteProfileSchemaAttributeByID = newQuery("CDS-SCH-13",
	`DELETE FROM profile_schema WHERE org_id = $1 AND attribute_id = $2`)

// DeleteStaleIdentityClaimsForProfileSchema removes the identity attributes the
// identity server no longer reports. The %s is the NOT IN list.
var DeleteStaleIdentityClaimsForProfileSchema = newQuery("CDS-SCH-14",
	`DELETE FROM profile_schema WHERE org_id = $1 AND scope = 'identity_attributes'
	 AND attribute_id NOT IN (%s)`)

// GetUnificationRules returns every unification rule of an org.
var GetUnificationRules = newQuery(
	"CDS-UNR-01",
	`SELECT rule_id, org_id, rule_name, property_name, property_id, priority, is_active, attribute_type,
	 unification_method, match_strength, mismatch_strength, created_at, updated_at
FROM unification_rules WHERE org_id = $1`,
)

// GetUnificationRulesByIDs returns the rules with the IDs. The %s is the IN list of rule IDs.
var GetUnificationRulesByIDs = newQuery("CDS-UNR-06",
	`SELECT rule_id, org_id, rule_name, property_name, property_id, priority, is_active, attribute_type,
	 unification_method, match_strength, mismatch_strength, created_at, updated_at
	 FROM unification_rules WHERE rule_id IN (%s)`)

// GetUnificationRule returns the unification rule of an org with the given ID.
var GetUnificationRule = newQuery(
	"CDS-UNR-02",
	`SELECT rule_id, org_id, rule_name, property_name, property_id, priority, is_active, attribute_type,
	 unification_method, match_strength, mismatch_strength, created_at, updated_at
	 FROM unification_rules WHERE rule_id = $1 AND org_id = $2`,
)

// DeleteUnificationRule removes the unification rule of an org with the given ID.
var DeleteUnificationRule = newQuery("CDS-UNR-03",
	`DELETE FROM unification_rules WHERE rule_id = $1 AND org_id = $2`)

// InsertUnificationRule stores a new unification rule.
var InsertUnificationRule = newQuery("CDS-UNR-04",
	`INSERT INTO unification_rules (rule_id, org_id, rule_name, property_name, property_id, priority, is_active,
			attribute_type, unification_method, match_strength, mismatch_strength, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`)

// UpdateUnificationRule replaces the editable fields of a unification rule. The rule name is not editable,
// because merges record it as the reason that two profiles were combined.
var UpdateUnificationRule = newQuery("CDS-UNR-05",
	`UPDATE unification_rules SET priority = $1, is_active = $2, attribute_type = $3,
		 unification_method = $4, match_strength = $5, mismatch_strength = $6, updated_at = $7
		 WHERE rule_id = $8 AND org_id = $9;`)

// InsertProfile stores a new profile. A profile whose ID already exists is left unchanged.
var InsertProfile = newQuery("CDS-PRF-01",
	`
		INSERT INTO profiles (
		profile_id, user_id, org_id, created_at, updated_at, list_profile, delete_profile, traits, identity_attributes
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (profile_id) DO NOTHING;`)

// InsertProfileReference stores the reference row of a profile. An existing row is left unchanged.
var InsertProfileReference = newQuery("CDS-PRF-02",
	`
		INSERT INTO profile_reference (profile_id, profile_status, reference_profile_id, reference_reason, org_id,
			reference_profile_org_id)
		VALUES ($1,$2,$3,$4, $5, $6)
		ON CONFLICT (profile_id) DO NOTHING;`)

// GetProfileByID returns a profile of an org by ID, with its reference details.
var GetProfileByID = newQuery("CDS-PRF-03",
	`
		SELECT p.profile_id, p.user_id, p.created_at, p.updated_at, p.org_id, p.list_profile, p.delete_profile, 
		       p.traits, p.identity_attributes, r.profile_status, r.reference_profile_id, r.reference_reason
		FROM 
			profiles p
		LEFT JOIN 
			profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			p.profile_id = $1
			AND p.org_id = $2;`)

// GetProfileConsentsByProfileID returns the consents recorded for a profile.
var GetProfileConsentsByProfileID = newQuery("CDS-CON-01",
	`SELECT profile_id, category_id, consent_status, consented_at FROM profile_consents WHERE profile_id = $1;`)

// DeleteProfileConsentsByProfileID removes every consent recorded for a profile.
var DeleteProfileConsentsByProfileID = newQuery("CDS-CON-02",
	`DELETE FROM profile_consents WHERE profile_id = $1;`)

// InsertProfileConsentsByProfileID records one consent for a profile.
var InsertProfileConsentsByProfileID = newQuery("CDS-CON-03",
	`INSERT INTO profile_consents (profile_id, category_id, consent_status, consented_at) VALUES ($1, $2, $3, $4)`)

// GetProfileConsentsByProfileIDs returns the consents recorded for several profiles. It is a
// template: the caller fills the IN list with one placeholder per profile.
var GetProfileConsentsByProfileIDs = newQuery("CDS-CON-17",
	`SELECT profile_id, category_id, consent_status FROM profile_consents WHERE profile_id IN (%s)`)

// GetAppDataByProfileID returns the application data of a profile for every application.
var GetAppDataByProfileID = newQuery("CDS-PRF-04",
	`SELECT app_id, application_data FROM application_data WHERE profile_id = $1;`)

// GetAppDataByProfileIDs returns the application data of several profiles. The %s is the IN list of profile IDs.
var GetAppDataByProfileIDs = newQuery("CDS-PRF-05",
	`SELECT profile_id, app_id, application_data FROM application_data WHERE profile_id IN (%s);`)

// GetAppDataByAppID returns the application data of a profile for one application.
var GetAppDataByAppID = newQuery("CDS-PRF-06",
	`SELECT app_id, application_data FROM application_data WHERE profile_id = $1 AND app_id = $2;`)

// UpdateProfile replaces the mutable fields of a profile of an org.
var UpdateProfile = newQuery("CDS-PRF-07",
	`
		UPDATE profiles SET
			user_id = $1,
			list_profile = $2,
			delete_profile = $3,
			traits = $4,
			identity_attributes = $5,
			updated_at = $6
		 WHERE profile_id = $7 AND org_id = $8;`)

// UpsertProfileReference updates the reference row of a profile. Despite the name, it does not insert a missing row.
var UpsertProfileReference = newQuery("CDS-PRF-08",
	`
		UPDATE profile_reference SET
			profile_id = $1,
			profile_status = $2,
			reference_profile_id = $3,
			reference_reason = $4
		 WHERE profile_id = $5;`)

// UpdateProfileReference points a profile at its reference profile and sets its status.
var UpdateProfileReference = newQuery("CDS-PRF-09",
	`
		UPDATE profile_reference
		SET reference_profile_id = $1,
			reference_reason = $2,
			profile_status = $3
		WHERE profile_id = $4`)

// GetProfilesByOrgID returns one page of an org's reference profiles, using keyset pagination
// on created_at and profile_id. $4 is the direction, 'next' or 'prev'.
var GetProfilesByOrgID = newQuery("CDS-PRF-10",
	`
		SELECT 
			p.profile_id, 
			p.org_id,
			p.created_at, 
			p.updated_at, 
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
			AND p.org_id = $1
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
			p.org_id,
			p.created_at,
			p.updated_at,
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
			AND p.org_id = $1
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

// DeleteProfileByProfileID removes the application data of a profile. DeleteProfile removes the profile row itself.
var DeleteProfileByProfileID = newQuery("CDS-PRF-11",
	`DELETE FROM application_data WHERE profile_id = $1`)

// DeleteProfile removes the profile row, after its application data is gone.
var DeleteProfile = newQuery("CDS-PRF-18",
	`DELETE FROM profiles WHERE profile_id = $1`)

// ListExpiredTemporaryProfiles locks and returns up to $3 temporary profiles of org $1 not updated
// since $2.
var ListExpiredTemporaryProfiles = newQuery("CDS-PRF-22",
	`SELECT p.profile_id FROM profiles p
	JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.org_id = $1 AND p.updated_at < $2 AND (p.user_id IS NULL OR p.user_id = '')
	  AND r.profile_status = 'REFERENCE_PROFILE'
	  AND NOT EXISTS (SELECT 1 FROM profile_reference c WHERE c.reference_profile_id = p.profile_id)
	ORDER BY p.updated_at
	LIMIT $3
	FOR UPDATE`,
	`SELECT p.profile_id FROM profiles p
	JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.org_id = $1 AND p.updated_at < $2 AND (p.user_id IS NULL OR p.user_id = '')
	  AND r.profile_status = 'REFERENCE_PROFILE'
	  AND NOT EXISTS (SELECT 1 FROM profile_reference c WHERE c.reference_profile_id = p.profile_id)
	ORDER BY p.updated_at
	LIMIT $3`)

// DeleteProfilesByIDs deletes the profiles in the IN list %s. Dependent rows go through foreign keys.
var DeleteProfilesByIDs = newQuery("CDS-PRF-25",
	`DELETE FROM profiles WHERE org_id = $1 AND profile_id IN (%s)`)

// LockProfileReference locks the reference row of one profile until the transaction ends, and
// returns its status. A merge locks the rows of both profiles first, one statement each in
// profile ID order, so two merges that share a profile wait for each other rather than
// deadlock. The inbuilt database takes its write lock when the transaction begins, so it needs
// no row lock.
var LockProfileReference = newQuery("CDS-PRF-26",
	`SELECT profile_id, profile_status FROM profile_reference
	WHERE profile_id = $1 AND org_id = $2
	FOR UPDATE`,
	`SELECT profile_id, profile_status FROM profile_reference
	WHERE profile_id = $1 AND org_id = $2`)

// DeleteReviewTasksByCandidateProfileIDs removes the review tasks that name a profile in the IN list %s
// as candidate.
var DeleteReviewTasksByCandidateProfileIDs = newQuery("CDS-IDR-24",
	`DELETE FROM review_tasks WHERE org_id = $1 AND candidate_profile_id IN (%s)`)

// DeleteProfileReferencesByProfileIDs removes the reference rows of the profiles in the IN list %s.
var DeleteProfileReferencesByProfileIDs = newQuery("CDS-PRF-23",
	`DELETE FROM profile_reference WHERE org_id = $1 AND profile_id IN (%s)`)

// ListProfileOrgIDs returns the ID of every organization that has a profile.
var ListProfileOrgIDs = newQuery("CDS-PRF-24",
	`SELECT DISTINCT org_id FROM profiles`)

// InsertApplicationData stores a profile's data for an application, replacing any existing data for that pair.
var InsertApplicationData = newQuery("CDS-PRF-12",
	`
		INSERT INTO application_data (profile_id, app_id, application_data)
		VALUES ($1, $2, $3)
		ON CONFLICT (profile_id, app_id)
		DO UPDATE SET application_data = EXCLUDED.application_data;
	`)

// DeleteProfileReference removes the reference row of a profile that points at the given reference profile.
var DeleteProfileReference = newQuery("CDS-PRF-13",
	`DELETE FROM profile_reference WHERE reference_profile_id = $1 AND profile_id = $2;`)

// GetProfileIDsWithFiltersBase is the prefix of the hybrid-search id lookup. The caller
// appends the WHERE clause it builds from the request filters.
var GetProfileIDsWithFiltersBase = newQuery("CDS-PRF-21",
	`SELECT DISTINCT p.profile_id FROM profiles p LEFT JOIN profile_reference r ON p.profile_id = r.profile_id`)

// GetAllProfilesWithFilterBase is the prefix of a filtered profile lookup. The caller appends the WHERE clause.
var GetAllProfilesWithFilterBase = newQuery("CDS-PRF-14",
	`SELECT DISTINCT p.profile_id,
                p.user_id,
                p.org_id,
                p.created_at,
                p.updated_at,
                r.profile_status,
                r.reference_profile_id,
                r.reference_reason,
                p.list_profile,
                p.traits,
                p.identity_attributes
FROM profiles p
LEFT JOIN profile_reference r
    ON p.profile_id = r.profile_id`)

// FetchReferencedProfiles returns the profiles that point at the given reference profile.
var FetchReferencedProfiles = newQuery("CDS-PRF-16",
	`
		SELECT profile_id, reference_reason, profile_status 
		FROM profile_reference 
		WHERE reference_profile_id = $1;`)

// GetProfileByUserID returns the reference profile of an org that belongs to the given user.
var GetProfileByUserID = newQuery("CDS-PRF-17",
	`
		SELECT p.profile_id, p.user_id, p.created_at, p.updated_at, p.org_id, p.list_profile, p.delete_profile, 
		       p.traits, p.identity_attributes, r.profile_status, r.reference_profile_id, r.reference_reason
		FROM 
			profiles p
		LEFT JOIN 
			profile_reference r ON p.profile_id = r.profile_id
		WHERE 
			p.user_id = $1
			AND p.org_id = $2
			AND r.profile_status = 'REFERENCE_PROFILE';`)

// FindOldestReferenceProfileIDByUserID returns one profile ID using explicit
// ordering so the result is independent of the database's scan order.
var FindOldestReferenceProfileIDByUserID = newQuery("CDS-PRF-19",
	`SELECT p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.org_id = $1 AND r.org_id = $1
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
	WHERE p.org_id = $1 AND r.org_id = $1
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
	WHERE p.org_id = $1 AND r.org_id = $1
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

// InsertConsentCategory stores a new consent category.
var InsertConsentCategory = newQuery("CDS-CON-04",
	`INSERT INTO consent_categories (category_name, category_identifier, org_id, purpose, destinations, is_mandatory)
				VALUES ($1, $2, $3, $4, $5, $6)`)

// UpsertDefaultIdentityDataCategory creates the mandatory consent category of an org, unless the org already has one.
var UpsertDefaultIdentityDataCategory = newQuery("CDS-CON-05",
	`INSERT INTO consent_categories (category_name, category_identifier, org_id, purpose, destinations, is_mandatory)
				SELECT $1::VARCHAR, $2::VARCHAR, $3::VARCHAR, $4::VARCHAR, $5::TEXT[], TRUE
				WHERE NOT EXISTS (
					SELECT 1 FROM consent_categories WHERE org_id = $3::VARCHAR AND is_mandatory = TRUE
				)`,
	// Same statement without the casts, which SQLite infers.
	`INSERT INTO consent_categories (category_name, category_identifier, org_id, purpose, destinations, is_mandatory)
				SELECT $1, $2, $3, $4, $5, TRUE
				WHERE NOT EXISTS (
					SELECT 1 FROM consent_categories WHERE org_id = $3 AND is_mandatory = TRUE
				)`)

// GetAllConsentCategories returns every consent category of an org.
var GetAllConsentCategories = newQuery(
	"CDS-CON-06",
	`SELECT category_name, category_identifier, org_id, purpose, destinations, is_mandatory FROM consent_categories
	 WHERE org_id = $1`,
)

// GetConsentCategoryByID returns the consent category of an org with the given identifier.
var GetConsentCategoryByID = newQuery(
	"CDS-CON-07",
	`SELECT category_name, category_identifier, org_id, purpose, destinations, is_mandatory FROM consent_categories
	 WHERE category_identifier = $1 AND org_id = $2`,
)

// GetConsentCategoryByName returns the consent category of an org with the given name.
var GetConsentCategoryByName = newQuery(
	"CDS-CON-08",
	`SELECT category_name, category_identifier, org_id, purpose, destinations, is_mandatory FROM consent_categories
	 WHERE category_name = $1 AND org_id = $2`,
)

// GetConsentCategoryIDsByOrg returns the identifiers of every consent category of an org.
var GetConsentCategoryIDsByOrg = newQuery("CDS-CON-16",
	`SELECT category_identifier FROM consent_categories WHERE org_id = $1`)

// GetMandatoryConsentCategoryIDsByOrg returns the identifiers of an org's mandatory consent categories.
var GetMandatoryConsentCategoryIDsByOrg = newQuery("CDS-CON-09",
	`SELECT category_identifier FROM consent_categories WHERE org_id = $1 AND is_mandatory = TRUE`)

// UpdateConsentCategory replaces the name, purpose and destinations of a consent category.
var UpdateConsentCategory = newQuery(
	"CDS-CON-10",
	`UPDATE consent_categories SET category_name=$1, purpose=$2, destinations=$3
	 WHERE category_identifier=$4 AND org_id=$5`,
)

// DeleteConsentCategory removes the consent category of an org with the given identifier.
var DeleteConsentCategory = newQuery("CDS-CON-11",
	`DELETE FROM consent_categories WHERE category_identifier=$1 AND org_id=$2`)

// InsertConsentCategoryAttribute links an attribute to a consent category. An existing link is left unchanged.
var InsertConsentCategoryAttribute = newQuery("CDS-CON-12",
	`INSERT INTO consent_category_attributes (category_id, scope, attribute_name, attribute_id, application_identifier)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (category_id, scope, attribute_name, application_identifier) DO NOTHING`)

// GetConsentCategoryAttributesByCategoryID returns the attributes linked to a consent category.
var GetConsentCategoryAttributesByCategoryID = newQuery(
	"CDS-CON-13",
	`SELECT scope, attribute_name, attribute_id, application_identifier FROM consent_category_attributes
	 WHERE category_id = $1`,
)

// DeleteConsentCategoryAttributesByCategoryID removes every attribute linked to a consent category.
var DeleteConsentCategoryAttributesByCategoryID = newQuery("CDS-CON-14",
	`DELETE FROM consent_category_attributes WHERE category_id = $1`)

// GetConsentCategoryAttributesByCategoryIDs fetches the attributes of several
// categories in one round trip. The %s is the IN list.
var GetConsentCategoryAttributesByCategoryIDs = newQuery("CDS-CON-15",
	`SELECT category_id, scope, attribute_name, attribute_id, application_identifier
	 FROM consent_category_attributes WHERE category_id IN (%s)`)

// InsertCookie stores a new profile cookie.
var InsertCookie = newQuery("CDS-CKI-01",
	`INSERT INTO profile_cookies (cookie_id, profile_id, is_active) VALUES ($1, $2, $3)`)

// GetCookieByCookieID returns a cookie by ID, provided its profile belongs to the given org.
var GetCookieByCookieID = newQuery("CDS-CKI-02",
	`SELECT c.cookie_id, c.profile_id, c.is_active FROM profile_cookies c
		JOIN profiles p ON p.profile_id = c.profile_id
		WHERE c.cookie_id = $1 AND p.org_id = $2`)

// GetCookieByProfileID returns the cookies of a profile.
var GetCookieByProfileID = newQuery("CDS-CKI-03",
	`SELECT cookie_id, profile_id, is_active FROM profile_cookies WHERE profile_id = $1`)

// UpdateCookieStatusByProfileID sets the active flag of every cookie of a profile.
var UpdateCookieStatusByProfileID = newQuery("CDS-CKI-04",
	`UPDATE profile_cookies SET is_active = $1 WHERE profile_id = $2`)

// UpdateCookieStatusByCookieID sets the active flag of one cookie.
var UpdateCookieStatusByCookieID = newQuery("CDS-CKI-05",
	`UPDATE profile_cookies SET is_active = $1 WHERE cookie_id = $2`)

// DeleteCookieByID removes one cookie.
var DeleteCookieByID = newQuery("CDS-CKI-06",
	`DELETE FROM profile_cookies WHERE cookie_id = $1`)

// DeleteCookieByProfileID removes every cookie of a profile.
var DeleteCookieByProfileID = newQuery("CDS-CKI-07",
	`DELETE FROM profile_cookies WHERE profile_id = $1`)

// DeleteInactiveCookies removes at most $1 inactive cookies, so the cleanup can run in batches.
var DeleteInactiveCookies = newQuery("CDS-CKI-08",
	`DELETE FROM profile_cookies WHERE cookie_id IN (SELECT cookie_id FROM profile_cookies 
                                                                 WHERE is_active = false LIMIT $1)`)

// GetOrgConfigurations returns every configuration entry of an org.
var GetOrgConfigurations = newQuery("CDS-CFG-01",
	`SELECT config, value FROM cds_config WHERE org_id = $1`)

// UpdateOrgConfiguration inserts or updates one configuration entry of an org.
var UpdateOrgConfiguration = newQuery("CDS-CFG-02",
	`INSERT INTO cds_config (org_id, config, value) 
                 VALUES ($1, $2, $3) 
                 ON CONFLICT (org_id, config) 
                 DO UPDATE SET value = EXCLUDED.value`)

// GetOrgConfiguration returns the value of one configuration entry of an org.
var GetOrgConfiguration = newQuery("CDS-CFG-03",
	`SELECT value FROM cds_config WHERE org_id = $1 AND config = $2`)

// ListOrgConfigurationValues returns the value of the config entries $1 and $2 of every org that
// stores them.
var ListOrgConfigurationValues = newQuery("CDS-CFG-05",
	`SELECT org_id, config, value FROM cds_config WHERE config IN ($1, $2)`)

// UpdateInitialSchemaSyncDoneConfig records whether the initial schema sync of an org is done.
var UpdateInitialSchemaSyncDoneConfig = newQuery("CDS-CFG-04",
	`INSERT INTO cds_config (org_id, config, value) 
                 VALUES ($1, 'initial_schema_sync_done', $2) 
                 ON CONFLICT (org_id, config) 
                 DO UPDATE SET value = EXCLUDED.value`)

// GetOrgServiceCredentials reads the service credentials of an organization by its handle. They are
// keyed by org ID; the organizations table resolves the handle, since token requests and introspection
// name the organization by its handle.
var GetOrgServiceCredentials = newQuery("CDS-SVC-01",
	`SELECT c.org_id, c.client_id, c.client_secret, c.key_id
                 FROM org_service_credentials c JOIN organizations o ON o.org_id = c.org_id
                 WHERE o.org_handle = $1`)

// CheckOrgServiceCredentials reads one row, to check at start that the table exists.
var CheckOrgServiceCredentials = newQuery("CDS-SVC-03",
	`SELECT org_id FROM org_service_credentials LIMIT 1`)

// UpsertOrgServiceCredentials stores the service credentials of an organization, replacing any it had.
var UpsertOrgServiceCredentials = newQuery("CDS-SVC-02",
	`INSERT INTO org_service_credentials (org_id, client_id, client_secret, key_id)
                 VALUES ($1, $2, $3, $4)
                 ON CONFLICT (org_id)
                 DO UPDATE SET client_id = EXCLUDED.client_id, client_secret = EXCLUDED.client_secret,
                               key_id = EXCLUDED.key_id`)

// HealthCheckPing checks that the datasource answers.
var HealthCheckPing = newQuery("CDS-SYS-01", `SELECT 1;`)

// Identity resolution: blocking index, review tasks, rejection pairs and merge audit.

// DeleteBlockingKeysSQL is scoped by the org as well as profile_id. The profile id alone
// identifies the rows, but blocking_keys is partitioned by org_id, so without it the
// delete probes every partition instead of pruning to one.
var DeleteBlockingKeysSQL = newQuery("CDS-IDR-01",
	`DELETE FROM blocking_keys WHERE org_id = $1 AND profile_id = $2`)

// DeleteBlockingKeysByAttributeSQL removes every blocking key of an org for one attribute.
var DeleteBlockingKeysByAttributeSQL = newQuery("CDS-IDR-02",
	`DELETE FROM blocking_keys WHERE org_id = $1 AND attribute_name = $2`)

// IRGetProfilesForOrgAfter walks an org's profiles by key rather than by offset. OFFSET
// pagination re-runs the query for each page, so a profile inserted with a lower id while
// the scan is in flight shifts every later row back one place and one row is never read —
// leaving a silent hole in the index. Seeking past the last id already seen cannot skip.
var IRGetProfilesForOrgAfter = newQuery("CDS-IDR-03",
	`SELECT profile_id, user_id, traits, identity_attributes
				 FROM profiles
				 WHERE org_id = $1 AND delete_profile = FALSE AND profile_id > $2
				 ORDER BY profile_id
				 LIMIT $3`)

// IRGetProfilesByIDs returns the profiles with the IDs that are not marked for deletion, with their
// reference profile. The %s is the IN list.
var IRGetProfilesByIDs = newQuery("CDS-IDR-04",
	`SELECT p.profile_id, p.user_id, p.traits, p.identity_attributes,
				        pr.reference_profile_id
				 FROM profiles p
				 LEFT JOIN profile_reference pr ON p.profile_id = pr.profile_id
				 WHERE p.profile_id IN (%s) AND p.delete_profile = FALSE`)

// IRInsertBlockingKeys stores blocking keys, skipping any that exist. The %s is the VALUES list.
var IRInsertBlockingKeys = newQuery("CDS-IDR-05",
	`INSERT INTO blocking_keys (profile_id, org_id, attribute_name, key_value)
				 VALUES %s ON CONFLICT DO NOTHING`)

// IRFindCandidateIDsByKeys returns the profiles that share a blocking key value with the incoming profile.
// The %s is the IN list of key values, and the %d placeholders take the excluded profile ID and the limit.
var IRFindCandidateIDsByKeys = newQuery("CDS-IDR-06",
	`SELECT DISTINCT profile_id FROM blocking_keys
				 WHERE org_id = $1 AND attribute_name = $2 AND key_value IN (%s)
				   AND profile_id != $%d LIMIT $%d`)

// IRCountProfilesByBlockingKey reports how many profiles in an org share one exact key
// value, which is how common that value is within the tenant.
var IRCountProfilesByBlockingKey = newQuery("CDS-IDR-07",
	`SELECT COUNT(DISTINCT profile_id) AS profile_count FROM blocking_keys
				 WHERE org_id = $1 AND attribute_name = $2 AND key_value = $3`)

// IRInsertReviewTask raises a task, reopening the pair's existing row where there is one. A
// REJECTED row is reopened too: the worker raises a task for a rejected pair only once the
// rejection is reconsidered, and the decision itself is kept in rejection_pairs.
var IRInsertReviewTask = newQuery(
	"CDS-IDR-08",
	`INSERT INTO review_tasks (id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
				 score_breakdown, match_reason)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				 ON CONFLICT (incoming_profile_id, candidate_profile_id)
				 DO UPDATE SET match_score = $5, score_breakdown = $7, match_reason = $8, status = $6,
				               resolved_at = NULL, resolved_by = NULL, resolution_notes = NULL
				 WHERE review_tasks.status IN ('PENDING', 'CANCELLED', 'REJECTED')`,
)

// IRMirrorReviewTaskExists checks whether a PENDING task exists for the reverse pair (candidate→incoming).
var IRMirrorReviewTaskExists = newQuery("CDS-IDR-09",
	`SELECT COUNT(*) AS count FROM review_tasks
				 WHERE incoming_profile_id = $1 AND candidate_profile_id = $2 AND status = $3`)

// IRUpdateMirrorReviewTask flips the direction of a mirror task and refreshes its evidence.
var IRUpdateMirrorReviewTask = newQuery("CDS-IDR-10",
	`UPDATE review_tasks
				 SET incoming_profile_id = $1, candidate_profile_id = $2, match_score = $3, score_breakdown = $4,
				     match_reason = $8
				 WHERE incoming_profile_id = $5 AND candidate_profile_id = $6 AND status = $7`)

// IRCancelRelatedReviewTasks cancels all PENDING tasks that reference either profile.
var IRCancelRelatedReviewTasks = newQuery("CDS-IDR-11",
	`UPDATE review_tasks
				 SET status = $1, resolved_at = now(), resolved_by = $2, resolution_notes = $3
				 WHERE id != $4 AND status = $5
				   AND (incoming_profile_id IN ($6, $7) OR candidate_profile_id IN ($6, $7))`,
	`UPDATE review_tasks
				 SET status = $1, resolved_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00', resolved_by = $2,
				     resolution_notes = $3
				 WHERE id != $4 AND status = $5
				   AND (incoming_profile_id IN ($6, $7) OR candidate_profile_id IN ($6, $7))`)

// IRFindRelatedPendingReviewTasks finds the profile pairs of PENDING tasks affected by a cascade cancel.
var IRFindRelatedPendingReviewTasks = newQuery("CDS-IDR-12",
	`SELECT DISTINCT incoming_profile_id, candidate_profile_id
				 FROM review_tasks
				 WHERE id != $1 AND status = $2
				   AND (incoming_profile_id IN ($3, $4) OR candidate_profile_id IN ($3, $4))`)

// IRGetReviewTaskByID returns the review task with the given ID.
var IRGetReviewTaskByID = newQuery("CDS-IDR-13",
	`SELECT id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
				        score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
				 FROM review_tasks
				 WHERE id = $1`)

// IRGetPendingReviewTasks returns an org's review tasks in one status, newest first, up to the limit.
var IRGetPendingReviewTasks = newQuery("CDS-IDR-14",
	`SELECT id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
				        score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
				 FROM review_tasks
				 WHERE org_id = $1 AND status = $2
				 ORDER BY created_at DESC
				 LIMIT $3`)

// IRCountPendingReviewTasks counts an org's review tasks in one status.
var IRCountPendingReviewTasks = newQuery("CDS-IDR-15",
	`SELECT COUNT(*) AS count FROM review_tasks WHERE org_id = $1 AND status = $2`)

// IRGetPendingReviewTasksByProfile returns an org's review tasks in one status raised for an incoming profile,
// highest score first, up to the limit.
var IRGetPendingReviewTasksByProfile = newQuery("CDS-IDR-16",
	`SELECT id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
				        score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
				 FROM review_tasks
				 WHERE org_id = $1 AND status = $2
				   AND (incoming_profile_id = $3)
				 ORDER BY match_score DESC
				 LIMIT $4`)

// IRCountPendingReviewTasksByProfile counts an org's review tasks in one status that involve a profile on either side.
var IRCountPendingReviewTasksByProfile = newQuery("CDS-IDR-17",
	`SELECT COUNT(*) AS count FROM review_tasks WHERE org_id = $1 AND status = $2
				   AND (incoming_profile_id = $3 OR candidate_profile_id = $3)`)

// IRUpdateReviewTaskStatus sets the status of a review task and records who resolved it and when.
var IRUpdateReviewTaskStatus = newQuery("CDS-IDR-18",
	`UPDATE review_tasks
				 SET status = $1, resolved_at = now(), resolved_by = $2, resolution_notes = $3
				 WHERE id = $4`,
	// SQLite has no now(); strftime matches the format the schema defaults use.
	`UPDATE review_tasks
				 SET status = $1, resolved_at = strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00', resolved_by = $2,
				     resolution_notes = $3
				 WHERE id = $4`)

// IRLockReviewTask locks a review task until the transaction ends, and returns its status.
var IRLockReviewTask = newQuery("CDS-IDR-25",
	`SELECT status FROM review_tasks WHERE id = $1 FOR UPDATE`,
	// SQLite locks the whole database when the transaction begins.
	`SELECT status FROM review_tasks WHERE id = $1`)

// IRInsertRejectionPair records a rejected profile pair, replacing the evidence of an existing rejection.
var IRInsertRejectionPair = newQuery("CDS-IDR-19",
	`INSERT INTO rejection_pairs (id, org_id, profile_id_1, profile_id_2, match_score,
				 score_breakdown, rejected_by)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)
				 ON CONFLICT (profile_id_1, profile_id_2) DO UPDATE
				 SET match_score = $5, score_breakdown = $6, rejected_by = $7`)

// IRGetRejectionsForProfile returns every rejection involving a profile, with the evidence
// the decision was made against so a later, stronger match can be reconsidered.
var IRGetRejectionsForProfile = newQuery("CDS-IDR-20",
	`SELECT profile_id_1, profile_id_2, match_score, score_breakdown FROM rejection_pairs
				 WHERE org_id = $1 AND (profile_id_1 = $2 OR profile_id_2 = $2)`)

// IRDeleteRejectionPairsForProfile removes every rejection that involves a profile.
var IRDeleteRejectionPairsForProfile = newQuery("CDS-IDR-21",
	`DELETE FROM rejection_pairs WHERE org_id = $1 AND (profile_id_1 = $2 OR profile_id_2 = $2)`)

// IRDeleteRejectionPairsForProfiles removes the rejections that involve a profile in the IN list %s.
var IRDeleteRejectionPairsForProfiles = newQuery("CDS-IDR-23",
	`DELETE FROM rejection_pairs WHERE org_id = $1 AND (profile_id_1 IN (%s) OR profile_id_2 IN (%s))`)

// IRRepointRejectionPairs moves a rejection from a profile that has become a child onto
// the master that now represents it, so the decision survives the merge.
var IRRepointRejectionPairs = newQuery("CDS-IDR-22",
	`UPDATE rejection_pairs
				 SET profile_id_1 = CASE WHEN profile_id_1 = $2 THEN $3 ELSE profile_id_1 END,
				     profile_id_2 = CASE WHEN profile_id_2 = $2 THEN $3 ELSE profile_id_2 END
				 WHERE org_id = $1 AND ($2 IN (profile_id_1, profile_id_2))
				   AND profile_id_1 != $3 AND profile_id_2 != $3`)
