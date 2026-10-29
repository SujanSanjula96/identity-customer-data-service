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

// Statements that read the PostgreSQL catalog, for the start-up check of the org_id_migration
// phase. They run on PostgreSQL only; the inbuilt database migrates at start-up instead.

// GetOrgColumns returns the org columns of the tables $1 (a text array), with their nullability.
var GetOrgColumns = newQuery("CDS-SYS-02",
	`SELECT table_name, column_name, is_nullable
	FROM information_schema.columns
	WHERE table_schema = current_schema() AND table_name = ANY($1)
	AND column_name IN ('org_handle', 'org_id', 'reference_profile_org_handle', 'reference_profile_org_id')`)

// GetValidIndexes returns the valid indexes of the tables $1 (a text array), each with its columns
// in order. An index that a failed CREATE INDEX CONCURRENTLY left invalid is not returned.
var GetValidIndexes = newQuery("CDS-SYS-03",
	`SELECT t.relname AS table_name, i.relname AS index_name, ix.indisunique AS is_unique,
		ix.indisprimary AS is_primary, string_agg(a.attname, ',' ORDER BY k.ord) AS columns
	FROM pg_index ix
	JOIN pg_class i ON i.oid = ix.indexrelid
	JOIN pg_class t ON t.oid = ix.indrelid
	JOIN pg_namespace n ON n.oid = t.relnamespace
	CROSS JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord)
	JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
	WHERE n.nspname = current_schema() AND t.relname = ANY($1) AND ix.indisvalid
	GROUP BY t.relname, i.relname, ix.indisunique, ix.indisprimary`)
