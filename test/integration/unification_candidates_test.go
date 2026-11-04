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

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
)

func insertCandidateFixture(t *testing.T, id, org, userID, status, attributes string, created time.Time) {
	t.Helper()
	args := []interface{}{id, org, userID, attributes, created}
	if suiteDBType == database.TypeSQLite {
		args = database.NormalizeSQLiteArgs(args)
	}
	_, err := suiteDB.Exec(`INSERT INTO profiles
		(profile_id, org_handle, user_id, identity_attributes, traits, created_at)
		VALUES ($1, $2, $3, $4, $4, $5)`, args...)
	require.NoError(t, err)
	_, err = suiteDB.Exec(`INSERT INTO profile_reference (profile_id, org_handle, profile_status)
		VALUES ($1, $2, $3)`, id, org, status)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = suiteDB.Exec(`DELETE FROM profile_reference WHERE profile_id = $1`, id)
		_, _ = suiteDB.Exec(`DELETE FROM profiles WHERE profile_id = $1`, id)
	})
}

func findReferenceProfileCandidate(ctx context.Context, incoming profileModel.Profile,
	property string, values []string) (string, error) {
	if property == "" {
		return profileStore.FindOldestReferenceProfileIDByUserID(ctx, incoming.OrgHandle, incoming.UserId,
			incoming.ProfileId)
	}
	return profileStore.FindOldestReferenceProfileIDByAttributeValues(ctx, incoming.OrgHandle, property, values,
		incoming.ProfileId, incoming.UserId)
}

// Runs on both CI datasources: exact string comparisons must agree even for
// arrays, nested attributes, JSON metacharacters and non-string values.
func Test_UnificationCandidateMatching(t *testing.T) {
	cases := []struct {
		name, attributes, property string
		values                     []string
		match                      bool
	}{
		{"scalar", `{"email":"a@example.com"}`, "identity_attributes.email", []string{"a@example.com"}, true},
		{"array overlap", `{"email":["b","a"]}`, "identity_attributes.email", []string{"x", "a"}, true},
		{"case sensitive", `{"email":"A"}`, "identity_attributes.email", []string{"a"}, false},
		{"empty string", `{"email":""}`, "identity_attributes.email", []string{""}, true},
		{"missing", `{}`, "identity_attributes.email", []string{""}, false},
		{"null", `{"email":null}`, "identity_attributes.email", []string{"null"}, false},
		{"number", `{"email":123}`, "identity_attributes.email", []string{"123"}, false},
		{"boolean", `{"email":true}`, "identity_attributes.email", []string{"true"}, false},
		{"object leaf", `{"email":{"value":"a"}}`, "identity_attributes.email", []string{"a"}, false},
		{"mixed array", `{"email":[123,null,true,"a"]}`, "identity_attributes.email", []string{"a"}, true},
		{"nested object", `{"contact":{"email":["a","b"]}}`, "identity_attributes.contact.email", []string{"b"}, true},
		{"nested object array", `{"contact":[{"email":"a"},{"email":["b"]}]}`, "identity_attributes.contact.email", []string{"b"}, true},
		{"array of arrays before member", `{"contact":[[{"email":"a"}]]}`, "identity_attributes.contact.email", []string{"a"}, false},
		{"multiple nested objects", `{"contact":{"details":{"email":["a"]}}}`, "identity_attributes.contact.details.email", []string{"a"}, true},
		{"empty array", `{"email":[]}`, "identity_attributes.email", []string{""}, false},
		{"wrong nested key", `{"other":{"email":"a"}}`, "identity_attributes.contact.email", []string{"a"}, false},
		{"nested array leaf", `{"email":[["a"]]}`, "identity_attributes.email", []string{"a"}, false},
		{"traits", `{"tag":["a"]}`, "traits.tag", []string{"a"}, true},
		{"quoted key", `{"a\"b":"x"}`, "traits.a\"b", []string{"x"}, true},
		{"literal wildcard key", `{"*":"x","other":"y"}`, "traits.*", []string{"y"}, false},
		{"quoted value", `{"email":"x\" || @ == \"y"}`, "identity_attributes.email", []string{`x" || @ == "y`}, true},
		{"SQL-like value", `{"email":"x' OR 1=1 --"}`, "identity_attributes.email", []string{"x' OR 1=1 --"}, true},
		{"no incoming strings", `{"email":"a"}`, "identity_attributes.email", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			org, id := uuid.NewString(), uuid.NewString()
			insertCandidateFixture(t, id, org, "", "REFERENCE_PROFILE", tc.attributes, time.Now().UTC())
			incoming := profileModel.Profile{ProfileId: "incoming", OrgHandle: org}
			got, err := findReferenceProfileCandidate(context.Background(), incoming, tc.property, tc.values)
			require.NoError(t, err)
			if tc.match {
				require.Equal(t, id, got)
			} else {
				require.Empty(t, got)
			}
		})
	}
}

