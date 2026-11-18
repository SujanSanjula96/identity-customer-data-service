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

import (
	"reflect"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func Test_BuildFindOldestReferenceProfileIDByAttributeValuesQuery(t *testing.T) {
	values := []string{"a@b.com", `q"uote`}

	t.Run("sqlite binds the key path and the values as JSON", func(t *testing.T) {
		query, args, err := BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
			database.TypeSQLite, "identity_attributes.email", values)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(query.Query, "p.identity_attributes") {
			t.Errorf("the column was not put into the statement: %s", query.Query)
		}
		expected := []interface{}{`["email"]`, `["a@b.com","q\"uote"]`}
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("expected %#v, got %#v", expected, args)
		}
	})

	t.Run("postgres binds a JSONPath, the leaf path and the values", func(t *testing.T) {
		_, args, err := BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
			database.TypePostgres, "traits.contact.email", values)
		if err != nil {
			t.Fatal(err)
		}
		expected := []interface{}{
			`$."contact"."email" ? (@ == "a@b.com" || @ == "q\"uote")`,
			`$."contact"."email"`,
			`["a@b.com","q\"uote"]`,
		}
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("expected %#v, got %#v", expected, args)
		}
	})

	t.Run("mysql binds the key path and the values as JSON", func(t *testing.T) {
		query, args, err := BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
			database.TypeMySQL, "traits.contact.email", values)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(query.GetQuery(database.TypeMySQL), "JSON_OVERLAPS(JSON_EXTRACT(p.traits, $4)") {
			t.Errorf("the column was not put into the MySQL statement: %s", query.GetQuery(database.TypeMySQL))
		}
		expected := []interface{}{`$."contact"[0 to last]."email"[0 to last]`, `["a@b.com","q\"uote"]`}
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("expected %#v, got %#v", expected, args)
		}
	})

	t.Run("mssql binds each key and the values, and reads one step per key", func(t *testing.T) {
		query, args, err := BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
			database.TypeMSSQL, "traits.contact.email", values)
		if err != nil {
			t.Fatal(err)
		}
		expected := []interface{}{"contact", "email", `["a@b.com","q\"uote"]`}
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("expected %#v, got %#v", expected, args)
		}
		text := query.GetQuery(database.TypeMSSQL)
		for _, part := range []string{"(SELECT p.traits AS v, 5 AS t) s0", "= $4) s1", "= $5) s2",
			"OPENJSON($6)", "SELECT TOP (1) p.profile_id"} {
			if !strings.Contains(text, part) {
				t.Errorf("the MSSQL statement does not contain %q:\n%s", part, text)
			}
		}
		if strings.Contains(query.GetQuery(database.TypePostgres), "OPENJSON") {
			t.Error("PostgreSQL got the MSSQL text")
		}
		if _, _, err := database.BindStatement(database.TypeMSSQL, text,
			append([]interface{}{"o", "p", "u"}, args...)); err != nil {
			t.Errorf("the MSSQL statement does not rebind: %v", err)
		}
	})

	t.Run("a datasource without a lookup is refused", func(t *testing.T) {
		for _, dbType := range []string{"oracle", ""} {
			if _, _, err := BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
				dbType, "traits.email", values); err == nil {
				t.Errorf("expected an error for %q", dbType)
			}
		}
	})

	t.Run("a property or value that cannot be looked up is refused", func(t *testing.T) {
		build := BuildFindOldestReferenceProfileIDByAttributeValuesQuery
		supported := []string{database.TypePostgres, database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL}
		for _, dbType := range supported {
			if _, _, err := build(dbType, "email", values); err == nil {
				t.Errorf("%s: expected an error for a property without a scope", dbType)
			}
			if _, _, err := build(dbType, "other.email", values); err == nil {
				t.Errorf("%s: expected an error for an unsupported scope", dbType)
			}
			if _, _, err := build(dbType, "traits.", values); err == nil {
				t.Errorf("%s: expected an error for an empty key", dbType)
			}
			if _, _, err := build(dbType, "traits.email", nil); err == nil {
				t.Errorf("%s: expected an error for no values", dbType)
			}
		}
	})
}
