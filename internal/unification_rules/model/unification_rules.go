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

import "time"

// UnificationRule represents rules for merging user profiles
type UnificationRule struct {
	RuleId       string    `json:"rule_id" bson:"rule_id" binding:"required"`
	OrgHandle    string    `json:"org_handle" bson:"org_handle" binding:"required"`
	RuleName     string    `json:"rule_name" bson:"rule_name" binding:"required"`
	PropertyName string    `json:"property_name" bson:"property_name" binding:"required"`
	PropertyId   string    `json:"property_id" bson:"property_id" binding:"required"`
	Priority     int       `json:"priority" bson:"priority" binding:"required"`
	IsActive     bool      `json:"is_active" bson:"is_active" binding:"required"`
	CreatedAt    time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" bson:"updated_at"`
	// B2B: set only on reads of the effective rules of an org.
	Origin         string `json:"origin,omitempty" bson:"-"`           // OWNED or SHARED
	OwnerOrgHandle string `json:"owner_org_handle,omitempty" bson:"-"` // The owner org of a shared rule
	State          string `json:"state,omitempty" bson:"-"`            // ACTIVE, or why the rule does not run
	Rank           int    `json:"-" bson:"-"`                          // Position in the evaluation order
}
