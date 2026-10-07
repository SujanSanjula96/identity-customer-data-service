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

package idp

import (
	"strings"
	"testing"
)

// The WSO2 IS adapter needs the runtime configuration, so this test checks only the types that
// CDS refuses at start.
func TestNewAdapterRefusesOtherTypes(t *testing.T) {

	cases := map[string]string{
		"thunderid": "not available yet",
		"okta":      "not supported",
	}
	for idpType, message := range cases {
		_, err := NewAdapter(idpType)
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("type %q: expected an error with %q, got %v", idpType, message, err)
		}
	}
}
