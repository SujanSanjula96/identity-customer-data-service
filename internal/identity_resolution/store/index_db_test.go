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
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var indexOrgCounter atomic.Int64

type indexEnv struct {
	ctx   context.Context
	orgID string
	db    *sql.DB
}

func newIndexEnv(t *testing.T) *indexEnv {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	org := setup.TestOrg(t, fmt.Sprintf("acme-%d", indexOrgCounter.Add(1)))
	return &indexEnv{ctx: context.Background(), orgID: org.ID, db: testDB.DB}
}

// addProfile stores a master profile with a city and an email.
func (e *indexEnv) addProfile(t *testing.T, id, city, email string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, profileStore.InsertProfile(e.ctx, e.orgID, profileModel.Profile{
		ProfileID:          id,
		OrgID:              e.orgID,
		UserID:             "user-" + id,
		CreatedAt:          now,
		UpdatedAt:          now,
		Traits:             map[string]interface{}{"city": city},
		IdentityAttributes: map[string]interface{}{"email": email},
		ProfileStatus:      &profileModel.ProfileStatus{IsReferenceProfile: true, ListProfile: true},
	}))
}

func keys(attribute string, values ...string) []model.BlockingKey {
	out := make([]model.BlockingKey, 0, len(values))
	for _, value := range values {
		out = append(out, model.BlockingKey{AttributeName: attribute, KeyValue: value})
	}
	return out
}

func TestCandidatesAreFoundByTheirBlockingKeys(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-1", env.orgID, keys("traits.city", "colombo", "kandy")))
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-2", env.orgID, keys("traits.city", "colombo")))
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-3", env.orgID, keys("traits.city", "galle")))
	find := func(attribute string, values []string, exclude string, max int) []string {
		got, err := FindCandidateIDsByKeys(env.ctx, env.orgID, attribute, values, exclude, max)
		require.NoError(t, err)
		return got
	}

	assert.ElementsMatch(t, []string{"p-2"}, find("traits.city", []string{"colombo"}, "p-1", 10),
		"the profile that asks is not its own candidate")
	assert.ElementsMatch(t, []string{"p-1", "p-2"}, find("traits.city", []string{"colombo"}, "", 10))
	assert.ElementsMatch(t, []string{"p-1", "p-3"}, find("traits.city", []string{"kandy", "galle"}, "", 10))
	assert.Empty(t, find("identity_attributes.email", []string{"colombo"}, "", 10), "keys are per attribute")
	assert.Empty(t, find("traits.city", nil, "", 10))
	assert.Empty(t, find("traits.city", []string{"colombo"}, "", 1),
		"a value that more profiles share than the limit is too common to be evidence")

	other, err := FindCandidateIDsByKeys(env.ctx, "another-org", "traits.city", []string{"colombo"}, "", 10)
	require.NoError(t, err)
	assert.Empty(t, other, "keys are per organization")
}

func TestACandidateThatSharesManyKeysIsReturnedOnce(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-1", env.orgID, keys("traits.city", "a", "b", "c")))
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-2", env.orgID, keys("traits.city", "b")))

	got, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", []string{"a", "b", "c", "b"}, "", 2)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"p-1", "p-2"}, got)
}

func TestACrowdedKeyGroupIsSkippedUntilTheEntryExpires(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2", "p-3", "p-4"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		require.NoError(t, InsertBlockingKeys(env.ctx, id, env.orgID, keys("traits.city", "common")))
	}
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-4", env.orgID, keys("traits.city", "rare")))
	find := func(values ...string) []string {
		got, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", values, "", 2)
		require.NoError(t, err)
		return got
	}

	assert.Nil(t, find("rare", "common"), "three profiles share the group, which is more than the limit of two")
	assert.Equal(t, []string{"p-4"}, find("rare"), "a key that is not crowded by itself is still looked up")

	// The rows go away, but the node remembers the crowded key: no query runs for it.
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		require.NoError(t, DeleteBlockingKeys(env.ctx, env.orgID, id))
	}
	assert.Nil(t, find("common"), "the key that is crowded by itself is remembered")
	assert.Nil(t, find("common", "other"), "a group that holds a crowded key is skipped")

	crowdedKeys.Clear()
	assert.Empty(t, find("common"), "after the entry expires, the key is looked up again")
}

func TestDistinctCandidatesKeepsTheRowOrder(t *testing.T) {
	got, perKey := distinctCandidates([]map[string]interface{}{
		{"profile_id": "p-2", "key_value": "a"},
		{"profile_id": "p-1", "key_value": "a"},
		{"profile_id": "p-2", "key_value": "b"},
		{"profile_id": "", "key_value": "b"},
	})
	assert.Equal(t, []string{"p-2", "p-1"}, got)
	assert.Len(t, perKey["a"], 2)
	assert.Len(t, perKey["b"], 1)
}

