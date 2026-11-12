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
package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func schemaAttr(name, valueType, mutability string) schemaModel.ProfileSchemaAttribute {
	return schemaModel.ProfileSchemaAttribute{AttributeName: name, ValueType: valueType, Mutability: mutability}
}

func testSchema() schemaModel.ProfileSchema {
	address := schemaAttr("traits.address", constants.ComplexDataType, constants.MutabilityReadWrite)
	address.SubAttributes = []schemaModel.SubAttribute{
		{AttributeId: "city", AttributeName: "traits.address.city"},
		{AttributeId: "zip", AttributeName: "traits.address.zip"},
	}
	tier := schemaAttr("traits.tier", constants.StringDataType, constants.MutabilityReadWrite)
	tier.CanonicalValues = []schemaModel.CanonicalValue{{Value: "gold"}, {Value: "silver"}}
	tags := schemaAttr("traits.tags", constants.StringDataType, constants.MutabilityReadWrite)
	tags.MultiValued = true
	return schemaModel.ProfileSchema{
		IdentityAttributes: []schemaModel.ProfileSchemaAttribute{
			schemaAttr("identity_attributes.email", constants.StringDataType, constants.MutabilityReadWrite),
			schemaAttr("identity_attributes.userid", constants.StringDataType, constants.MutabilityImmutable),
		},
		Traits: []schemaModel.ProfileSchemaAttribute{
			schemaAttr("traits.age", constants.IntegerDataType, constants.MutabilityReadWrite),
			schemaAttr("traits.score", constants.DecimalDataType, constants.MutabilityReadWrite),
			schemaAttr("traits.vip", constants.BooleanDataType, constants.MutabilityReadWrite),
			schemaAttr("traits.created", constants.DateTimeDataType, constants.MutabilityReadWrite),
			schemaAttr("traits.system", constants.StringDataType, constants.MutabilityReadOnly),
			schemaAttr("traits.ssn", constants.StringDataType, constants.MutabilityWriteOnce),
			schemaAttr("traits.code", constants.StringDataType, constants.MutabilityImmutable),
			schemaAttr("traits.odd", constants.StringDataType, "mystery"),
			schemaAttr("traits.address.city", constants.StringDataType, constants.MutabilityReadWrite),
			schemaAttr("traits.address.zip", constants.IntegerDataType, constants.MutabilityReadWrite),
			address, tier, tags,
		},
		ApplicationData: map[string][]schemaModel.ProfileSchemaAttribute{
			"app-1": {schemaAttr("application_data.theme", constants.StringDataType, constants.MutabilityReadWrite)},
		},
	}
}

func requireProfileClientError(t *testing.T, err error) *errors2.ClientError {
	t.Helper()
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	assert.Equal(t, http.StatusBadRequest, clientError.StatusCode)
	return clientError
}

func TestValidateProfileAgainstSchemaAcceptsAValidProfile(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	request := profileModel.ProfileRequest{
		IdentityAttributes: map[string]interface{}{"email": "a@b.c"},
		Traits: map[string]interface{}{
			"age": float64(30), "score": 1.5, "vip": true, "created": "2026-01-01T00:00:00Z",
			"tier": "gold", "tags": []interface{}{"a", "b"},
			"address": map[string]interface{}{"city": "Colombo", "zip": float64(100)},
		},
		ApplicationData: map[string]interface{}{"app-1": map[string]interface{}{"theme": "dark"}},
	}

	err := ValidateProfileAgainstSchema(request, profileModel.Profile{}, testSchema(), false)

	assert.NoError(t, err)
}

