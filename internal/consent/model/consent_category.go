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

// Package model defines the consent categories of an organization and their API requests and responses.
package model

// ConsentCategory represents a category of user consent
type ConsentCategory struct {
	// Human-readable category name
	CategoryName string `json:"category_name" bson:"category_name"`
	// Identifier used for internal referencing
	CategoryIdentifier string `json:"category_identifier" bson:"category_identifier"`
	// Identifier of the organization
	OrgID string `json:"org_id" bson:"org_id"`
	// One of: profiling, personalization, destination
	Purpose string `json:"purpose" bson:"purpose"`
	// Optional list of destination names
	Destinations []string `json:"destinations,omitempty" bson:"destinations,omitempty"`
	// Profile attributes covered by this consent category
	Attributes []ConsentAttribute `json:"attributes,omitempty" bson:"attributes,omitempty"`
	// If true, category is system-managed and cannot be modified or deleted
	IsMandatory bool `json:"is_mandatory" bson:"is_mandatory"`
}
