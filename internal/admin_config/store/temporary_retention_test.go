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

package store

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func TestParseCleanupSetting(t *testing.T) {
	tests := []struct {
		key    string
		value  string
		want   int
		wantOK bool
	}{
		{constants.ConfigTemporaryProfileRetentionDays, "1", 1, true},
		{constants.ConfigTemporaryProfileRetentionDays, "90", 90, true},
		{constants.ConfigTemporaryProfileRetentionDays, "366", 366, true},
		{constants.ConfigTemporaryProfileRetentionDays, "2147483647", 2147483647, true},
		{constants.ConfigTemporaryProfileRetentionDays, "2147483648", 0, false},
		{constants.ConfigTemporaryProfileRetentionDays, "0", 0, false},
		{constants.ConfigTemporaryProfileRetentionDays, "-1", 0, false},
		{constants.ConfigTemporaryProfileRetentionDays, "ninety", 0, false},
		{constants.ConfigTemporaryProfileRetentionDays, "", 0, false},
		{constants.ConfigTemporaryProfileRetentionDays, "30.5", 0, false},
		{constants.ConfigTemporaryProfileCleanupIntervalDays, "1", 1, true},
		{constants.ConfigTemporaryProfileCleanupIntervalDays, "0", 0, false},
		{"another_key", "5", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			got, ok := parseCleanupSetting(tt.key, tt.value)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseCleanupSetting(%q, %q) = %d, %v; want %d, %v", tt.key, tt.value, got, ok,
					tt.want, tt.wantOK)
			}
		})
	}
}

// An organization that has stored no retention reads as unset, so the default applies.
func TestAdminConfigRowsLeaveRetentionUnsetByDefault(t *testing.T) {
	config := scanAdminConfigRows([]map[string]interface{}{
		{"config": constants.ConfigCDSEnabled, "value": "true"},
	})
	if config.TemporaryProfileRetentionDays != 0 {
		t.Errorf("TemporaryProfileRetentionDays = %d, want 0 (unset)", config.TemporaryProfileRetentionDays)
	}
}

func TestAdminConfigRowsReadAStoredRetention(t *testing.T) {
	config := scanAdminConfigRows([]map[string]interface{}{
		{"config": constants.ConfigTemporaryProfileRetentionDays, "value": "30"},
	})
	if config.TemporaryProfileRetentionDays != 30 {
		t.Errorf("TemporaryProfileRetentionDays = %d, want 30", config.TemporaryProfileRetentionDays)
	}
}

// A stored value outside the allowed range is ignored.
func TestAdminConfigRowsIgnoreAnOutOfRangeRetention(t *testing.T) {
	for _, value := range []string{"0", "-3", "2147483648", "abc"} {
		config := scanAdminConfigRows([]map[string]interface{}{
			{"config": constants.ConfigTemporaryProfileRetentionDays, "value": value},
		})
		if config.TemporaryProfileRetentionDays != 0 {
			t.Errorf("value %q: TemporaryProfileRetentionDays = %d, want 0 (unset)", value,
				config.TemporaryProfileRetentionDays)
		}
	}
}

func TestAdminConfigRowsReadTheInterval(t *testing.T) {
	config := scanAdminConfigRows([]map[string]interface{}{
		{"config": constants.ConfigTemporaryProfileCleanupIntervalDays, "value": "6"},
	})
	if config.TemporaryProfileCleanupIntervalDays != 6 {
		t.Errorf("interval = %d; want 6", config.TemporaryProfileCleanupIntervalDays)
	}
}

// A stored interval outside the allowed range is ignored.
func TestAdminConfigRowsIgnoreAnOutOfRangeInterval(t *testing.T) {
	config := scanAdminConfigRows([]map[string]interface{}{
		{"config": constants.ConfigTemporaryProfileCleanupIntervalDays, "value": "0"},
	})
	if config.TemporaryProfileCleanupIntervalDays != 0 {
		t.Errorf("interval = %d; want 0 (unset)", config.TemporaryProfileCleanupIntervalDays)
	}
}

// The batch size is not a setting, so a stored row for it is not read.
func TestAdminConfigRowsDoNotReadABatchSize(t *testing.T) {
	if _, ok := parseCleanupSetting("temporary_profile_cleanup_batch_size", "250"); ok {
		t.Error("a batch size row was read as a setting")
	}
}
