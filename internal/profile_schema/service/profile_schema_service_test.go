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
	"context"
	"reflect"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func sharedAttribute(id, owner, name, appId string) model.ProfileSchemaAttribute {
	return model.ProfileSchemaAttribute{AttributeId: id, AttributeName: name, ApplicationIdentifier: appId,
		OwnerOrgId: owner}
}

func attributeIds(attrs []model.ProfileSchemaAttribute) []string {
	ids := []string{}
	for _, attr := range attrs {
		ids = append(ids, attr.AttributeId)
	}
	return ids
}

// An owned attribute with the same name wins. Among shared attributes, the attribute of the
// nearest owner wins.
func TestSelectActiveSharedAttributesConflicts(t *testing.T) {

	attrs := []model.ProfileSchemaAttribute{
		sharedAttribute("primary-tier", "r", "traits.tier", ""),
		sharedAttribute("a-tier", "a", "traits.tier", ""),
		sharedAttribute("primary-plan", "r", "traits.plan", ""),
		sharedAttribute("primary-age", "r", "traits.age", ""),
	}
	shared := []shareModel.SharedResource{
		{ResourceId: "a-tier", OwnerOrgId: "a", OwnerDepth: 1},
		{ResourceId: "primary-tier", OwnerOrgId: "r", OwnerDepth: 2},
		{ResourceId: "primary-plan", OwnerOrgId: "r", OwnerDepth: 2},
		{ResourceId: "primary-age", OwnerOrgId: "r", OwnerDepth: 2},
		// A resource that does not exist, and a resource of another owner, are skipped.
		{ResourceId: "deleted", OwnerOrgId: "r", OwnerDepth: 2},
		{ResourceId: "primary-age", OwnerOrgId: "a", OwnerDepth: 1},
	}
	owned := []model.ProfileSchemaAttribute{{AttributeId: "a1-plan", AttributeName: "traits.plan"}}

	got := attributeIds(selectActiveSharedAttributes(shared, attrs, owned))
	if expected := []string{"a-tier", "primary-age"}; !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// Two applications can use the same application data attribute name. A name conflicts only in one
// application.
func TestSelectActiveSharedAttributesScopesAppDataNamesToTheApp(t *testing.T) {

	attrs := []model.ProfileSchemaAttribute{sharedAttribute("shop-cart", "r", "application_data.cart", "shop")}
	shared := []shareModel.SharedResource{{ResourceId: "shop-cart", OwnerOrgId: "r", OwnerDepth: 1}}

	otherApp := []model.ProfileSchemaAttribute{{AttributeId: "other-cart", AttributeName: "application_data.cart",
		ApplicationIdentifier: "other"}}
	if got := attributeIds(selectActiveSharedAttributes(shared, attrs, otherApp)); len(got) != 1 {
		t.Errorf("expected the attribute to be active next to the attribute of another app, got %v", got)
	}
	sameApp := []model.ProfileSchemaAttribute{{AttributeId: "local-cart", AttributeName: "application_data.cart",
		ApplicationIdentifier: "shop"}}
	if got := attributeIds(selectActiveSharedAttributes(shared, attrs, sameApp)); len(got) != 0 {
		t.Errorf("expected a conflict with the attribute of the same app, got %v", got)
	}
}

func complexAttribute(id, owner, name string, subs ...model.ProfileSchemaAttribute) model.ProfileSchemaAttribute {
	attr := sharedAttribute(id, owner, name, "")
	attr.ValueType = constants.ComplexDataType
	for _, sub := range subs {
		attr.SubAttributes = append(attr.SubAttributes,
			model.SubAttribute{AttributeId: sub.AttributeId, AttributeName: sub.AttributeName})
	}
	return attr
}

// A shared complex attribute comes with its sub-attributes at all levels, parent first.
func TestSelectActiveSharedAttributesAddsSubAttributesOfComplexAttribute(t *testing.T) {

	city := sharedAttribute("city", "r", "traits.address.city", "")
	lat := sharedAttribute("lat", "r", "traits.address.geo.lat", "")
	geo := complexAttribute("geo", "r", "traits.address.geo", lat)
	address := complexAttribute("address", "r", "traits.address", city, geo)
	// A sub-attribute of another owner is not in the group.
	address.SubAttributes = append(address.SubAttributes,
		model.SubAttribute{AttributeId: "foreign", AttributeName: "traits.address.zip"})
	foreign := sharedAttribute("foreign", "a", "traits.address.zip", "")

	attrs := []model.ProfileSchemaAttribute{address, city, geo, lat, foreign}
	shared := []shareModel.SharedResource{{ResourceId: "address", OwnerOrgId: "r", OwnerDepth: 1}}

	got := attributeIds(selectActiveSharedAttributes(shared, attrs, nil))
	if expected := []string{"address", "city", "geo", "lat"}; !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// The parent and its sub-attributes are active or not active together. A conflict on a
// sub-attribute name makes the whole complex attribute not active.
func TestSelectActiveSharedAttributesComplexAttributeConflicts(t *testing.T) {

	city := sharedAttribute("r-city", "r", "traits.address.city", "")
	address := complexAttribute("r-address", "r", "traits.address", city)
	plan := sharedAttribute("r-plan", "r", "traits.plan", "")
	attrs := []model.ProfileSchemaAttribute{address, city, plan}
	shared := []shareModel.SharedResource{
		{ResourceId: "r-address", OwnerOrgId: "r", OwnerDepth: 1},
		{ResourceId: "r-plan", OwnerOrgId: "r", OwnerDepth: 1},
	}

	owned := []model.ProfileSchemaAttribute{{AttributeId: "local-city", AttributeName: "traits.address.city"}}
	got := attributeIds(selectActiveSharedAttributes(shared, attrs, owned))
	if expected := []string{"r-plan"}; !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}

	// The complex attribute of a nearer owner wins as one unit.
	aCity := sharedAttribute("a-city", "a", "traits.address.city", "")
	aAddress := complexAttribute("a-address", "a", "traits.address", aCity)
	attrs = append(attrs, aAddress, aCity)
	shared = append([]shareModel.SharedResource{{ResourceId: "a-address", OwnerOrgId: "a", OwnerDepth: 1}},
		shareModel.SharedResource{ResourceId: "r-address", OwnerOrgId: "r", OwnerDepth: 2})
	got = attributeIds(selectActiveSharedAttributes(shared, attrs, nil))
	if expected := []string{"a-address", "a-city"}; !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// A complex attribute can be shared. A sub-attribute cannot be shared alone.
func TestIsShareableAttributeComplexAndSubAttribute(t *testing.T) {

	ctx := context.Background()
	address := complexAttribute("address", "r", "traits.address",
		sharedAttribute("city", "r", "traits.address.city", ""))
	if err := isShareableAttribute(ctx, "r", address); err != nil {
		t.Errorf("expected the complex attribute to be shareable, got %v", err)
	}
	city := sharedAttribute("city", "r", "traits.address.city", "")
	if err := isShareableAttribute(ctx, "r", city); err == nil {
		t.Error("expected an error for a sub-attribute")
	}
}
