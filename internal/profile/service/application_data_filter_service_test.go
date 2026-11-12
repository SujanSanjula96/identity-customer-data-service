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
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func sampleAppData() map[string]map[string]interface{} {
	return map[string]map[string]interface{}{
		"app-1": {"theme": "dark"},
		"app-2": {"lang": "en"},
	}
}

func TestFilterApplicationDataReturnsNothingWhenNotRequested(t *testing.T) {
	got := FilterApplicationData(sampleAppData(), "app-1", true, ApplicationDataFilterParams{IncludeAppData: false})

	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestFilterApplicationDataReturnsEmptyMapForNoData(t *testing.T) {
	got := FilterApplicationData(nil, "app-1", true, ApplicationDataFilterParams{IncludeAppData: true})

	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestFilterApplicationDataForSystemApp(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		want      map[string]map[string]interface{}
	}{
		{"no ids returns everything", nil, sampleAppData()},
		{"wildcard returns everything", []string{"*"}, sampleAppData()},
		{"wildcard among ids returns everything", []string{"app-1", "*"}, sampleAppData()},
		{"one id", []string{"app-2"}, map[string]map[string]interface{}{"app-2": {"lang": "en"}}},
		{"unknown id is skipped", []string{"app-1", "missing"},
			map[string]map[string]interface{}{"app-1": {"theme": "dark"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := ApplicationDataFilterParams{IncludeAppData: true, RequestedAppIDs: tc.requested}

			got := FilterApplicationData(sampleAppData(), "caller", true, params)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFilterApplicationDataForRegularAppSeesOnlyItsOwnData(t *testing.T) {
	// A regular app never sees another app's data, whatever it asks for.
	params := ApplicationDataFilterParams{IncludeAppData: true, RequestedAppIDs: []string{"*"}}

	got := FilterApplicationData(sampleAppData(), "app-1", false, params)

	assert.Equal(t, map[string]map[string]interface{}{"app-1": {"theme": "dark"}}, got)
}

func TestFilterApplicationDataForRegularAppWithoutIdentityOrData(t *testing.T) {
	params := ApplicationDataFilterParams{IncludeAppData: true}

	assert.Empty(t, FilterApplicationData(sampleAppData(), "", false, params))
	assert.Empty(t, FilterApplicationData(sampleAppData(), "unknown-app", false, params))
}
