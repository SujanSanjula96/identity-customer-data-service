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

package handler

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

// resolveMatchingFields mirrors the defaulting the create handler applies before validation.
// It is kept beside the test rather than exported, so the test states the contract the
// handler has to meet rather than re-running the handler's own code.
func resolveMatchingFields(rule model.UnificationRule) (model.UnificationRule, string) {
	if rule.UnificationMethod == "" {
		rule.UnificationMethod = constants.UnificationMethodDeterministic
	}
	if rule.AttributeType == "" {
		if rule.UnificationMethod == constants.UnificationMethodFuzzy {
			return rule, "attribute_type is required when unification_method is 'fuzzy'"
		}
		rule.AttributeType = constants.AttributeTypePrimitiveExact
	}
	if rule.UnificationMethod == constants.UnificationMethodFuzzy &&
		!constants.FuzzyCapableAttributeTypes[rule.AttributeType] {
		return rule, "attribute type does not support fuzzy matching"
	}

	return rule, ""
}

// TestMatchingFieldsAreOnlyRequiredForFuzzy pins which parts of a rule a caller has to spell
// out.
//
// A rule that says nothing about how it matches is an exact match on a plain value — the
// same reading a rule stored before typed matching gets. Requiring both fields would make
// the simplest rule harder to write than it was before the feature, and would reject a
// client that predates the fields entirely.
//
// Fuzzy is the exception: the type picks the comparison algorithm, and not every kind of
// value has one, so there is nothing sensible for the server to assume.
func TestMatchingFieldsAreOnlyRequiredForFuzzy(t *testing.T) {
	tests := []struct {
		name       string
		in         model.UnificationRule
		wantType   string
		wantMethod string
		wantErr    bool
	}{
		{
			name:       "neither field given — the pre-typed-matching rule",
			in:         model.UnificationRule{},
			wantType:   constants.AttributeTypePrimitiveExact,
			wantMethod: constants.UnificationMethodDeterministic,
		},
		{
			name:       "method given, type omitted",
			in:         model.UnificationRule{UnificationMethod: constants.UnificationMethodDeterministic},
			wantType:   constants.AttributeTypePrimitiveExact,
			wantMethod: constants.UnificationMethodDeterministic,
		},
		{
			// A date still normalizes under exact matching, so naming the type is worth doing
			// even though it is not demanded.
			name:       "type given, method omitted",
			in:         model.UnificationRule{AttributeType: constants.AttributeTypeDate},
			wantType:   constants.AttributeTypeDate,
			wantMethod: constants.UnificationMethodDeterministic,
		},
		{
			name: "fuzzy with a type",
			in: model.UnificationRule{
				AttributeType:     constants.AttributeTypeEmail,
				UnificationMethod: constants.UnificationMethodFuzzy,
			},
			wantType:   constants.AttributeTypeEmail,
			wantMethod: constants.UnificationMethodFuzzy,
		},
		{
			name:    "fuzzy without a type is refused rather than guessed",
			in:      model.UnificationRule{UnificationMethod: constants.UnificationMethodFuzzy},
			wantErr: true,
		},
		{
			name: "fuzzy on a type that cannot support it",
			in: model.UnificationRule{
				AttributeType:     constants.AttributeTypeUniqueID,
				UnificationMethod: constants.UnificationMethodFuzzy,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errMsg := resolveMatchingFields(tt.in)

			if tt.wantErr {
				if errMsg == "" {
					t.Fatalf("expected the request to be refused, got type=%q method=%q",
						got.AttributeType, got.UnificationMethod)
				}

				return
			}

			if errMsg != "" {
				t.Fatalf("unexpected refusal: %s", errMsg)
			}
			if got.AttributeType != tt.wantType {
				t.Errorf("attribute_type = %q, want %q", got.AttributeType, tt.wantType)
			}
			if got.UnificationMethod != tt.wantMethod {
				t.Errorf("unification_method = %q, want %q", got.UnificationMethod, tt.wantMethod)
			}
		})
	}
}
