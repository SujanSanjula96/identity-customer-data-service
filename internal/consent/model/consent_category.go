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

package models

// ConsentCategory represents a category of user consent
type ConsentCategory struct {
	CategoryName       string             `json:"category_name" bson:"category_name"`                   // Human-readable category name
	CategoryIdentifier string             `json:"category_identifier" bson:"category_identifier"`       // Identifier used for internal referencing
	OrgHandle          string             `json:"org_handle" bson:"org_handle"`                         // Identifier of the organization
	Purpose            string             `json:"purpose" bson:"purpose"`                               // One of: profiling, personalization, destination
	Destinations       []string           `json:"destinations,omitempty" bson:"destinations,omitempty"` // Optional list of destination names
	Attributes         []ConsentAttribute `json:"attributes,omitempty" bson:"attributes,omitempty"`     // Profile attributes covered by this consent category
	IsMandatory        bool               `json:"is_mandatory" bson:"is_mandatory"`                     // If true, category is system-managed and cannot be modified or deleted
}
