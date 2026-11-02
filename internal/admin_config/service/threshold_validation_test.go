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

import "testing"

// TestValidateThresholds pins the relationship between the two score thresholds.
//
// Each is individually reasonable at values that break the engine when combined, which is
// why they are checked against each other rather than only against 0 and 1. The case that
// motivated this is auto_merge == manual_review: the cap applied to a held-back match lands
// below the review threshold, so the pair is dropped entirely rather than escalated — no
// merge, no review task, and nothing to say a candidate was found.
func TestValidateThresholds(t *testing.T) {
	tests := []struct {
		name                  string
		autoMerge, manualView float64
		wantErr               bool
	}{
		{"shipped defaults", 0.95, 0.75, false},
		{"unset — both fall back to the shipped defaults", 0, 0, false},
		{"review unset, auto-merge lowered to its floor", 0.76, 0, false},
		{"review exactly at the highest safe value", 0.95, 0.94, false},
		{"narrow but valid band", 0.80, 0.79, false},
		{"review just above the contradiction bar", 0.95, 0.31, false},

		// Pairs exactly one offset apart, where subtracting the offset in binary floating
		// point lands just under the decimal value. Each of these was refused before the
		// bound was rounded, for a difference far below the precision a score is given in.
		{"one offset apart at 0.82", 0.82, 0.81, false},
		{"one offset apart at 0.35", 0.35, 0.34, false},
		{"one offset apart at 0.41", 0.41, 0.40, false},
		{"one offset apart at 0.47", 0.47, 0.46, false},
		{"one offset apart at 0.57", 0.57, 0.56, false},
		{"one offset apart at 0.69", 0.69, 0.68, false},

		{"review above the cap — a held-back match would vanish", 0.95, 0.95, true},
		{"thresholds equal — the measured suppression case", 0.75, 0.75, true},
		{"review between the cap and auto-merge", 0.95, 0.945, true},
		{"review above auto-merge entirely", 0.80, 0.90, true},
		{"review at the contradiction bar", 0.95, 0.30, true},
		{"review below the contradiction bar", 0.95, 0.10, true},
		{"review unset but auto-merge too low for the default", 0.70, 0, true},

		// Outside the range a score is expressed in. The store reads such a value back as
		// unset, so accepting it would report a setting the engine never applies.
		{"auto-merge at the top of the range", 1, 0, false},
		{"auto-merge above 1", 1.5, 0, true},
		{"auto-merge above 1 with a review threshold set", 1.5, 0.8, true},
		{"auto-merge negative", -0.5, 0, true},
		{"review above 1", 0, 1.2, true},
		{"both above 1 and one offset apart", 1.3, 1.2, true},
		{"review negative", 0.95, -0.5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateThresholds(tt.autoMerge, tt.manualView)
			if tt.wantErr && err == nil {
				t.Errorf("validateThresholds(%g, %g) accepted a combination the engine cannot apply",
					tt.autoMerge, tt.manualView)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateThresholds(%g, %g) rejected a usable combination: %v",
					tt.autoMerge, tt.manualView, err)
			}
		})
	}
}
