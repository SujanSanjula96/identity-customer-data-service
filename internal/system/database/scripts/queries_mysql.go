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

package scripts

// mysqlStatements holds the MySQL text of every statement that cannot run on
// MySQL as it is written for PostgreSQL, keyed by statement id. newQuery adds
// each one to its statement as the MySQL override. A statement that is not
// listed here runs on MySQL with its PostgreSQL text.
var mysqlStatements = map[string]string{
	"CDS-APP-01": `INSERT INTO applications (app_id, org_id, client_id, updated_at)
		VALUES ($1, $2, $3, NOW(6))
		ON DUPLICATE KEY UPDATE
			org_id = VALUES(org_id),
			client_id = VALUES(client_id),
			updated_at = NOW(6)`,

	"CDS-CFG-02": `INSERT INTO cds_config (org_id, config, value)
		VALUES ($1, $2, $3)
		ON DUPLICATE KEY UPDATE value = VALUES(value)`,

	"CDS-CFG-04": `INSERT INTO cds_config (org_id, config, value)
		VALUES ($1, 'initial_schema_sync_done', $2)
		ON DUPLICATE KEY UPDATE value = VALUES(value)`,

	"CDS-CKI-08": `DELETE FROM profile_cookies WHERE is_active = false LIMIT $1`,

	"CDS-CON-05": `INSERT INTO consent_categories (category_name, category_identifier, org_id,
		purpose, destinations, is_mandatory)
		SELECT $1, $2, $3, $4, $5, TRUE FROM DUAL
		WHERE NOT EXISTS (
			SELECT 1 FROM consent_categories WHERE org_id = $3 AND is_mandatory = TRUE
		)`,

	"CDS-CON-12": `INSERT INTO consent_category_attributes (category_id, scope, attribute_name,
		attribute_id, application_identifier)
		VALUES ($1, $2, $3, $4, $5)
		ON DUPLICATE KEY UPDATE id = id`,

	"CDS-IDR-05": `INSERT INTO blocking_keys (profile_id, org_id, attribute_name, key_value)
		VALUES %s ON DUPLICATE KEY UPDATE profile_id = profile_id`,

	"CDS-IDR-08": `INSERT INTO review_tasks (id, org_id, incoming_profile_id, candidate_profile_id,
		match_score, status, score_breakdown, match_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON DUPLICATE KEY UPDATE
			match_score = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), VALUES(match_score), match_score),
			score_breakdown = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), VALUES(score_breakdown),
				score_breakdown),
			match_reason = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), VALUES(match_reason), match_reason),
			resolved_at = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), NULL, resolved_at),
			resolved_by = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), NULL, resolved_by),
			resolution_notes = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), NULL, resolution_notes),
			status = IF(status IN ('PENDING', 'CANCELLED', 'REJECTED'), VALUES(status), status)`,

	"CDS-IDR-19": `INSERT INTO rejection_pairs (id, org_id, profile_id_1, profile_id_2, match_score,
		score_breakdown, rejected_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON DUPLICATE KEY UPDATE
			match_score = VALUES(match_score),
			score_breakdown = VALUES(score_breakdown),
			rejected_by = VALUES(rejected_by)`,

	"CDS-ORG-01": `INSERT INTO organizations (org_id, org_handle, org_name, parent_org_id,
		primary_org_id, status, created_at,
		updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON DUPLICATE KEY UPDATE
			org_handle = VALUES(org_handle),
			org_name = VALUES(org_name),
			parent_org_id = VALUES(parent_org_id),
			primary_org_id = VALUES(primary_org_id),
			status = VALUES(status),
			updated_at = VALUES(updated_at)`,

	"CDS-PRF-01": `INSERT INTO profiles (
		profile_id, user_id, org_id, created_at, updated_at, list_profile, delete_profile, traits, identity_attributes
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON DUPLICATE KEY UPDATE profile_id = profile_id`,

	"CDS-PRF-02": `INSERT INTO profile_reference (profile_id, profile_status, reference_profile_id,
		reference_reason, org_id, reference_profile_org_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON DUPLICATE KEY UPDATE profile_id = profile_id`,

	"CDS-PRF-10": `SELECT
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
		LIMIT $5`,

	"CDS-PRF-12": `INSERT INTO application_data (profile_id, app_id, application_data)
		VALUES ($1, $2, $3)
		ON DUPLICATE KEY UPDATE application_data = VALUES(application_data)`,

	"CDS-PRF-20": `SELECT p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.org_id = $1 AND r.org_id = $1
	AND r.profile_status = 'REFERENCE_PROFILE'
	AND p.profile_id != $2
	AND ($3 = '' OR COALESCE(p.user_id, '') = '' OR p.user_id = $3)
	AND JSON_OVERLAPS(JSON_EXTRACT(%[1]s, $4), CAST($5 AS JSON))
	ORDER BY p.created_at, p.profile_id LIMIT 1`,

	"CDS-SCH-16": `SELECT attribute_id, attribute_name, scope, display_name, value_type, merge_strategy, mutability,
		application_identifier, multi_valued, sub_attributes, canonical_values, org_id
		FROM profile_schema WHERE attribute_id IN (%s)`,

	"CDS-SCH-02": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy ,
		application_identifier, mutability,
		multi_valued, sub_attributes, canonical_values FROM profile_schema WHERE org_id = $1`,

	"CDS-SCH-05": `INSERT INTO profile_schema
		(org_id, attribute_id, attribute_name, value_type, merge_strategy, mutability, application_identifier,
		 multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name) VALUES
		%s
		ON DUPLICATE KEY UPDATE
			attribute_name         = VALUES(attribute_name),
			value_type             = VALUES(value_type),
			merge_strategy         = VALUES(merge_strategy),
			mutability             = VALUES(mutability),
			application_identifier = VALUES(application_identifier),
			multi_valued           = VALUES(multi_valued),
			canonical_values       = VALUES(canonical_values),
			sub_attributes         = VALUES(sub_attributes),
			scim_dialect           = VALUES(scim_dialect),
			display_name           = VALUES(display_name)`,

	"CDS-SCH-06": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy,
		mutability, application_identifier,
		multi_valued, sub_attributes, canonical_values, scope FROM profile_schema WHERE org_id = $1
		AND attribute_name = $2 LIMIT 1`,

	"CDS-SCH-08": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy,
		mutability, application_identifier, multi_valued, sub_attributes,
		canonical_values FROM profile_schema WHERE org_id = $1 AND scope = $2`,

	"CDS-SCH-11": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy,
		mutability, application_identifier, multi_valued, sub_attributes,
		canonical_values, scope
		FROM profile_schema WHERE org_id = $1 AND attribute_id = $2`,

	"CDS-SCH-12": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy,
		mutability, application_identifier, multi_valued, sub_attributes,
		canonical_values FROM profile_schema WHERE org_id = $1`,

	"CDS-SHR-07": `INSERT INTO resource_sharing_policy_target (policy_id, target_scope, target_org_id) VALUES %s
		ON DUPLICATE KEY UPDATE policy_id = policy_id`,

	"CDS-SVC-02": `INSERT INTO org_service_credentials (org_id, client_id, client_secret, key_id)
		VALUES ($1, $2, $3, $4)
		ON DUPLICATE KEY UPDATE
			client_id = VALUES(client_id),
			client_secret = VALUES(client_secret),
			key_id = VALUES(key_id)`,
}
