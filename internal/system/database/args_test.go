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

import (
	"reflect"
	"testing"
)

func Test_NormalizeSQLiteArgs(t *testing.T) {

	t.Run("json bytes are bound as text", func(t *testing.T) {
		normalized := NormalizeSQLiteArgs([]interface{}{[]byte(`{"a":1}`), "plain", 7, nil})
		expected := []interface{}{`{"a":1}`, "plain", 7, nil}
		if !reflect.DeepEqual(normalized, expected) {
			t.Fatalf("expected %#v, got %#v", expected, normalized)
		}
	})

	t.Run("no arguments", func(t *testing.T) {
		if normalized := NormalizeSQLiteArgs(nil); len(normalized) != 0 {
			t.Fatalf("expected no arguments, got %#v", normalized)
		}
	})
}
