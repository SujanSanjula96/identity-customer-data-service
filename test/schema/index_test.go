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

package schema

import (
	"regexp"
	"testing"
)

// candidateIndex matches the index that serves the candidate half of the review task lookups,
// "incoming_profile_id IN (...) OR candidate_profile_id IN (...)".
var candidateIndex = regexp.MustCompile(
	`(?i)idx_review_tasks_candidate\b[^;]*\(\s*candidate_profile_id\s*,\s*status\s*\)`)

// TestEverySchemaIndexesTheReviewTaskCandidate keeps the index in the schema of every datasource and on the
// PostgreSQL upgrade path. Without it, the OR of the review task lookups reads the whole table.
func TestEverySchemaIndexesTheReviewTaskCandidate(t *testing.T) {
	for _, path := range []string{
		installSchema,
		sqliteSchema,
		testSchema,
		"../../dbscripts/mysql.sql",
		"../../dbscripts/mssql.sql",
		"../../dbscripts/oracle.sql",
		"../../dbscripts/migrations/004-review-task-candidate-index-postgres.sql",
	} {
		if !candidateIndex.MatchString(read(t, path)) {
			t.Errorf("%s has no idx_review_tasks_candidate on (candidate_profile_id, status)", path)
		}
	}
}
