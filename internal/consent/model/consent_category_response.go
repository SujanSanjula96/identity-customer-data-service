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

// ConsentCategoryResponse is the API-facing representation of a consent category.
//
// Attributes are returned as a list of objects, each with attribute_name and an optional
// application_identifier (only present for applicationData-scoped attributes):
//
//	{ "attribute_name": "traits.age" }
//	{ "attribute_name": "application_data.last_purchase", "application_identifier": "crm_app" }
//
// org_handle is intentionally omitted — it is an internal field not relevant to callers.
type ConsentCategoryResponse struct {
	CategoryName       string             `json:"category_name"`
	CategoryIdentifier string             `json:"category_identifier"`
	Purpose            string             `json:"purpose"`
	Destinations       []string           `json:"destinations,omitempty"`
	Attributes         []ConsentAttribute `json:"attributes,omitempty"`
	IsMandatory        bool               `json:"is_mandatory"`
}

// ToResponse converts an internal ConsentCategory to its API response form.
func (c ConsentCategory) ToResponse() ConsentCategoryResponse {
	attrs := make([]ConsentAttribute, 0, len(c.Attributes))
	for _, a := range c.Attributes {
		attrs = append(attrs, ConsentAttribute{
			Scope:                 a.Scope,
			AttributeName:         a.AttributeName,
			ApplicationIdentifier: a.ApplicationIdentifier,
		})
	}
	resp := ConsentCategoryResponse{
		CategoryName:       c.CategoryName,
		CategoryIdentifier: c.CategoryIdentifier,
		Purpose:            c.Purpose,
		Destinations:       c.Destinations,
		IsMandatory:        c.IsMandatory,
	}
	if len(attrs) > 0 {
		resp.Attributes = attrs
	}
	return resp
}