func TestUpsertReplacesTheKeysOfAProfile(t *testing.T) {
	env := newIndexEnv(t)
	env.addProfile(t, "p-1", "Colombo", "a@b.c")
	require.NoError(t, UpsertBlockingKeys(env.ctx, "p-1", env.orgID, keys("traits.city", "old")))
	require.NoError(t, UpsertBlockingKeys(env.ctx, "p-1", env.orgID, keys("traits.city", "new")))

	old, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", []string{"old"}, "", 10)
	require.NoError(t, err)
	assert.Empty(t, old, "the previous keys are gone")
	current, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", []string{"new"}, "", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"p-1"}, current)

	require.NoError(t, UpsertBlockingKeys(env.ctx, "p-1", env.orgID, nil), "no keys changes nothing")
	still, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", []string{"new"}, "", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"p-1"}, still)
}

// countKeyWrites records each row that is inserted into or deleted from blocking_keys.
func (e *indexEnv) countKeyWrites(t *testing.T) func() (inserted, deleted int) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE key_writes (kind TEXT)`,
		`CREATE TRIGGER key_inserted AFTER INSERT ON blocking_keys BEGIN INSERT INTO key_writes VALUES ('i'); END`,
		`CREATE TRIGGER key_deleted AFTER DELETE ON blocking_keys BEGIN INSERT INTO key_writes VALUES ('d'); END`,
	} {
		_, err := e.db.Exec(statement)
		require.NoError(t, err)
	}
	return func() (int, int) {
		var inserted, deleted int
		require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM key_writes WHERE kind = 'i'`).Scan(&inserted))
		require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM key_writes WHERE kind = 'd'`).Scan(&deleted))
		_, err := e.db.Exec(`DELETE FROM key_writes`)
		require.NoError(t, err)
		return inserted, deleted
	}
}

func TestUpsertWritesOnlyTheKeysThatChanged(t *testing.T) {
	env := newIndexEnv(t)
	env.addProfile(t, "p-1", "Colombo", "a@b.c")
	writes := env.countKeyWrites(t)
	stored := func() []string {
		got, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", []string{"a", "b", "c"}, "", 10)
		require.NoError(t, err)
		return got
	}

	require.NoError(t, UpsertBlockingKeys(env.ctx, "p-1", env.orgID,
		append(keys("traits.city", "a", "b"), keys("identity_attributes.email", "a@b.c")...)))
	inserted, deleted := writes()
	assert.Equal(t, 3, inserted)
	assert.Equal(t, 0, deleted)

	require.NoError(t, UpsertBlockingKeys(env.ctx, "p-1", env.orgID,
		append(keys("traits.city", "b", "a", "a"), keys("identity_attributes.email", "a@b.c")...)))
	inserted, deleted = writes()
	assert.Zero(t, inserted, "the same keys write nothing")
	assert.Zero(t, deleted, "the same keys write nothing")

	require.NoError(t, UpsertBlockingKeys(env.ctx, "p-1", env.orgID,
		append(keys("traits.city", "b", "c"), keys("identity_attributes.email", "a@b.c")...)))
	inserted, deleted = writes()
	assert.Equal(t, 1, inserted, "only the new key is inserted")
	assert.Equal(t, 1, deleted, "only the key that went away is deleted")
	assert.Equal(t, []string{"p-1"}, stored())

	old, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "traits.city", []string{"a"}, "", 10)
	require.NoError(t, err)
	assert.Empty(t, old)
	email, err := FindCandidateIDsByKeys(env.ctx, env.orgID, "identity_attributes.email", []string{"a@b.c"}, "", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"p-1"}, email, "the keys of the other attribute stay")
}

func TestDiffBlockingKeys(t *testing.T) {
	stored := []blockingKeyID{{"traits.city", "a"}, {"traits.city", "b"}, {"traits.name", "a"}}
	toDelete, toInsert := diffBlockingKeys(stored, append(keys("traits.city", "b", "c", "c"),
		keys("traits.name", "a")...))

	assert.Equal(t, []blockingKeyID{{"traits.city", "a"}}, toDelete)
	assert.Equal(t, keys("traits.city", "c"), toInsert)

	toDelete, toInsert = diffBlockingKeys(nil, keys("traits.city", "a"))
	assert.Empty(t, toDelete)
	assert.Equal(t, keys("traits.city", "a"), toInsert)
}

func TestBlockingKeyDeletesAreSplitIntoStatements(t *testing.T) {
	many := make([]blockingKeyID, maxBlockingKeyDeletesPerStatement+1)
	for i := range many {
		many[i] = blockingKeyID{"traits.city", fmt.Sprint(i)}
	}
	deletes := buildBlockingKeyDeletes("p-1", "org-1", many)

	require.Len(t, deletes, 2)
	assert.Len(t, deletes[0].args, 2+2*maxBlockingKeyDeletesPerStatement)
	assert.Equal(t, []interface{}{"org-1", "p-1", "traits.city", fmt.Sprint(maxBlockingKeyDeletesPerStatement)},
		deletes[1].args)
	assert.Contains(t, deletes[1].query.Query, "(attribute_name = $3 AND key_value = $4)")
	assert.Empty(t, buildBlockingKeyDeletes("p-1", "org-1", nil))
}

func TestKeysAreInsertedInABatchAndDeleted(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	require.NoError(t, InsertBlockingKeysBatch(env.ctx, env.orgID, map[string][]model.BlockingKey{
		"p-1": append(keys("traits.city", "colombo"), keys("identity_attributes.email", "p-1@b.c")...),
		"p-2": keys("traits.city", "colombo"),
	}))
	require.NoError(t, InsertBlockingKeysBatch(env.ctx, env.orgID, nil), "an empty batch is fine")
	count := func(attribute, value string) int {
		got, err := CountProfilesByBlockingKey(env.ctx, env.orgID, attribute, value)
		require.NoError(t, err)
		return got
	}

	assert.Equal(t, 2, count("traits.city", "colombo"))
	assert.Equal(t, 1, count("identity_attributes.email", "p-1@b.c"))
	assert.Equal(t, 0, count("traits.city", "nowhere"))
}

func TestBlockingKeysCanBeDeletedForAProfileOrAnAttribute(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-1", env.orgID,
		append(keys("traits.city", "delete-me"), keys("identity_attributes.email", "keep-me")...)))
	require.NoError(t, InsertBlockingKeys(env.ctx, "p-2", env.orgID, keys("traits.city", "delete-me")))
	find := func(attribute, value string) []string {
		got, err := FindCandidateIDsByKeys(env.ctx, env.orgID, attribute, []string{value}, "", 10)
		require.NoError(t, err)
		return got
	}

	require.NoError(t, DeleteBlockingKeys(env.ctx, env.orgID, "p-2"))
	assert.Equal(t, []string{"p-1"}, find("traits.city", "delete-me"))

	require.NoError(t, DeleteBlockingKeysByAttribute(env.ctx, env.orgID, "traits.city"))
	assert.Empty(t, find("traits.city", "delete-me"))
	assert.Equal(t, []string{"p-1"}, find("identity_attributes.email", "keep-me"), "other attributes are kept")
}

func TestProfilesAreReadAsFlatAttributes(t *testing.T) {
	env := newIndexEnv(t)
	env.addProfile(t, "p-1", "Colombo", "a@b.c")
	env.addProfile(t, "p-2", "Kandy", "b@b.c")

	got, err := GetProfilesByIDs(env.ctx, []string{"p-1", "p-2", "missing"})

	require.NoError(t, err)
	require.Len(t, got, 2)
	byID := map[string]model.ProfileData{}
	for _, profile := range got {
		byID[profile.ProfileID] = profile
	}
	assert.Equal(t, "Colombo", byID["p-1"].Attributes["traits.city"])
	assert.Equal(t, "a@b.c", byID["p-1"].Attributes["identity_attributes.email"])
	assert.Equal(t, "user-p-1", byID["p-1"].UserID)
	first := byID["p-1"]
	assert.False(t, first.IsChild())

	none, err := GetProfilesByIDs(env.ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestProfilesOfAnOrganizationArePagedByID(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2", "p-3", "p-4"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	ids := func(after string, limit int) []string {
		got, err := GetProfilesForOrgAfter(env.ctx, env.orgID, after, limit)
		require.NoError(t, err)
		out := make([]string, 0, len(got))
		for _, profile := range got {
			out = append(out, profile.ProfileID)
		}
		return out
	}

	assert.Equal(t, []string{"p-1", "p-2"}, ids("", 2))
	assert.Equal(t, []string{"p-3", "p-4"}, ids("p-2", 2))
	assert.Empty(t, ids("p-4", 2))

	other, err := GetProfilesForOrgAfter(env.ctx, "another-org", "", 10)
	require.NoError(t, err)
	assert.Empty(t, other, "profiles are per organization")
}

func TestARejectionIsMovedToTheMaster(t *testing.T) {
	env := newIndexEnv(t)
	require.NoError(t, InsertRejectionPair(env.ctx, env.orgID, "p-2", "p-1", "admin", 0.8,
		map[string]float64{"traits.city": 0.8}))

	require.NoError(t, RepointRejectionPairs(env.ctx, env.orgID, "p-2", "master"))

	moved, err := GetRejectionsForProfile(env.ctx, env.orgID, "master")
	require.NoError(t, err)
	require.Contains(t, moved, "p-1")
	assert.InDelta(t, 0.8, moved["p-1"].MatchScore, 1e-9)
	assert.Equal(t, map[string]float64{"traits.city": 0.8}, moved["p-1"].ScoreBreakdown)
	old, err := GetRejectionsForProfile(env.ctx, env.orgID, "p-2")
	require.NoError(t, err)
	assert.Empty(t, old)

	for _, args := range [][2]string{{"", "master"}, {"p-2", ""}, {"p-1", "p-1"}} {
		assert.NoError(t, RepointRejectionPairs(env.ctx, env.orgID, args[0], args[1]), "nothing to move: %v", args)
	}
}

func TestReviewTasksAreListedNewestFirstAndKeepTheirExplanation(t *testing.T) {
	env := newIndexEnv(t)
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		env.addProfile(t, id, "Colombo", id+"@b.c")
	}
	low := model.ReviewTask{ID: "task-low", IncomingProfileID: "p-1", CandidateProfileID: "p-2", MatchScore: 0.6,
		Status: constants.ReviewStatusPending, ScoreBreakdown: map[string]float64{"a": 0.6}}
	high := model.ReviewTask{ID: "task-high", IncomingProfileID: "p-1", CandidateProfileID: "p-3", MatchScore: 0.9,
		Status: constants.ReviewStatusPending, ScoreBreakdown: map[string]float64{"a": 0.9},
		Explanation: &model.MatchExplanation{Reasons: []model.ReviewReason{{Code: "BELOW_AUTO_MERGE_THRESHOLD"}}}}
	require.NoError(t, InsertReviewTask(env.ctx, env.orgID, low))
	require.NoError(t, InsertReviewTask(env.ctx, env.orgID, high))

	// created_at holds whole seconds here, so both tasks would tie. Backdating the
	// stronger match makes the weaker one the newest: newest first and strongest
	// first then give different answers.
	_, err := env.db.ExecContext(env.ctx,
		`UPDATE review_tasks SET created_at = datetime(created_at, '-1 hour') WHERE id = 'task-high'`)
	require.NoError(t, err)

	tasks, _, err := GetPendingReviewTasks(env.ctx, env.orgID, 10)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	assert.Equal(t, []string{"task-low", "task-high"}, []string{tasks[0].ID, tasks[1].ID},
		"the newest task comes first")

	stored, err := GetReviewTaskByID(env.ctx, "task-high")
	require.NoError(t, err)
	require.NotNil(t, stored.Explanation)
	require.Len(t, stored.Explanation.Reasons, 1)
	assert.Equal(t, "BELOW_AUTO_MERGE_THRESHOLD", stored.Explanation.Reasons[0].Code)
	plain, err := GetReviewTaskByID(env.ctx, "task-low")
	require.NoError(t, err)
	assert.Nil(t, plain.Explanation, "a task without an explanation has none")
	missing, err := GetReviewTaskByID(env.ctx, "missing")
	require.NoError(t, err)
	assert.Nil(t, missing)

	limited, _, err := GetPendingReviewTasks(env.ctx, env.orgID, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestScanScoreReadsEveryNumericForm(t *testing.T) {
	assert.Equal(t, 0.5, scanScore(0.5))
	assert.Equal(t, 2.0, scanScore(int64(2)))
	assert.Equal(t, 0.25, scanScore([]byte("0.25")))
	assert.Equal(t, 0.75, scanScore("0.75"))
	assert.Equal(t, 0.0, scanScore("not a number"))
	assert.Equal(t, 0.0, scanScore([]byte("nope")))
	assert.Equal(t, 0.0, scanScore(nil))
	assert.Equal(t, 0.0, scanScore(true))
}

func TestExplanationIsStoredAsJSONOrNull(t *testing.T) {
	assert.Nil(t, explanationJSON(nil))
	encoded, ok := explanationJSON(&model.MatchExplanation{Reasons: []model.ReviewReason{{Code: "X"}}}).(string)
	require.True(t, ok)
	assert.Contains(t, encoded, `"code":"X"`)
}
