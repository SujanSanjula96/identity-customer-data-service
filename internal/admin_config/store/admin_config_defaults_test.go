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
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// TestAdminConfigRowsDefaultAutoMergeEnabled is an upgrade guard.
//
// auto_merge_enabled is stored as a row in cds_config, so an org configured before the key
// existed simply has no row for it. Scanning that absence into the zero value would leave
// automatic merging switched off for every existing tenant — every match becoming a review
// task with nothing in the logs to say why. The absence must read as enabled, and only an
// explicit "false" may turn it off.
func TestAdminConfigRowsDefaultAutoMergeEnabled(t *testing.T) {
	tests := []struct {
		name string
		rows []map[string]interface{}
		want bool
	}{
		{
			name: "org predating the key",
			rows: []map[string]interface{}{
				{"config": constants.ConfigCDSEnabled, "value": "true"},
			},
			want: true,
		},
		{
			name: "explicitly disabled",
			rows: []map[string]interface{}{
				{"config": constants.ConfigCDSEnabled, "value": "true"},
				{"config": constants.ConfigAutoMergeEnabled, "value": "false"},
			},
			want: false,
		},
		{
			name: "explicitly enabled",
			rows: []map[string]interface{}{
				{"config": constants.ConfigAutoMergeEnabled, "value": "true"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := scanAdminConfigRows("acme", tt.rows)
			if config.AutoMergeEnabled != tt.want {
				t.Errorf("AutoMergeEnabled = %v, want %v", config.AutoMergeEnabled, tt.want)
			}
		})
	}
}

// TestAdminConfigRowsDefaultDeterministicMatchDecisive is the same upgrade guard for the
// setting that decides whether other rules may object to an exact match.
//
// Before typed matching, any deterministic rule matching merged the pair outright. An org
// predating this key has no row for it, and reading that absence as "off" would let other
// rules start sending its merges to review the moment it upgraded. Absence must read as on;
// only an explicit "false" opens exact matches to objection.
func TestAdminConfigRowsDefaultDeterministicMatchDecisive(t *testing.T) {
	tests := []struct {
		name string
		rows []map[string]interface{}
		want bool
	}{
		{
			name: "org predating the key",
			rows: []map[string]interface{}{
				{"config": constants.ConfigAutoMergeEnabled, "value": "true"},
			},
			want: true,
		},
		{
			name: "explicitly opened to objection",
			rows: []map[string]interface{}{
				{"config": constants.ConfigDeterministicMatchDecisive, "value": "false"},
			},
			want: false,
		},
		{
			name: "explicitly decisive",
			rows: []map[string]interface{}{
				{"config": constants.ConfigDeterministicMatchDecisive, "value": "true"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := scanAdminConfigRows("acme", tt.rows)
			if config.DeterministicMatchDecisive != tt.want {
				t.Errorf("DeterministicMatchDecisive = %v, want %v", config.DeterministicMatchDecisive, tt.want)
			}
		})
	}
}
