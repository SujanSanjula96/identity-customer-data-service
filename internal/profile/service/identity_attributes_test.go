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

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithoutAttributesOfKeepsOnlyWhatTheLinkedProfileLacks(t *testing.T) {
	anonymous := map[string]interface{}{
		"email":   "anon@b.c",
		"phone":   "111",
		"name":    "Anon",
		"address": map[string]interface{}{"country": "IN", "city": "Kandy"},
		"emails":  []interface{}{"x@b.c"},
	}
	linked := map[string]interface{}{
		"email":   "idp@b.c",
		"name":    " ",
		"address": map[string]interface{}{"country": "LK"},
		"emails":  []interface{}{},
	}

	assert.Equal(t, map[string]interface{}{
		"phone":   "111",
		"name":    "Anon",
		"address": map[string]interface{}{"city": "Kandy"},
		"emails":  []interface{}{"x@b.c"},
	}, WithoutAttributesOf(anonymous, linked))
	assert.Equal(t, anonymous, WithoutAttributesOf(anonymous, nil))
	assert.Nil(t, WithoutAttributesOf(nil, linked))
	assert.Empty(t, WithoutAttributesOf(map[string]interface{}{"address": map[string]interface{}{"country": "IN"}},
		linked), "a nested object left with nothing is dropped")
}
