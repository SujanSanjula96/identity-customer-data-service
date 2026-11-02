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
	"reflect"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
)

func sharedAttribute(id, owner, name, appId string) model.ProfileSchemaAttribute {
	return model.ProfileSchemaAttribute{AttributeId: id, AttributeName: name, ApplicationIdentifier: appId,
		OwnerOrgHandle: owner}
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
		{ResourceId: "a-tier", OwnerOrgHandle: "a", OwnerDepth: 1},
		{ResourceId: "primary-tier", OwnerOrgHandle: "r", OwnerDepth: 2},
		{ResourceId: "primary-plan", OwnerOrgHandle: "r", OwnerDepth: 2},
		{ResourceId: "primary-age", OwnerOrgHandle: "r", OwnerDepth: 2},
		// A resource that does not exist, and a resource of another owner, are skipped.
		{ResourceId: "deleted", OwnerOrgHandle: "r", OwnerDepth: 2},
		{ResourceId: "primary-age", OwnerOrgHandle: "a", OwnerDepth: 1},
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
	shared := []shareModel.SharedResource{{ResourceId: "shop-cart", OwnerOrgHandle: "r", OwnerDepth: 1}}

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
