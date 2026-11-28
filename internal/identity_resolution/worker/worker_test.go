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

package worker

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

const testOrgID = "org-id-tenant"

// reviewTaskDB starts an SQLite database with profiles a, b and c, all reference profiles.
func reviewTaskDB(t *testing.T) *sql.DB {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	db, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(db.DB, database.TypeSQLite)
	t.Cleanup(func() { provider.SetTestDB(nil, ""); db.Terminate() })
	for _, id := range []string{"a", "b", "c"} {
		_, err := db.DB.Exec(`INSERT INTO profiles (profile_id, org_id, user_id, identity_attributes, location)
			VALUES (?, ?, '', '{}', '')`, id, testOrgID)
		require.NoError(t, err)
		_, err = db.DB.Exec(`INSERT INTO profile_reference (profile_id, org_id, profile_status, reference_reason)
			VALUES (?, ?, 'REFERENCE_PROFILE', '')`, id, testOrgID)
		require.NoError(t, err)
	}
	return db.DB
}

// reviewTaskCount returns the number of review tasks.
func reviewTaskCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM review_tasks`).Scan(&count))
	return count
}

func TestInsertReviewTaskRaisesTheTask(t *testing.T) {
	db := reviewTaskDB(t)

	require.NoError(t, insertReviewTask(context.Background(), testOrgID, "a",
		scoredCandidate{id: "b", similarity: 0.8}, 0.75, nil))

	require.Equal(t, 1, reviewTaskCount(t, db))
}

// TestInsertReviewTaskForAProfileMergedSinceItWasScored covers a merge that commits between scoring a
// pair and raising its task. The merge had no task to cancel, and the merged profile is never
// evaluated again, so the task is not raised.
func TestInsertReviewTaskForAProfileMergedSinceItWasScored(t *testing.T) {
	for name, merged := range map[string]string{"incoming": "a", "candidate": "b"} {
		t.Run(name, func(t *testing.T) {
			db := reviewTaskDB(t)
			_, err := db.Exec(`UPDATE profile_reference SET profile_status = 'MERGED_TO',
				reference_profile_id = 'c' WHERE profile_id = ?`, merged)
			require.NoError(t, err)

			require.NoError(t, insertReviewTask(context.Background(), testOrgID, "a",
				scoredCandidate{id: "b", similarity: 0.8}, 0.75, nil))

			require.Zero(t, reviewTaskCount(t, db))
		})
	}
}
