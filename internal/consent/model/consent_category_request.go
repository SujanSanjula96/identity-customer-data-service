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

package models

// ConsentCategoryRequest is the API input model for creating or updating a consent category.
//
// Each attribute is provided as an object with attribute_name and an optional application_identifier:
//
//	{ "attribute_name": "traits.age" }
//	{ "attribute_name": "application_data.last_purchase", "application_identifier": "crm_app" }
//
// category_identifier and scope are never supplied by the caller:
//   - category_identifier is always server-generated (UUID).
//   - scope is derived automatically from the attribute name prefix in the profile schema.
type ConsentCategoryRequest struct {
	CategoryName string             `json:"category_name"`
	Purpose      string             `json:"purpose"`
	Destinations []string           `json:"destinations,omitempty"`
	Attributes   []ConsentAttribute `json:"attributes,omitempty"`
}

// ToCategory converts the request into the internal ConsentCategory model.
// orgHandle and (for updates) categoryIdentifier are injected by the handler.
func (r ConsentCategoryRequest) ToCategory(orgHandle, categoryIdentifier string) ConsentCategory {
	return ConsentCategory{
		CategoryName:       r.CategoryName,
		CategoryIdentifier: categoryIdentifier,
		OrgHandle:          orgHandle,
		Purpose:            r.Purpose,
		Destinations:       r.Destinations,
		Attributes:         r.Attributes,
	}
}
