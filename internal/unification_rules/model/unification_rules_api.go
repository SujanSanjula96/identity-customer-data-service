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
	RuleName     string `json:"rule_name" bson:"rule_name" binding:"required"`
	PropertyName string `json:"property_name" bson:"property_name" binding:"required"`
	Priority     int    `json:"priority" bson:"priority" binding:"required"`
	IsActive     bool   `json:"is_active" bson:"is_active" binding:"required"`
}

type UnificationRuleAPIResponse struct {
	RuleId         string `json:"rule_id" bson:"rule_id" binding:"required"`
	RuleName       string `json:"rule_name" bson:"rule_name" binding:"required"`
	PropertyName   string `json:"property_name" bson:"property_name" binding:"required"`
	Priority       int    `json:"priority" bson:"priority" binding:"required"`
	IsActive       bool   `json:"is_active" bson:"is_active" binding:"required"`
	Origin         string `json:"origin,omitempty" bson:"-"`
	OwnerOrgHandle string `json:"owner_org_handle,omitempty" bson:"-"`
	State          string `json:"state,omitempty" bson:"-"`
}

// ToAPIResponse converts a rule to its API response.
func ToAPIResponse(rule UnificationRule) UnificationRuleAPIResponse {
	return UnificationRuleAPIResponse{
		RuleId:         rule.RuleId,
		RuleName:       rule.RuleName,
		PropertyName:   rule.PropertyName,
		Priority:       rule.Priority,
		IsActive:       rule.IsActive,
		Origin:         rule.Origin,
		OwnerOrgHandle: rule.OwnerOrgHandle,
		State:          rule.State,
	}
}

type UnificationRuleUpdateRequest struct {
	RuleName *string `json:"rule_name" bson:"rule_name"`
	Priority *int    `json:"priority" bson:"priority"`
	IsActive *bool   `json:"is_active" bson:"is_active"`
}