func TestValidateProfileAgainstSchemaRejections(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	tests := []struct {
		name    string
		request profileModel.ProfileRequest
		want    string
	}{
		{"an identity attribute that is not in the schema",
			profileModel.ProfileRequest{IdentityAttributes: map[string]interface{}{"nope": "x"}},
			"not defined in schema"},
		{"a trait that is not in the schema",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"nope": "x"}}, "not defined in schema"},
		{"application data for an unknown application",
			profileModel.ProfileRequest{ApplicationData: map[string]interface{}{
				"app-9": map[string]interface{}{"theme": "x"}}},
			"not defined in schema"},
		{"application data that is not an object",
			profileModel.ProfileRequest{ApplicationData: map[string]interface{}{"app-1": "oops"}},
			"must be of object type"},
		{"a string where an integer is expected",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"age": "thirty"}}, "type mismatch"},
		{"a fraction where an integer is expected",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"age": 1.5}}, "type mismatch"},
		{"a number where a boolean is expected",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"vip": 1}}, "type mismatch"},
		{"a scalar where a list is expected",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"tags": "a"}}, "type mismatch"},
		{"a list holding the wrong type",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"tags": []interface{}{1}}}, "type mismatch"},
		{"a value outside the canonical values",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"tier": "bronze"}}, "canonical values"},
		{"a read-only attribute",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"system": "x"}}, "read-only"},
		{"an attribute with an unknown mutability",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"odd": "x"}}, "Unknown mutability"},
		{"a flattened sub-attribute",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"address.city": "x"}}, "nested object"},
		{"a sub-attribute that is not defined",
			profileModel.ProfileRequest{Traits: map[string]interface{}{
				"address": map[string]interface{}{"street": "x"}}},
			"not defined in schema"},
		{"a sub-attribute of the wrong type",
			profileModel.ProfileRequest{Traits: map[string]interface{}{"address": map[string]interface{}{"zip": "x"}}},
			"type mismatch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProfileAgainstSchema(tc.request, profileModel.Profile{}, testSchema(), false)

			clientError := requireProfileClientError(t, err)
			assert.Contains(t, clientError.Description, tc.want)
		})
	}
}

func TestValidateProfileAgainstSchemaChecksEachItemOfAComplexList(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	schema := testSchema()
	for i := range schema.Traits {
		if schema.Traits[i].AttributeName == "traits.address" {
			schema.Traits[i].MultiValued = true
		}
	}
	good := map[string]interface{}{"city": "Colombo"}
	bad := map[string]interface{}{"street": "x"}

	ok := ValidateProfileAgainstSchema(profileModel.ProfileRequest{
		Traits: map[string]interface{}{"address": []interface{}{good, good}}}, profileModel.Profile{}, schema, false)
	failed := ValidateProfileAgainstSchema(profileModel.ProfileRequest{
		Traits: map[string]interface{}{"address": []interface{}{good, bad}}}, profileModel.Profile{}, schema, false)

	assert.NoError(t, ok)
	clientError := requireProfileClientError(t, failed)
	assert.Contains(t, clientError.Description, "address[1]")
}

