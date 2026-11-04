/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

type UnificationRuleAPIRequest struct {
	RuleName          string `json:"rule_name" bson:"rule_name" binding:"required"`
	PropertyName      string `json:"property_name" bson:"property_name" binding:"required"`
	Priority          int    `json:"priority" bson:"priority" binding:"required"`
	IsActive          bool   `json:"is_active" bson:"is_active" binding:"required"`
	AttributeType     string `json:"attribute_type" bson:"attribute_type" binding:"required"`
	UnificationMethod string `json:"unification_method" bson:"unification_method" binding:"required"`
	MatchStrength     string `json:"match_strength" bson:"match_strength"`
	MismatchStrength  string `json:"mismatch_strength" bson:"mismatch_strength"`
}

// UnificationRuleAPIResponse carries the evidence strengths only on a server that lets an
// operator set them, and only where one actually did.
//
// Both halves matter. While identity_resolution.allow_evidence_strength_override is off the
// fields are refused on the way in, so returning them would describe a setting no caller can
// make; the value would be a pure function of attribute_type — already in this response —
// and publishing it would turn a retune of the scoring weights into an API change.
//
// Where the override is on, an omitted field means the rule inherits from attribute_type,
// which is exactly what the row holds. Returning the derived value instead would make a
// rule copied through GET-then-POST freeze its strengths as explicit overrides while the
// original kept following the table, and would leave an editor unable to tell a deliberate
// choice from an inherited one.
type UnificationRuleAPIResponse struct {
	RuleId            string `json:"rule_id" bson:"rule_id" binding:"required"`
	RuleName          string `json:"rule_name" bson:"rule_name" binding:"required"`
	PropertyName      string `json:"property_name" bson:"property_name" binding:"required"`
	Priority          int    `json:"priority" bson:"priority" binding:"required"`
	IsActive          bool   `json:"is_active" bson:"is_active" binding:"required"`
	AttributeType     string `json:"attribute_type" bson:"attribute_type"`
	UnificationMethod string `json:"unification_method" bson:"unification_method"`
	MatchStrength     string `json:"match_strength,omitempty" bson:"match_strength,omitempty"`
	MismatchStrength  string `json:"mismatch_strength,omitempty" bson:"mismatch_strength,omitempty"`
	Origin            string `json:"origin,omitempty" bson:"-"`
	OwnerOrgHandle    string `json:"owner_org_handle,omitempty" bson:"-"`
}

// ToAPIResponse converts a rule to its API response. It leaves out the evidence strengths, which
// the caller sets when the server exposes them.
func ToAPIResponse(rule UnificationRule) UnificationRuleAPIResponse {
	return UnificationRuleAPIResponse{
		RuleId:            rule.RuleId,
		RuleName:          rule.RuleName,
		PropertyName:      rule.PropertyName,
		Priority:          rule.Priority,
		IsActive:          rule.IsActive,
		AttributeType:     rule.AttributeType,
		UnificationMethod: rule.UnificationMethod,
		Origin:            rule.Origin,
		OwnerOrgHandle:    rule.OwnerOrgHandle,
	}
}

type UnificationRuleUpdateRequest struct {
	RuleName          *string `json:"rule_name" bson:"rule_name"`
	Priority          *int    `json:"priority" bson:"priority"`
	IsActive          *bool   `json:"is_active" bson:"is_active"`
	AttributeType     *string `json:"attribute_type" bson:"attribute_type"`
	UnificationMethod *string `json:"unification_method" bson:"unification_method"`
	MatchStrength     *string `json:"match_strength" bson:"match_strength"`
	MismatchStrength  *string `json:"mismatch_strength" bson:"mismatch_strength"`
}
