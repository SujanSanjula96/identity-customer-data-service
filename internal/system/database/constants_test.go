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

package database

import "testing"

func Test_ResolveType(t *testing.T) {

	testCases := map[string]string{
		"":          TypeSQLite,
		"   ":       TypeSQLite,
		"sqlite":    TypeSQLite,
		" SQLite  ": TypeSQLite,
		"postgres":  TypePostgres,
		"Postgres":  TypePostgres,
	}

	for configured, expected := range testCases {
		if actual := ResolveType(configured); actual != expected {
			t.Errorf("ResolveType(%q) = %q, expected %q", configured, actual, expected)
		}
	}
}

// Test_unsupportedTypeIsRejected records that normalization is not validation:
// ResolveType passes an unrecognised value through, and the startup check is
// what refuses to run on it.
func Test_unsupportedTypeIsRejected(t *testing.T) {

	if normalized := ResolveType(" MySQL "); normalized != "mysql" {
		t.Errorf("ResolveType(%q) = %q, expected %q", " MySQL ", normalized, "mysql")
	}
	if IsSupportedType("mysql") {
		t.Error("mysql is not a datasource CDS can run on and must not be reported as supported")
	}
	for _, supported := range []string{TypePostgres, TypeSQLite} {
		if !IsSupportedType(supported) {
			t.Errorf("%q must be reported as supported", supported)
		}
	}
}
