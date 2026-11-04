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

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// TestApplyDefaultsDerivesStrengthsFromAttributeType pins the table the scoring engine
// applies. A rule stores a strength only where an operator overrode it, so for every rule on
// a default deployment these derived values are what decides whether a lone agreement may
// merge — and they are no longer visible in any API response, which is precisely why they
// need asserting here.
func TestApplyDefaultsDerivesStrengthsFromAttributeType(t *testing.T) {
	tests := []struct {
		name         string
		rule         UnificationRule
		wantMatch    string
		wantMismatch string
	}{
		{
			name:         "email agreement identifies, email disagreement does not",
			rule:         UnificationRule{AttributeType: constants.AttributeTypeEmail},
			wantMatch:    constants.EvidenceStrengthHigh,
			wantMismatch: constants.EvidenceStrengthLow,
		},
		{
			name:         "a shared name is weak evidence on its own",
			rule:         UnificationRule{AttributeType: constants.AttributeTypeName},
			wantMatch:    constants.EvidenceStrengthLow,
			wantMismatch: constants.EvidenceStrengthMedium,
		},
		{
			// A rule written before typed matching resolves to PRIMITIVE_EXACT. It has to
			// stay strong enough to merge alone, or an upgrading tenant whose rules all
			// land here starts producing review tasks for matches it used to merge.
			name:         "the type every pre-feature rule resolves to still merges alone",
			rule:         UnificationRule{},
			wantMatch:    constants.EvidenceStrengthHigh,
			wantMismatch: constants.EvidenceStrengthMedium,
		},
		{
			// Two different dates of birth are near-proof of two different people, while a
			// shared one is a coincidence a crowded org produces constantly.
			name:         "date of birth is the reverse of email",
			rule:         UnificationRule{AttributeType: constants.AttributeTypeDate},
			wantMatch:    constants.EvidenceStrengthLow,
			wantMismatch: constants.EvidenceStrengthHigh,
		},
		{
			name: "an operator override wins over the derived value",
			rule: UnificationRule{
				AttributeType: constants.AttributeTypeEmail,
				MatchStrength: constants.EvidenceStrengthLow,
			},
			wantMatch:    constants.EvidenceStrengthLow,
			wantMismatch: constants.EvidenceStrengthLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyDefaults(tt.rule, constants.AttributeTypePrimitiveExact,
				constants.UnificationMethodDeterministic,
				constants.DefaultMatchStrength, constants.DefaultMismatchStrength)

			if got.MatchStrength != tt.wantMatch {
				t.Errorf("match_strength = %q, want %q", got.MatchStrength, tt.wantMatch)
			}
			if got.MismatchStrength != tt.wantMismatch {
				t.Errorf("mismatch_strength = %q, want %q", got.MismatchStrength, tt.wantMismatch)
			}
		})
	}
}