func TestValidateProfileAgainstSchemaMutabilityOnUpdate(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	existing := profileModel.Profile{Traits: map[string]interface{}{"code": "A1", "ssn": "111"}}

	tests := []struct {
		name   string
		traits map[string]interface{}
		fails  bool
	}{
		{"an immutable attribute set to the same value", map[string]interface{}{"code": "A1"}, false},
		{"an immutable attribute changed", map[string]interface{}{"code": "B2"}, true},
		{"a write-once attribute set to the same value", map[string]interface{}{"ssn": "111"}, false},
		{"a write-once attribute changed after being set", map[string]interface{}{"ssn": "222"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProfileAgainstSchema(profileModel.ProfileRequest{Traits: tc.traits}, existing,
				testSchema(), true)

			if tc.fails {
				_ = requireProfileClientError(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWriteOnceAttributeCanBeSetWhileItIsEmpty(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	existing := profileModel.Profile{Traits: map[string]interface{}{"ssn": ""}}

	err := ValidateProfileAgainstSchema(profileModel.ProfileRequest{Traits: map[string]interface{}{"ssn": "222"}},
		existing, testSchema(), true)

	assert.NoError(t, err)
}

func TestSystemIdentityAttributesSkipTheMutabilityCheck(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	existing := profileModel.Profile{IdentityAttributes: map[string]interface{}{"userid": "u-1"}}

	// identity_attributes.userid is immutable in the schema but the identity server writes it.
	err := ValidateProfileAgainstSchema(
		profileModel.ProfileRequest{IdentityAttributes: map[string]interface{}{"userid": "u-2"}}, existing,
		testSchema(), true)

	assert.NoError(t, err)
}

func TestValuesEqualForMutabilityTreatsEqualNumbersAsEqual(t *testing.T) {
	assert.True(t, valuesEqualForMutability(1, int64(1)))
	assert.True(t, valuesEqualForMutability(int32(2), 2))
	assert.True(t, valuesEqualForMutability(1, 1.0))
	assert.True(t, valuesEqualForMutability(float32(1.5), 1.5))
	assert.True(t, valuesEqualForMutability("a", "a"))
	assert.True(t, valuesEqualForMutability(map[string]interface{}{"a": 1}, map[string]interface{}{"a": 1}))
	assert.False(t, valuesEqualForMutability(1, 2))
	assert.False(t, valuesEqualForMutability("1", 1))
	// Large integers keep their precision.
	assert.False(t, valuesEqualForMutability(int64(1<<60), int64(1<<60+1)))
}

func TestIsValidType(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	obj := map[string]interface{}{"a": 1}
	tests := []struct {
		name        string
		value       interface{}
		valueType   string
		multiValued bool
		want        bool
	}{
		{"string", "x", constants.StringDataType, false, true},
		{"not a string", 1, constants.StringDataType, false, false},
		{"decimal", 1.5, constants.DecimalDataType, false, true},
		{"decimals", []interface{}{1.5, 2.5}, constants.DecimalDataType, true, true},
		{"a bad decimal list", []interface{}{"x"}, constants.DecimalDataType, true, false},
		{"integer from JSON", float64(3), constants.IntegerDataType, false, true},
		{"integer from Go", 3, constants.IntegerDataType, false, true},
		{"integers", []interface{}{float64(1), float64(2)}, constants.IntegerDataType, true, true},
		{"a fractional integer list", []interface{}{1.5}, constants.IntegerDataType, true, false},
		{"boolean", true, constants.BooleanDataType, false, true},
		{"booleans", []interface{}{true, false}, constants.BooleanDataType, true, true},
		{"a bad boolean list", []interface{}{"x"}, constants.BooleanDataType, true, false},
		{"epoch", "1700000000", constants.EpochDataType, false, true},
		{"epochs", []interface{}{"1"}, constants.EpochDataType, true, true},
		{"a bad epoch list", []interface{}{1}, constants.EpochDataType, true, false},
		{"date time", "2026-01-01T00:00:00Z", constants.DateTimeDataType, false, true},
		{"date times", []interface{}{"2026-01-01T00:00:00Z"}, constants.DateTimeDataType, true, true},
		{"a bad date time list", []interface{}{1}, constants.DateTimeDataType, true, false},
		{"date", "2026-01-01", constants.DateDataType, false, true},
		{"dates", []interface{}{"2026-01-01"}, constants.DateDataType, true, true},
		{"a bad date list", []interface{}{1}, constants.DateDataType, true, false},
		{"complex", obj, constants.ComplexDataType, false, true},
		{"complex list", []interface{}{obj}, constants.ComplexDataType, true, true},
		{"a bad complex list", []interface{}{"x"}, constants.ComplexDataType, true, false},
		{"not complex", "x", constants.ComplexDataType, false, false},
		{"an unknown type", "x", "blob", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isValidType(tc.value, tc.valueType, tc.multiValued, nil))
		})
	}
}

func TestIsValidCanonicalValue(t *testing.T) {
	allowed := []schemaModel.CanonicalValue{{Value: "a"}, {Value: "b"}}

	assert.True(t, isValidCanonicalValue("anything", nil))
	assert.True(t, isValidCanonicalValue("a", allowed))
	assert.False(t, isValidCanonicalValue("c", allowed))
	assert.True(t, isValidCanonicalValue([]interface{}{"a", "b"}, allowed))
	assert.False(t, isValidCanonicalValue([]interface{}{"a", "c"}, allowed))
	assert.False(t, isValidCanonicalValue([]interface{}{1}, allowed))
	assert.False(t, isValidCanonicalValue(1, allowed))
}

func TestGetAppDataValue(t *testing.T) {
	data := []profileModel.ApplicationData{
		{AppId: "app-1", AppSpecificData: map[string]interface{}{"theme": "dark"}},
	}

	val, ok := getAppDataValue(data, "app-1", "theme")
	assert.True(t, ok)
	assert.Equal(t, "dark", val)

	_, ok = getAppDataValue(data, "app-1", "lang")
	assert.False(t, ok)
	_, ok = getAppDataValue(data, "app-2", "theme")
	assert.False(t, ok)
	_, ok = getAppDataValue(nil, "app-1", "theme")
	assert.False(t, ok)
}

func TestConvertAppData(t *testing.T) {
	got, err := ConvertAppData(map[string]interface{}{"app-1": map[string]interface{}{"theme": "dark"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "app-1", got[0].AppId)
	assert.Equal(t, map[string]interface{}{"theme": "dark"}, got[0].AppSpecificData)

	empty, err := ConvertAppData(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = ConvertAppData(map[string]interface{}{"app-1": "not an object"})
	_ = requireProfileClientError(t, err)
}

func TestWideAppDataMap(t *testing.T) {
	got := WideAppDataMap(map[string]map[string]interface{}{"app-1": {"theme": "dark"}})

	assert.Equal(t, map[string]interface{}{"app-1": map[string]interface{}{"theme": "dark"}}, got)
	assert.Empty(t, WideAppDataMap(nil))
}
