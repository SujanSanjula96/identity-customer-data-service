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

// oracleNow is the current time in UTC
const oracleNow = "FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC')"

// oracleStatements contains statements that need Oracle-specific versions.
var oracleStatements = map[string]string{
	"CDS-APP-01": `MERGE INTO applications t
		USING (SELECT $1 AS app_id FROM dual) s ON (t.app_id = s.app_id)
		WHEN MATCHED THEN UPDATE SET org_id = $2, client_id = $3, updated_at = ` + oracleNow + `
		WHEN NOT MATCHED THEN INSERT (app_id, org_id, client_id, updated_at)
			VALUES ($1, $2, $3, ` + oracleNow + `)`,

	"CDS-APP-02": `SELECT app_id FROM applications
		WHERE org_id = $1 AND client_id = $2 FETCH FIRST 1 ROWS ONLY`,

	"CDS-CFG-02": `MERGE INTO cds_config t
		USING (SELECT $1 AS org_id, $2 AS config FROM dual) s ON (t.org_id = s.org_id AND t.config = s.config)
		WHEN MATCHED THEN UPDATE SET value = $3
		WHEN NOT MATCHED THEN INSERT (org_id, config, value) VALUES ($1, $2, $3)`,

	"CDS-CFG-04": `MERGE INTO cds_config t
		USING (SELECT $1 AS org_id, 'initial_schema_sync_done' AS config FROM dual) s
			ON (t.org_id = s.org_id AND t.config = s.config)
		WHEN MATCHED THEN UPDATE SET value = $2
		WHEN NOT MATCHED THEN INSERT (org_id, config, value) VALUES ($1, 'initial_schema_sync_done', $2)`,

	"CDS-CKI-08": `DELETE FROM profile_cookies WHERE cookie_id IN (
		SELECT cookie_id FROM profile_cookies WHERE is_active = 0 FETCH FIRST $1 ROWS ONLY)`,

	"CDS-CON-05": `INSERT INTO consent_categories (category_name, category_identifier, org_id,
		purpose, destinations, is_mandatory)
		SELECT $1, $2, $3, $4, $5, 1 FROM dual
		WHERE NOT EXISTS (
			SELECT 1 FROM consent_categories WHERE org_id = $3 AND is_mandatory = 1
		)`,

	"CDS-CON-09": `SELECT category_identifier FROM consent_categories WHERE org_id = $1 AND is_mandatory = 1`,

	"CDS-CON-12": `MERGE INTO consent_category_attributes t
		USING (SELECT $1 AS category_id, $2 AS scope, $3 AS attribute_name, $5 AS application_identifier
			FROM dual) s
			ON (t.category_id = s.category_id AND t.scope = s.scope AND t.attribute_name = s.attribute_name
			AND (t.application_identifier = s.application_identifier
				OR (t.application_identifier IS NULL AND s.application_identifier IS NULL)))
		WHEN NOT MATCHED THEN INSERT (category_id, scope, attribute_name, attribute_id, application_identifier)
			VALUES ($1, $2, $3, $4, $5)`,

	"CDS-IDR-03": `SELECT profile_id, user_id, traits, identity_attributes
		FROM profiles
		WHERE org_id = $1 AND delete_profile = 0 AND ($2 IS NULL OR profile_id > $2)
		ORDER BY profile_id
		FETCH FIRST $3 ROWS ONLY`,

	"CDS-IDR-04": `SELECT p.profile_id, p.user_id, p.traits, p.identity_attributes,
		pr.reference_profile_id
		FROM profiles p
		LEFT JOIN profile_reference pr ON p.profile_id = pr.profile_id
		WHERE p.profile_id IN (%s) AND p.delete_profile = 0`,

	"CDS-IDR-05": `INSERT /*+ IGNORE_ROW_ON_DUPKEY_INDEX(blocking_keys pk_blocking_keys) */
		INTO blocking_keys (profile_id, org_id, attribute_name, key_value)
		VALUES %s`,

	"CDS-IDR-06": `SELECT profile_id, key_value FROM blocking_keys
		WHERE org_id = $1 AND attribute_name = $2 AND key_value IN (%[1]s)
		AND ($%[2]d IS NULL OR profile_id != $%[2]d) FETCH FIRST $%[3]d ROWS ONLY`,

	"CDS-IDR-07": `SELECT COUNT(DISTINCT profile_id) AS profile_count FROM blocking_keys
		WHERE org_id = $1 AND attribute_name = $2 AND key_value = $3`,

	"CDS-IDR-08": `MERGE INTO review_tasks t
		USING (SELECT $3 AS incoming_profile_id, $4 AS candidate_profile_id FROM dual) s
			ON (t.incoming_profile_id = s.incoming_profile_id AND t.candidate_profile_id = s.candidate_profile_id)
		WHEN MATCHED THEN UPDATE SET
			match_score = $5, score_breakdown = $7, match_reason = $8, status = $6,
			resolved_at = NULL, resolved_by = NULL, resolution_notes = NULL
			WHERE t.status IN ('PENDING', 'CANCELLED', 'REJECTED')
		WHEN NOT MATCHED THEN INSERT (id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
			score_breakdown, match_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,

	"CDS-IDR-09": `SELECT COUNT(*) AS "count" FROM review_tasks
		WHERE incoming_profile_id = $1 AND candidate_profile_id = $2 AND status = $3`,

	"CDS-IDR-11": `UPDATE review_tasks
		SET status = $1, resolved_at = ` + oracleNow + `, resolved_by = $2, resolution_notes = $3
		WHERE id != $4 AND status = $5
		AND (incoming_profile_id IN ($6, $7) OR candidate_profile_id IN ($6, $7))`,

	"CDS-IDR-14": `SELECT id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
		score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
		FROM review_tasks
		WHERE org_id = $1 AND status = $2
		ORDER BY created_at DESC
		FETCH FIRST $3 ROWS ONLY`,

	"CDS-IDR-15": `SELECT COUNT(*) AS "count" FROM review_tasks WHERE org_id = $1 AND status = $2`,

	"CDS-IDR-16": `SELECT id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
		score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
		FROM review_tasks
		WHERE org_id = $1 AND status = $2
		AND (incoming_profile_id = $3)
		ORDER BY match_score DESC
		FETCH FIRST $4 ROWS ONLY`,

	"CDS-IDR-17": `SELECT COUNT(*) AS "count" FROM review_tasks WHERE org_id = $1 AND status = $2
		AND (incoming_profile_id = $3 OR candidate_profile_id = $3)`,

	"CDS-IDR-18": `UPDATE review_tasks
		SET status = $1, resolved_at = ` + oracleNow + `, resolved_by = $2, resolution_notes = $3
		WHERE id = $4`,

	"CDS-IDR-19": `MERGE INTO rejection_pairs t
		USING (SELECT $3 AS profile_id_1, $4 AS profile_id_2 FROM dual) s
			ON (t.profile_id_1 = s.profile_id_1 AND t.profile_id_2 = s.profile_id_2)
		WHEN MATCHED THEN UPDATE SET match_score = $5, score_breakdown = $6, rejected_by = $7
		WHEN NOT MATCHED THEN INSERT (id, org_id, profile_id_1, profile_id_2, match_score,
			score_breakdown, rejected_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,

	"CDS-ORG-01": `MERGE INTO organizations t
		USING (SELECT $1 AS org_id FROM dual) s ON (t.org_id = s.org_id)
		WHEN MATCHED THEN UPDATE SET org_handle = $2, org_name = $3, parent_org_id = $4, primary_org_id = $5,
			status = $6, updated_at = $7
		WHEN NOT MATCHED THEN INSERT (org_id, org_handle, org_name, parent_org_id, primary_org_id, status, created_at,
			updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,

	"CDS-ORG-07": `WITH chain (org_id, parent_org_id, hops) AS (
		SELECT org_id, parent_org_id, 0 FROM organizations WHERE org_id = $1
		UNION ALL
		SELECT o.org_id, o.parent_org_id, c.hops + 1 FROM organizations o JOIN chain c ON o.org_id = c.parent_org_id
		WHERE c.hops < 64)
		SELECT o.org_id, o.org_handle, o.org_name, o.parent_org_id, o.primary_org_id, o.status,
		o.created_at, o.updated_at FROM organizations o JOIN chain c ON c.org_id = o.org_id
		ORDER BY c.hops DESC`,

	"CDS-ORG-09": `WITH chain (org_id, parent_org_id, hops) AS (
		SELECT org_id, parent_org_id, 0 FROM organizations WHERE org_id = $1
		UNION ALL
		SELECT o.org_id, o.parent_org_id, c.hops + 1 FROM organizations o JOIN chain c ON o.org_id = c.parent_org_id
		WHERE c.hops < 64)
		SELECT o.org_id, o.org_handle, o.org_name, o.parent_org_id, o.primary_org_id, o.status,
		o.created_at, o.updated_at, c.hops FROM organizations o JOIN chain c ON c.org_id = o.org_id
		WHERE c.hops > 0 ORDER BY c.hops`,

	"CDS-PRF-01": `MERGE INTO profiles t
		USING (SELECT $1 AS profile_id FROM dual) s ON (t.profile_id = s.profile_id)
		WHEN NOT MATCHED THEN INSERT (
			profile_id, user_id, org_id, created_at, updated_at, list_profile, delete_profile, traits,
				identity_attributes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,

	"CDS-PRF-02": `MERGE INTO profile_reference t
		USING (SELECT $1 AS profile_id FROM dual) s ON (t.profile_id = s.profile_id)
		WHEN NOT MATCHED THEN INSERT (profile_id, profile_status, reference_profile_id, reference_reason, org_id,
			reference_profile_org_id)
			VALUES ($1, $2, $3, $4, $5, $6)`,

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
					($4 = 'next' AND (p.created_at < $2 OR (p.created_at = $2 AND p.profile_id < $3)))
					OR
					($4 = 'prev' AND (p.created_at > $2 OR (p.created_at = $2 AND p.profile_id > $3)))
				)
			)
		ORDER BY
			CASE WHEN $4 = 'prev' THEN p.created_at END ASC,
			CASE WHEN $4 = 'prev' THEN p.profile_id END ASC,
			CASE WHEN $4 <> 'prev' THEN p.created_at END DESC,
			CASE WHEN $4 <> 'prev' THEN p.profile_id END DESC
		FETCH FIRST $5 ROWS ONLY`,

	"CDS-PRF-12": `MERGE INTO application_data t
		USING (SELECT $1 AS profile_id, $2 AS app_id FROM dual) s
			ON (t.profile_id = s.profile_id AND t.app_id = s.app_id)
		WHEN MATCHED THEN UPDATE SET application_data = $3
		WHEN NOT MATCHED THEN INSERT (profile_id, app_id, application_data) VALUES ($1, $2, $3)`,

	"CDS-PRF-14": `SELECT p.profile_id,
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
    ON p.profile_id = r.profile_id`,

	"CDS-PRF-19": `SELECT p.profile_id
		FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
		WHERE p.org_id = $1 AND r.org_id = $1
		AND r.profile_status = 'REFERENCE_PROFILE'
		AND ($2 IS NULL OR p.profile_id != $2) AND p.user_id = $3
		ORDER BY p.created_at, p.profile_id
		FETCH FIRST 1 ROWS ONLY`,

	"CDS-PRF-22": `SELECT p.profile_id FROM profiles p
		WHERE p.profile_id IN (
			SELECT x.profile_id FROM profiles x
			JOIN profile_reference r ON r.profile_id = x.profile_id
			WHERE x.org_id = $1 AND x.updated_at < $2 AND x.user_id IS NULL
			  AND r.profile_status = 'REFERENCE_PROFILE'
			  AND NOT EXISTS (SELECT 1 FROM profile_reference c WHERE c.reference_profile_id = x.profile_id)
			ORDER BY x.updated_at
			FETCH FIRST $3 ROWS ONLY)
		FOR UPDATE OF p.profile_id SKIP LOCKED`,

	"CDS-SCH-05": `MERGE INTO profile_schema t
		USING (SELECT 1 AS one FROM dual) s ON (t.attribute_id = $2)
		WHEN MATCHED THEN UPDATE SET
			attribute_name         = $3,
			value_type             = $4,
			merge_strategy         = $5,
			mutability             = $6,
			application_identifier = $7,
			multi_valued           = $8,
			canonical_values       = $9,
			sub_attributes         = $10,
			scim_dialect           = $11,
			display_name           = $13
		WHEN NOT MATCHED THEN INSERT (org_id, attribute_id, attribute_name, value_type, merge_strategy, mutability,
			application_identifier, multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name)
			VALUES %s`,

	"CDS-SCH-02": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy ,
		application_identifier, mutability,
		multi_valued, sub_attributes, canonical_values FROM profile_schema WHERE org_id = $1`,

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

	"CDS-SCH-16": `SELECT attribute_id, attribute_name, scope, display_name, value_type, merge_strategy, mutability,
		application_identifier, multi_valued, sub_attributes, canonical_values, org_id
		FROM profile_schema WHERE attribute_id IN (%s)`,

	"CDS-SCH-06": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability,
		application_identifier, multi_valued, sub_attributes, canonical_values, scope
		FROM profile_schema WHERE org_id = $1 AND attribute_name = $2 FETCH FIRST 1 ROWS ONLY`,

	"CDS-SHR-07": `INSERT /*+ IGNORE_ROW_ON_DUPKEY_INDEX(resource_sharing_policy_target
		pk_resource_sharing_policy_target) */
		INTO resource_sharing_policy_target (policy_id, target_scope, target_org_id) VALUES %s`,

	"CDS-SVC-02": `MERGE INTO org_service_credentials t
		USING (SELECT $1 AS org_id FROM dual) s ON (t.org_id = s.org_id)
		WHEN MATCHED THEN UPDATE SET client_id = $2, client_secret = $3, key_id = $4
		WHEN NOT MATCHED THEN INSERT (org_id, client_id, client_secret, key_id) VALUES ($1, $2, $3, $4)`,

	"CDS-SVC-03": `SELECT org_id FROM org_service_credentials FETCH FIRST 1 ROWS ONLY`,

	"CDS-SYS-01": `SELECT 1 FROM dual`,
}
