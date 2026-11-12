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
package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func TestExtractClaimKeyFromURI(t *testing.T) {
	assert.Equal(t, "emailaddress", extractClaimKeyFromURI("http://wso2.org/claims/emailaddress"))
	assert.Equal(t, "emailaddress", extractClaimKeyFromURI("http://wso2.org/claims/emailaddress/"))
	assert.Equal(t, "plain", extractClaimKeyFromURI("plain"))
	assert.Equal(t, "", extractClaimKeyFromURI(""))
}

func attributeRow() map[string]interface{} {
	return map[string]interface{}{
		"attribute_id":           "a-1",
		"attribute_name":         "traits.city",
		"scope":                  "traits",
		"display_name":           "City",
		"value_type":             "string",
		"merge_strategy":         "overwrite",
		"mutability":             "readWrite",
		"application_identifier": "",
		"multi_valued":           true,
	}
}

func TestMapRowToProfileAttribute(t *testing.T) {
	row := attributeRow()
	row["sub_attributes"] = `[{"attribute_id":"s-1","attribute_name":"traits.city.name"}]`
	row["canonical_values"] = `[{"value":"CMB","label":"Colombo"}]`

	got := mapRowToProfileAttribute(row)

	assert.Equal(t, "a-1", got.AttributeId)
	assert.Equal(t, "traits.city", got.AttributeName)
	assert.Equal(t, "traits", got.Scope)
	assert.Equal(t, "City", got.DisplayName)
	assert.Equal(t, "string", got.ValueType)
	assert.Equal(t, "overwrite", got.MergeStrategy)
	assert.Equal(t, "readWrite", got.Mutability)
	assert.True(t, got.MultiValued)
	if assert.Len(t, got.SubAttributes, 1) {
		assert.Equal(t, "s-1", got.SubAttributes[0].AttributeId)
	}
	if assert.Len(t, got.CanonicalValues, 1) {
		assert.Equal(t, "Colombo", got.CanonicalValues[0].Label)
	}
}

func TestMapRowToProfileAttributeToleratesMissingAndBadJSON(t *testing.T) {
	// A row that cannot be decoded is logged, so the logger has to exist.
	require.NoError(t, log.Init("ERROR"))
	row := attributeRow()
	delete(row, "scope")
	row["sub_attributes"] = "not json"
	row["canonical_values"] = ""

	got := mapRowToProfileAttribute(row)

	assert.Equal(t, "", got.Scope)
	assert.Empty(t, got.SubAttributes)
	assert.Empty(t, got.CanonicalValues)
}

func TestMapRowToProfileAttributeStringifiesANumericID(t *testing.T) {
	row := attributeRow()
	row["attribute_id"] = 42

	assert.Equal(t, "42", mapRowToProfileAttribute(row).AttributeId)
}
