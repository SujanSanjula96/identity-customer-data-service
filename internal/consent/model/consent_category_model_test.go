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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToCategoryInjectsTheOrganizationAndTheIdentifier(t *testing.T) {
	request := ConsentCategoryRequest{
		CategoryName: "Analytics",
		Purpose:      "profiling",
		Destinations: []string{"warehouse"},
		Attributes:   []ConsentAttribute{{AttributeName: "traits.age"}},
	}

	category := request.ToCategory("org-1", "cat-1")

	assert.Equal(t, "Analytics", category.CategoryName)
	assert.Equal(t, "cat-1", category.CategoryIdentifier)
	assert.Equal(t, "org-1", category.OrgId)
	assert.Equal(t, "profiling", category.Purpose)
	assert.Equal(t, []string{"warehouse"}, category.Destinations)
	assert.Equal(t, request.Attributes, category.Attributes)
	assert.False(t, category.IsMandatory)
}

func TestToResponseLeavesOutInternalFields(t *testing.T) {
	category := ConsentCategory{
		CategoryName:       "Analytics",
		CategoryIdentifier: "cat-1",
		OrgId:              "org-1",
		Purpose:            "profiling",
		IsMandatory:        true,
		Attributes: []ConsentAttribute{
			{Scope: "traits", AttributeName: "traits.age", AttributeId: "attr-1"},
			{Scope: "application_data", AttributeName: "application_data.x", AttributeId: "attr-2",
				ApplicationIdentifier: "crm"},
		},
	}

	response := category.ToResponse()

	assert.Equal(t, "cat-1", response.CategoryIdentifier)
	assert.True(t, response.IsMandatory)
	assert.Equal(t, []ConsentAttribute{
		{Scope: "traits", AttributeName: "traits.age"},
		{Scope: "application_data", AttributeName: "application_data.x", ApplicationIdentifier: "crm"},
	}, response.Attributes, "the schema attribute ID is not part of the response")
}

func TestToResponseOmitsAttributesWhenThereAreNone(t *testing.T) {
	response := ConsentCategory{CategoryName: "Analytics"}.ToResponse()

	assert.Nil(t, response.Attributes)
}
