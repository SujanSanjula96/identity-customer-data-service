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
	"fmt"
	"net/http"
	"strings"

	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	psstr "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// The effective schema of an org is the set of attributes that CDS uses to validate, return, and
// unify the profiles of the org: the attributes that the org owns, and the shared attributes
// that are ACTIVE in the org.

// GetEffectiveProfileSchemaAttributes returns the effective schema of the org.
func GetEffectiveProfileSchemaAttributes(ctx context.Context,
	orgHandle string) ([]model.ProfileSchemaAttribute, error) {

	owned, err := psstr.GetProfileSchemaAttributesForOrg(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	return withSharedAttributes(ctx, orgHandle, owned, "")
}

// withSharedAttributes adds the active shared attributes of the org to the owned attributes. When
// scope is not empty, it adds only the shared attributes of that scope. The origin fields are set
// only when CDS knows the org, so the output does not change for an org without B2B data.
func withSharedAttributes(ctx context.Context, orgHandle string, owned []model.ProfileSchemaAttribute,
	scope string) ([]model.ProfileSchemaAttribute, error) {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return owned, nil
	}
	result := make([]model.ProfileSchemaAttribute, 0, len(owned))
	for _, attr := range owned {
		attr.Origin = shareModel.OriginOwned
		result = append(result, attr)
	}
	shared, err := sharingService.ActiveSharedAttributes(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	for _, s := range shared {
		if scope != "" && s.Attribute.Scope != scope {
			continue
		}
		attr := s.Attribute
		attr.Origin = shareModel.OriginShared
		attr.OwnerOrgHandle = s.OwnerOrgHandle
		result = append(result, attr)
	}
	return result, nil
}

// getVisibleSharedAttribute returns the shared attribute when it is ACTIVE in the org, or nil.
func getVisibleSharedAttribute(ctx context.Context, orgHandle,
	attributeId string) (*model.ProfileSchemaAttribute, error) {

	shared, err := sharingService.ActiveSharedAttributes(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	for _, s := range shared {
		if s.Attribute.AttributeId == attributeId {
			attr := s.Attribute
			attr.Origin = shareModel.OriginShared
			attr.OwnerOrgHandle = s.OwnerOrgHandle
			return &attr, nil
		}
	}
	return nil, nil
}

// checkSharedNameConflict refuses a local attribute name that an active shared attribute of the
// org uses.
func checkSharedNameConflict(ctx context.Context, orgHandle, attributeName string) error {

	shared, err := sharingService.ActiveSharedAttributes(ctx, orgHandle)
	if err != nil {
		return err
	}
	for _, s := range shared {
		if s.Attribute.AttributeName == attributeName {
			return sharingService.ConflictError(fmt.Sprintf("The attribute '%s' is shared with this organization "+
				"by '%s'. Use the shared attribute, or ask the owner to exclude this organization.", attributeName,
				s.OwnerOrgHandle))
		}
	}
	return nil
}

// recomputeShares evaluates the share states of the tree of the org again, after a change to the
// attributes of the org.
func recomputeShares(ctx context.Context, orgHandle string) {

	if err := sharingService.RecomputeForOrgHandle(ctx, orgHandle); err != nil {
		log.GetLogger().Warn(fmt.Sprintf("Failed to evaluate the share states after a schema change in "+
			"organization '%s'.", orgHandle), log.Error(err))
	}
}

// identityAttributeSourceHandle returns the org to read the identity attributes of the org from.
func identityAttributeSourceHandle(ctx context.Context, orgHandle string) string {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil || org.IsRoot() {
		return orgHandle
	}
	root, err := orgStore.GetOrganizationById(ctx, org.RootOrgId)
	if err != nil || root == nil {
		return orgHandle
	}
	return idp.GetAdapter().IdentityAttributeSourceHandle(orgHandle, root.OrgHandle)
}

// keepIdentityAttributeIds gives each synced attribute the ID that the org already stores for
// the same name. The identity client makes a new ID on each read, and the upsert matches on the
// ID. Without this, each sync deletes the old rows, and the foreign key cascade deletes the
// unification rules on identity attributes.
func keepIdentityAttributeIds(ctx context.Context, orgHandle string, incoming []model.ProfileSchemaAttribute) (
	[]model.ProfileSchemaAttribute, error) {

	existing, err := psstr.GetProfileSchemaAttributesByScope(ctx, orgHandle, constants.IdentityAttributes)
	if err != nil {
		return nil, err
	}
	idByName := map[string]string{}
	for _, attr := range existing {
		idByName[attr.AttributeName] = attr.AttributeId
	}
	finalIdByName := map[string]string{}
	for i := range incoming {
		incoming[i].OrgId = orgHandle
		if id, ok := idByName[incoming[i].AttributeName]; ok {
			incoming[i].AttributeId = id
		}
		finalIdByName[incoming[i].AttributeName] = incoming[i].AttributeId
	}
	for i := range incoming {
		for j := range incoming[i].SubAttributes {
			if id, ok := finalIdByName[incoming[i].SubAttributes[j].AttributeName]; ok {
				incoming[i].SubAttributes[j].AttributeId = id
			}
		}
	}
	return incoming, nil
}

// isShareableAttribute checks that the attribute can be shared in this phase.
func isShareableAttribute(attr model.ProfileSchemaAttribute) error {

	reject := func(description string) error {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.SHARE_BAD_REQUEST.Code,
			Message:     errors2.SHARE_BAD_REQUEST.Message,
			Description: description,
		}, http.StatusBadRequest)
	}
	scope := strings.SplitN(attr.AttributeName, ".", 2)[0]
	switch {
	case scope == constants.IdentityAttributes:
		return reject("Identity attributes are not shared. The identity provider controls them for each organization.")
	case scope == constants.ApplicationData:
		return reject("Sharing of application data attributes is not supported in this phase.")
	case attr.ValueType == constants.ComplexDataType:
		return reject("Sharing of complex attributes is not supported in this phase.")
	case strings.Count(attr.AttributeName, ".") > 1:
		return reject("Sharing of a sub-attribute is not supported in this phase.")
	}
	return nil
}
