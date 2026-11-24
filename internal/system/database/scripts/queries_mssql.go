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

// mssqlStatements holds the MSSQL text of every statement that cannot run on SQL
// Server as it is written for PostgreSQL, keyed by statement id. newQuery adds
// each one to its statement as the MSSQL override. A statement that is not
// listed here runs on SQL Server with its PostgreSQL text.
var mssqlStatements = map[string]string{
	"CDS-APP-01": `MERGE applications WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS app_id) AS s ON t.app_id = s.app_id
		WHEN MATCHED THEN UPDATE SET org_id = $2, client_id = $3, updated_at = SYSUTCDATETIME()
		WHEN NOT MATCHED THEN INSERT (app_id, org_id, client_id, updated_at)
			VALUES ($1, $2, $3, SYSUTCDATETIME());`,

	"CDS-APP-02": `SELECT TOP (1) app_id FROM applications
		WHERE org_id = $1 AND client_id = $2`,

	"CDS-CFG-02": `MERGE cds_config WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS org_id, $2 AS config) AS s ON t.org_id = s.org_id AND t.config = s.config
		WHEN MATCHED THEN UPDATE SET value = $3
		WHEN NOT MATCHED THEN INSERT (org_id, config, value) VALUES ($1, $2, $3);`,

	"CDS-CFG-04": `MERGE cds_config WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS org_id, 'initial_schema_sync_done' AS config) AS s
			ON t.org_id = s.org_id AND t.config = s.config
		WHEN MATCHED THEN UPDATE SET value = $2
		WHEN NOT MATCHED THEN INSERT (org_id, config, value) VALUES ($1, 'initial_schema_sync_done', $2);`,

	"CDS-CKI-08": `DELETE FROM profile_cookies WHERE cookie_id IN (
		SELECT TOP ($1) cookie_id FROM profile_cookies WHERE is_active = 0)`,

	"CDS-CON-05": `INSERT INTO consent_categories (category_name, category_identifier, org_id,
		purpose, destinations, is_mandatory)
		SELECT $1, $2, $3, $4, $5, 1
		WHERE NOT EXISTS (
			SELECT 1 FROM consent_categories WHERE org_id = $3 AND is_mandatory = 1
		)`,

	"CDS-CON-09": `SELECT category_identifier FROM consent_categories WHERE org_id = $1 AND is_mandatory = 1`,

	"CDS-CON-12": `MERGE consent_category_attributes WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS category_id, $2 AS scope, $3 AS attribute_name, $5 AS application_identifier) AS s
			ON t.category_id = s.category_id AND t.scope = s.scope AND t.attribute_name = s.attribute_name
			AND t.application_identifier = s.application_identifier
		WHEN NOT MATCHED THEN INSERT (category_id, scope, attribute_name, attribute_id, application_identifier)
			VALUES ($1, $2, $3, $4, $5);`,

	"CDS-IDR-03": `SELECT TOP ($3) profile_id, user_id, traits, identity_attributes
		FROM profiles
		WHERE org_id = $1 AND delete_profile = 0 AND profile_id > $2
		ORDER BY profile_id`,

	"CDS-IDR-04": `SELECT p.profile_id, p.user_id, p.traits, p.identity_attributes,
		pr.reference_profile_id
		FROM profiles p
		LEFT JOIN profile_reference pr ON p.profile_id = pr.profile_id
		WHERE p.profile_id IN (%s) AND p.delete_profile = 0`,

	"CDS-IDR-05": `MERGE blocking_keys WITH (HOLDLOCK) AS t
		USING (SELECT DISTINCT profile_id, org_id, attribute_name, key_value
			FROM (VALUES %s) AS v (profile_id, org_id, attribute_name, key_value)) AS s
			ON t.org_id = s.org_id AND t.attribute_name = s.attribute_name AND t.key_value = s.key_value
			AND t.profile_id = s.profile_id
		WHEN NOT MATCHED THEN INSERT (profile_id, org_id, attribute_name, key_value)
			VALUES (s.profile_id, s.org_id, s.attribute_name, s.key_value);`,

	"CDS-IDR-06": `SELECT TOP ($%[3]d) profile_id, key_value FROM blocking_keys
		WHERE org_id = $1 AND attribute_name = $2 AND key_value IN (%[1]s)
		AND profile_id != $%[2]d`,

	"CDS-IDR-08": `MERGE review_tasks WITH (HOLDLOCK) AS t
		USING (SELECT $3 AS incoming_profile_id, $4 AS candidate_profile_id) AS s
			ON t.incoming_profile_id = s.incoming_profile_id AND t.candidate_profile_id = s.candidate_profile_id
		WHEN MATCHED AND t.status IN ('PENDING', 'CANCELLED', 'REJECTED') THEN UPDATE SET
			match_score = $5, score_breakdown = $7, match_reason = $8, status = $6,
			resolved_at = NULL, resolved_by = NULL, resolution_notes = NULL
		WHEN NOT MATCHED THEN INSERT (id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
			score_breakdown, match_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8);`,

	"CDS-IDR-11": `UPDATE review_tasks
		SET status = $1, resolved_at = SYSUTCDATETIME(), resolved_by = $2, resolution_notes = $3
		WHERE id != $4 AND status = $5
		AND (incoming_profile_id IN ($6, $7) OR candidate_profile_id IN ($6, $7))`,

	"CDS-IDR-14": `SELECT TOP ($3) id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
		score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
		FROM review_tasks
		WHERE org_id = $1 AND status = $2
		ORDER BY created_at DESC`,

	"CDS-IDR-16": `SELECT TOP ($4) id, org_id, incoming_profile_id, candidate_profile_id, match_score, status,
		score_breakdown, match_reason, created_at, resolved_at, resolved_by, resolution_notes
		FROM review_tasks
		WHERE org_id = $1 AND status = $2
		AND (incoming_profile_id = $3)
		ORDER BY match_score DESC`,

	"CDS-IDR-18": `UPDATE review_tasks
		SET status = $1, resolved_at = SYSUTCDATETIME(), resolved_by = $2, resolution_notes = $3
		WHERE id = $4`,

	"CDS-IDR-19": `MERGE rejection_pairs WITH (HOLDLOCK) AS t
		USING (SELECT $3 AS profile_id_1, $4 AS profile_id_2) AS s
			ON t.profile_id_1 = s.profile_id_1 AND t.profile_id_2 = s.profile_id_2
		WHEN MATCHED THEN UPDATE SET match_score = $5, score_breakdown = $6, rejected_by = $7
		WHEN NOT MATCHED THEN INSERT (id, org_id, profile_id_1, profile_id_2, match_score,
			score_breakdown, rejected_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7);`,

	"CDS-ORG-01": `MERGE organizations WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS org_id) AS s ON t.org_id = s.org_id
		WHEN MATCHED THEN UPDATE SET org_handle = $2, org_name = $3, parent_org_id = $4, primary_org_id = $5,
			status = $6, updated_at = $7
		WHEN NOT MATCHED THEN INSERT (org_id, org_handle, org_name, parent_org_id, primary_org_id, status, created_at,
			updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7);`,

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

	"CDS-PRF-01": `MERGE profiles WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS profile_id) AS s ON t.profile_id = s.profile_id
		WHEN NOT MATCHED THEN INSERT (
			profile_id, user_id, org_id, created_at, updated_at, list_profile, delete_profile, traits,
				identity_attributes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);`,

	"CDS-PRF-02": `MERGE profile_reference WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS profile_id) AS s ON t.profile_id = s.profile_id
		WHEN NOT MATCHED THEN INSERT (profile_id, profile_status, reference_profile_id, reference_reason, org_id,
			reference_profile_org_id)
			VALUES ($1, $2, $3, $4, $5, $6);`,

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
		OFFSET 0 ROWS FETCH NEXT $5 ROWS ONLY`,

	"CDS-PRF-12": `MERGE application_data WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS profile_id, $2 AS app_id) AS s ON t.profile_id = s.profile_id AND t.app_id = s.app_id
		WHEN MATCHED THEN UPDATE SET application_data = $3
		WHEN NOT MATCHED THEN INSERT (profile_id, app_id, application_data) VALUES ($1, $2, $3);`,

	"CDS-PRF-18": `DELETE FROM review_tasks WHERE candidate_profile_id = $1;
		DELETE FROM profiles WHERE profile_id = $1`,

	"CDS-PRF-22": `SELECT TOP ($3) p.profile_id FROM profiles p WITH (UPDLOCK, ROWLOCK)
		JOIN profile_reference r ON r.profile_id = p.profile_id
		WHERE p.org_id = $1 AND p.updated_at < $2 AND (p.user_id IS NULL OR p.user_id = '')
		  AND r.profile_status = 'REFERENCE_PROFILE'
		  AND NOT EXISTS (SELECT 1 FROM profile_reference c WHERE c.reference_profile_id = p.profile_id)
		ORDER BY p.updated_at`,

	"CDS-PRF-19": `SELECT TOP (1) p.profile_id
		FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
		WHERE p.org_id = $1 AND r.org_id = $1
		AND r.profile_status = 'REFERENCE_PROFILE'
		AND p.profile_id != $2 AND p.user_id = $3
		ORDER BY p.created_at, p.profile_id`,

	"CDS-SCH-02": `SELECT attribute_id, attribute_name, display_name, value_type, merge_strategy ,
		application_identifier, mutability,
		multi_valued, sub_attributes, canonical_values FROM profile_schema WHERE org_id = $1`,

	"CDS-SCH-05": `MERGE profile_schema WITH (HOLDLOCK) AS t
		USING (VALUES %s) AS s (org_id, attribute_id, attribute_name, value_type, merge_strategy, mutability,
			application_identifier, multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name)
		ON t.attribute_id = s.attribute_id
		WHEN MATCHED THEN UPDATE SET
			attribute_name         = s.attribute_name,
			value_type             = s.value_type,
			merge_strategy         = s.merge_strategy,
			mutability             = s.mutability,
			application_identifier = s.application_identifier,
			multi_valued           = s.multi_valued,
			canonical_values       = s.canonical_values,
			sub_attributes         = s.sub_attributes,
			scim_dialect           = s.scim_dialect,
			display_name           = s.display_name
		WHEN NOT MATCHED THEN INSERT (org_id, attribute_id, attribute_name, value_type, merge_strategy, mutability,
			application_identifier, multi_valued, canonical_values, sub_attributes, scim_dialect, scope, display_name)
			VALUES (s.org_id, s.attribute_id, s.attribute_name, s.value_type, s.merge_strategy, s.mutability,
			s.application_identifier, s.multi_valued, s.canonical_values, s.sub_attributes, s.scim_dialect, s.scope,
			s.display_name);`,

	"CDS-SCH-06": `SELECT TOP (1) attribute_id, attribute_name, display_name, value_type, merge_strategy, mutability,
		application_identifier, multi_valued, sub_attributes, canonical_values, scope
		FROM profile_schema WHERE org_id = $1 AND attribute_name = $2`,

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

	"CDS-SHR-07": `MERGE resource_sharing_policy_target WITH (HOLDLOCK) AS t
		USING (SELECT DISTINCT policy_id, target_scope, target_org_id
			FROM (VALUES %s) AS v (policy_id, target_scope, target_org_id)) AS s
			ON t.policy_id = s.policy_id AND t.target_scope = s.target_scope AND t.target_org_id = s.target_org_id
		WHEN NOT MATCHED THEN INSERT (policy_id, target_scope, target_org_id)
			VALUES (s.policy_id, s.target_scope, s.target_org_id);`,

	"CDS-SVC-02": `MERGE org_service_credentials WITH (HOLDLOCK) AS t
		USING (SELECT $1 AS org_id) AS s ON t.org_id = s.org_id
		WHEN MATCHED THEN UPDATE SET client_id = $2, client_secret = $3, key_id = $4
		WHEN NOT MATCHED THEN INSERT (org_id, client_id, client_secret, key_id) VALUES ($1, $2, $3, $4);`,

	"CDS-SVC-03": `SELECT TOP (1) org_id FROM org_service_credentials`,
}
