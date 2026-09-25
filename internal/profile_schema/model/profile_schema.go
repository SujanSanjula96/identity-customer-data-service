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

import "github.com/wso2/identity-customer-data-service/internal/system/constants"

type ProfileSchemaAttribute struct {
	OrgId                 string           `json:"org_id,omitempty" bson:"org_id,omitempty"`
	AttributeId           string           `json:"attribute_id" bson:"attribute_id"`
	AttributeName         string           `json:"attribute_name" bson:"attribute_name" binding:"required"`
	Scope                 string           `json:"scope,omitempty" bson:"scope,omitempty"`
	DisplayName           string           `json:"display_name,omitempty" bson:"display_name,omitempty"`
	ValueType             string           `json:"value_type" bson:"value_type" binding:"required"`
	MergeStrategy         string           `json:"merge_strategy" bson:"merge_strategy" binding:"required"`
	Mutability            string           `json:"mutability" bson:"mutability"`
	ApplicationIdentifier string           `json:"application_identifier,omitempty" bson:"application_identifier,omitempty"`
	MultiValued           bool             `json:"multi_valued,omitempty" bson:"multi_valued,omitempty"`         // Means the data type is an array of chosen data type
	CanonicalValues       []CanonicalValue `json:"canonical_values,omitempty" bson:"canonical_values,omitempty"` // String of options for the attribute
	SubAttributes         []SubAttribute   `json:"sub_attributes,omitempty" bson:"sub_attributes,omitempty"`     // If the datatype is object
	SCIMDialect           string           `json:"scim_dialect,omitempty" bson:"scim_dialect,omitempty"`         // Need to skip this in the response
	// B2B: set only on reads of the effective schema of an org.
	Origin         string `json:"origin,omitempty" bson:"-"`           // OWNED or SHARED
	OwnerOrgHandle string `json:"owner_org_handle,omitempty" bson:"-"` // The owner org of a shared attribute
}

type SubAttribute struct {
	AttributeId   string `json:"attribute_id" bson:"attribute_id" binding:"required"`
	AttributeName string `json:"attribute_name" bson:"attribute_name" binding:"required"`
}

type CanonicalValue struct {
	Value string `json:"value" bson:"value"`
	Label string `json:"label" bson:"label"`
}

type Meta struct {
	CreatedAt int    `json:"created_at" bson:"created_at"`
	UpdatedAt int    `json:"updated_at" bson:"updated_at"`
	Location  string `json:"location" bson:"location"`
}

type ProfileSchema struct {
	ProfileId          map[string]string                   `json:"profile_id" bson:"profile_id"`
	UserId             map[string]string                   `json:"user_id" bson:"user_id"`
	Meta               map[string]map[string]string        `json:"meta" bson:"meta"`
	IdentityAttributes []ProfileSchemaAttribute            `json:"identity_attributes" bson:"identity_attributes"`
	Traits             []ProfileSchemaAttribute            `json:"traits" bson:"traits"`
	ApplicationData    map[string][]ProfileSchemaAttribute `json:"application_data" bson:"application_data"`
}

var CoreSchema = map[string]map[string]string{
	"profile_id": {
		"value_type": constants.StringDataType,
		"mutability": constants.MutabilityImmutable,
	},
	"user_id": {
		"value_type": constants.StringDataType,
		"mutability": constants.MutabilityWriteOnce,
	},
	"meta.created_at": {
		"value_type": constants.DateTimeDataType,
		"mutability": constants.MutabilityReadOnly,
	},
	"meta.updated_at": {
		"value_type": constants.DateTimeDataType,
		"mutability": constants.MutabilityReadOnly,
	},
	"meta.location": {
		"value_type": constants.StringDataType,
		"mutability": constants.MutabilityReadOnly,
	},
}
