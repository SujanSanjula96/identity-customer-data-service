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
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

var allDatasources = []string{
	database.TypePostgres, database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL,
}

const profileIDsBase = "SELECT DISTINCT p.profile_id FROM profiles p LEFT JOIN profile_reference r " +
	"ON p.profile_id = r.profile_id\n" +
	"                INNER JOIN application_data a_app_1\n" +
	"                  ON a_app_1.profile_id = p.profile_id AND a_app_1.app_id = $5 " +
	"INNER JOIN application_data a ON a.profile_id = p.profile_id\n"

var profileIDsFilters = []string{
	"traits.city eq Colombo",
	"traits.city ew ndy",
	"user_id co ice",
	"application_data.app-1.theme sw da",
	"application_data.mode eq dark",
}

func Test_buildProfileIDsQuery(t *testing.T) {
	const tail = " AND r.profile_status = 'REFERENCE_PROFILE' AND p.profile_id IN ($8, $9)"

	cases := map[string]struct {
		conditions []string
		args       []interface{}
	}{
		database.TypePostgres: {
			conditions: []string{
				`p.org_id = $1`,
				`p.traits @> $2::jsonb`,
				`p.traits ->> 'city' ILIKE $3`,
				`p.user_id ILIKE $4`,
				`a_app_1.application_data -> 'app_specific_data' ->> 'theme' ILIKE $6`,
				`a.application_data @> $7::jsonb`,
			},
			args: []interface{}{"org-1", `{"city": "Colombo"}`, "%ndy", "%ice%", "app-1", "da%",
				`{"app_specific_data": {"mode": "dark"}}`, "id-a", "id-b"},
		},
		database.TypeSQLite: {
			conditions: []string{
				`p.org_id = $1`,
				`json_extract(p.traits, '$."city"') = $2`,
				`json_extract(p.traits, '$."city"') LIKE $3`,
				`p.user_id LIKE $4`,
				`json_extract(a_app_1.application_data, '$."app_specific_data"."theme"') LIKE $6`,
				`json_extract(a.application_data, '$."app_specific_data"."mode"') = $7`,
			},
			args: []interface{}{"org-1", "Colombo", "%ndy", "%ice%", "app-1", "da%", "dark", "id-a", "id-b"},
		},
		database.TypeMySQL: {
			conditions: []string{
				`p.org_id = $1`,
				`JSON_UNQUOTE(JSON_EXTRACT(p.traits, '$."city"')) = $2`,
				`JSON_UNQUOTE(JSON_EXTRACT(p.traits, '$."city"')) LIKE $3`,
				`p.user_id LIKE $4`,
				`JSON_UNQUOTE(JSON_EXTRACT(a_app_1.application_data, '$."app_specific_data"."theme"')) LIKE $6`,
				`JSON_UNQUOTE(JSON_EXTRACT(a.application_data, '$."app_specific_data"."mode"')) = $7`,
			},
			args: []interface{}{"org-1", "Colombo", "%ndy", "%ice%", "app-1", "da%", "dark", "id-a", "id-b"},
		},
		database.TypeMSSQL: {
			conditions: []string{
				`p.org_id = $1`,
				`JSON_VALUE(p.traits, '$."city"') = $2`,
				`JSON_VALUE(p.traits, '$."city"') LIKE $3`,
				`p.user_id LIKE $4`,
				`JSON_VALUE(a_app_1.application_data, '$."app_specific_data"."theme"') LIKE $6`,
				`JSON_VALUE(a.application_data, '$."app_specific_data"."mode"') = $7`,
			},
			args: []interface{}{"org-1", "Colombo", "%ndy", "%ice%", "app-1", "da%", "dark", "id-a", "id-b"},
		},
	}

	for dbType, want := range cases {
		t.Run(dbType, func(t *testing.T) {
			gotSQL, gotArgs, err := buildProfileIDsQuery(dbType, "org-1", profileIDsFilters, []string{"id-a", "id-b"})
			if err != nil {
				t.Fatal(err)
			}

			expected := profileIDsBase + "WHERE " + strings.Join(want.conditions, " AND ") + tail
			if gotSQL != expected {
				t.Errorf("unexpected statement.\nexpected: %s\n     got: %s", expected, gotSQL)
			}
			if !reflect.DeepEqual(gotArgs, want.args) {
				t.Errorf("unexpected arguments.\nexpected: %#v\n     got: %#v", want.args, gotArgs)
			}
		})
	}
}

func Test_buildProfileIDsQuery_BindsInOrderOfAppearanceOnMySQL(t *testing.T) {
	gotSQL, gotArgs, err := buildProfileIDsQuery(database.TypeMySQL, "org-1", profileIDsFilters,
		[]string{"id-a", "id-b"})
	if err != nil {
		t.Fatal(err)
	}

	boundSQL, boundArgs, err := database.BindStatement(database.TypeMySQL, gotSQL, gotArgs)
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`\$\d`).MatchString(boundSQL) {
		t.Errorf("a numbered placeholder is left: %s", boundSQL)
	}
	if strings.Count(boundSQL, "?") != len(boundArgs) {
		t.Errorf("%d placeholders for %d arguments", strings.Count(boundSQL, "?"), len(boundArgs))
	}
	if boundArgs[0] != "app-1" || boundArgs[1] != "org-1" {
		t.Errorf("the application id must be bound first, then the organization: %#v", boundArgs)
	}
}

func Test_buildProfileIDsQuery_WithoutFilters(t *testing.T) {
	for _, dbType := range allDatasources {
		t.Run(dbType, func(t *testing.T) {
			gotSQL, gotArgs, err := buildProfileIDsQuery(dbType, "org-1", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			expected := "SELECT DISTINCT p.profile_id FROM profiles p LEFT JOIN profile_reference r " +
				"ON p.profile_id = r.profile_id\nWHERE p.org_id = $1 AND r.profile_status = 'REFERENCE_PROFILE'"
			if gotSQL != expected {
				t.Errorf("unexpected statement: %s", gotSQL)
			}
			if !reflect.DeepEqual(gotArgs, []interface{}{"org-1"}) {
				t.Errorf("unexpected arguments: %#v", gotArgs)
			}
		})
	}
}

func Test_buildProfileIDsQuery_Rejects(t *testing.T) {
	t.Run("an unsupported datasource", func(t *testing.T) {
		if _, _, err := buildProfileIDsQuery("oracle", "org-1", nil, nil); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("a key that could break the statement, on every datasource", func(t *testing.T) {
		for _, dbType := range allDatasources {
			for _, filter := range []string{"traits.a'b eq x", "traits.a'b co x", "application_data.app-1.a'b eq x"} {
				if _, _, err := buildProfileIDsQuery(dbType, "org-1", []string{filter}, nil); err == nil {
					t.Errorf("%s: expected an error for %q", dbType, filter)
				}
			}
		}
	})

	t.Run("a condition it cannot read is left out", func(t *testing.T) {
		gotSQL, gotArgs, err := buildProfileIDsQuery(database.TypeMySQL, "org-1",
			[]string{"malformed", "traits.city gt 5", "user_id zz x"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(gotSQL, "traits") || len(gotArgs) != 1 {
			t.Errorf("unexpected statement or arguments: %s %#v", gotSQL, gotArgs)
		}
	})
}

func Test_likePattern(t *testing.T) {
	cases := map[string]string{"co": "%v%", "sw": "v%", "ew": "%v"}
	for operator, want := range cases {
		if got := likePattern(operator, "v"); got != want {
			t.Errorf("likePattern(%q) = %q, expected %q", operator, got, want)
		}
	}
}