func Test_UnificationCandidateScopeAndOrder(t *testing.T) {
	org := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	ids := map[string]string{}
	for _, name := range []string{"self", "child", "other-org", "newer", "old-b", "old-a"} {
		id, tenant, status, created := org+"-"+name, org, "REFERENCE_PROFILE", now.Add(-time.Hour)
		if name == "other-org" {
			tenant = org + "-other"
		}
		if name == "child" {
			status = "MERGED_TO"
		}
		if name == "newer" {
			created = now
		}
		insertCandidateFixture(t, id, tenant, "same-user", status, `{"email":"shared"}`, created)
		ids[name] = id
	}
	incoming := profileModel.Profile{ProfileId: ids["self"], OrgHandle: org, UserId: "same-user"}
	for _, property := range []string{"", "identity_attributes.email"} {
		got, err := findReferenceProfileCandidate(context.Background(), incoming, property, []string{"shared"})
		require.NoError(t, err)
		require.Equal(t, ids["old-a"], got, "oldest match with ID tie-breaker")
	}
	// A rule candidate with a different nonempty user ID is not eligible.
	incoming.UserId = "different-user"
	got, err := findReferenceProfileCandidate(context.Background(), incoming, "", nil)
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = findReferenceProfileCandidate(context.Background(), incoming, "identity_attributes.email", []string{"shared"})
	require.NoError(t, err)
	require.Empty(t, got)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = findReferenceProfileCandidate(ctx, incoming, "identity_attributes.email", []string{"shared"})
	require.Error(t, err)
	for _, invalid := range []string{"application_data.email", "traits..email", "traits", "p.user_id"} {
		_, err = findReferenceProfileCandidate(context.Background(), incoming, invalid, []string{"x"})
		require.Error(t, err, invalid)
	}
}

// This checks GIN eligibility, not planner costs on a small test fixture. The
// large-data plan and timing are measured separately without disabling scans.
func Test_UnificationCandidateGINEligibility(t *testing.T) {
	if suiteDBType != database.TypePostgres {
		t.Skip("PostgreSQL GIN query plans")
	}
	tx, err := suiteDB.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_profiles_identity_attributes_gin ON profiles USING GIN (identity_attributes)`)
	require.NoError(t, err)
	_, err = tx.Exec(`SET LOCAL enable_seqscan = off; SET LOCAL enable_indexscan = off`)
	require.NoError(t, err)
	_, values, err := scripts.BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
		suiteDBType, "identity_attributes.email", []string{"a"})
	require.NoError(t, err)
	// Isolate the generated JSONPath predicate so a competing tenant index
	// cannot hide GIN eligibility on this small fixture.
	rows, err := tx.Query("EXPLAIN SELECT profile_id FROM profiles WHERE identity_attributes @? $1::jsonpath", values[0])
	require.NoError(t, err)
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += fmt.Sprintln(line)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, plan, "idx_profiles_identity_attributes_gin")
}

// Run with -run '^$' -bench Benchmark_UnificationCandidateLookup -benchmem.
// PostgreSQL is the production-scale datasource; SQLite has no JSONB GIN index.
func Benchmark_UnificationCandidateLookup(b *testing.B) {
	if suiteDBType != database.TypePostgres {
		b.Skip("PostgreSQL scale benchmark")
	}
	for _, count := range []int{1000, 100000, 1000000} {
		b.Run(fmt.Sprintf("masters_%d", count), func(b *testing.B) {
			org := "unification-bench-" + uuid.NewString()
			_, err := suiteDB.Exec(`INSERT INTO profiles
				(profile_id, org_handle, user_id, created_at, identity_attributes)
				SELECT $1 || '-' || n, $1, 'user-' || n,
				TIMESTAMPTZ '2026-01-01' + n * INTERVAL '1 microsecond',
				jsonb_build_object('email', jsonb_build_array('profile-' || n || '@example.test'),
				'tag', jsonb_build_array('common')) FROM generate_series(1, $2::int) n`, org, count)
			require.NoError(b, err)
			b.Cleanup(func() {
				_, _ = suiteDB.Exec(`DELETE FROM profile_reference WHERE org_handle = $1`, org)
				_, _ = suiteDB.Exec(`DELETE FROM profiles WHERE org_handle = $1`, org)
			})
			_, err = suiteDB.Exec(`INSERT INTO profile_reference (profile_id, org_handle, profile_status)
				SELECT profile_id, org_handle, 'REFERENCE_PROFILE' FROM profiles WHERE org_handle = $1`, org)
			require.NoError(b, err)
			_, err = suiteDB.Exec(`ANALYZE profiles; ANALYZE profile_reference`)
			require.NoError(b, err)
			incoming := profileModel.Profile{ProfileId: "incoming", OrgHandle: org, UserId: fmt.Sprintf("user-%d", count)}
			for _, tc := range []struct{ name, property, value string }{
				{"user_id", "", ""},
				{"rule_match", "identity_attributes.email", fmt.Sprintf("profile-%d@example.test", count)},
				{"rule_miss", "identity_attributes.email", "absent@example.test"},
				{"common_value", "identity_attributes.tag", "common"},
			} {
				b.Run(tc.name, func(b *testing.B) {
					lookupIncoming := incoming
					if tc.property != "" {
						lookupIncoming.UserId = ""
					}
					query := scripts.FindOldestReferenceProfileIDByUserID
					args := []interface{}{org, lookupIncoming.ProfileId, lookupIncoming.UserId}
					if tc.property != "" {
						var matchArgs []interface{}
						query, matchArgs, err = scripts.BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
							suiteDBType, tc.property, []string{tc.value})
						require.NoError(b, err)
						args = append(args, matchArgs...)
					}
					rows, err := suiteDB.Query("EXPLAIN (ANALYZE, BUFFERS) "+query.GetQuery(suiteDBType), args...)
					require.NoError(b, err)
					for rows.Next() {
						var line string
						require.NoError(b, rows.Scan(&line))
						b.Log(line)
					}
					require.NoError(b, rows.Err())
					require.NoError(b, rows.Close())
					b.ReportAllocs()
					for b.Loop() {
						id, err := findReferenceProfileCandidate(context.Background(), lookupIncoming,
							tc.property, []string{tc.value})
						if err != nil {
							b.Fatal(err)
						}
						if (id == "") != (tc.name == "rule_miss") {
							b.Fatalf("unexpected candidate %q", id)
						}
					}
				})
			}
		})
	}
}
