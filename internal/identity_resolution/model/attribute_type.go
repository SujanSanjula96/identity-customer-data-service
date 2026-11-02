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

package model

// FlattenMap recursively flattens a nested map into dot-delimited keys.
// For example, {"name": {"first": "John"}} with prefix "traits" becomes
// {"traits.name.first": "John"}.
func FlattenMap(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		fullKey := prefix + "." + k
		if nested, ok := v.(map[string]interface{}); ok {
			FlattenMap(fullKey, nested, out)
		} else {
			out[fullKey] = v
		}
	}
}
