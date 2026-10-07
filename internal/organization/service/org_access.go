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
	"strings"

	"github.com/google/uuid"
	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	shareStore "github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// The organization access of a root selects the sub orgs that can use CDS. It is a policy in the
// share tables, with resource type ORGANIZATION_ACCESS and resource ID CDS. A root has a maximum of
// one. Without one, no sub org can use CDS.

// IsSubOrgEnabled reports whether the organization access of the root of the org reaches it. The
// caller checks the enablement of the root.
func IsSubOrgEnabled(ctx context.Context, orgHandle string) bool {

	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return false
	}
	enabled, err := sharingService.IsOrgEnabled(ctx, *org)
	return err == nil && enabled
}

// CreateOrgAccess creates the organization access of the root.
func CreateOrgAccess(ctx context.Context, root model.Organization,
	req shareModel.PolicyRequest) (*shareModel.OrgAccessResponse, error) {

	existing, err := orgAccessOf(ctx, root)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, sharingService.PolicyExistsError(existing.PolicyId)
	}
	targets, problems := sharingService.ToTargets(req.TargetOrgScope, root.OrgId)
	if len(problems) > 0 {
		return nil, sharingService.BadRequest(strings.Join(problems, "; "))
	}
	p := shareModel.Policy{
		PolicyId:        uuid.New().String(),
		ResourceType:    shareModel.ResourceOrganizationAccess,
		ResourceId:      shareModel.OrganizationAccessResourceId,
		OwningOrgId:     root.OrgId,
		InitiatingOrgId: root.OrgId,
		Stage:           shareModel.StageShare,
		Targets:         targets,
	}
	orgs, err := validateOrgAccess(ctx, root, p)
	if err != nil {
		return nil, err
	}
	if err := shareStore.CreatePolicy(ctx, p); err != nil {
		return nil, err
	}
	initializeSelected(ctx, orgs, p)
	resp := toOrgAccessResponse(p)
	return &resp, nil
}

// ListOrgAccess returns the organization access policies of the root. The list has a maximum of
// one policy.
func ListOrgAccess(ctx context.Context, root model.Organization) (*shareModel.OrgAccessList, error) {

	list := &shareModel.OrgAccessList{Policies: []shareModel.OrgAccessResponse{}}
	p, err := orgAccessOf(ctx, root)
	if err != nil {
		return nil, err
	}
	if p != nil {
		list.Policies = append(list.Policies, toOrgAccessResponse(*p))
	}
	list.TotalResults = len(list.Policies)
	return list, nil
}

// GetOrgAccess returns the organization access policy of the path.
func GetOrgAccess(ctx context.Context, root model.Organization,
	policyId string) (*shareModel.OrgAccessResponse, error) {

	p, err := orgAccessById(ctx, root, policyId)
	if err != nil {
		return nil, err
	}
	resp := toOrgAccessResponse(*p)
	return &resp, nil
}

// UpdateOrgAccess replaces the selection. A blanket selection (all_children) only narrows, and it
// narrows only by exclusions, which are not in this phase. A change between blanket and selective
// is a DELETE and then a POST.
func UpdateOrgAccess(ctx context.Context, root model.Organization, policyId string,
	req shareModel.PolicyRequest) (*shareModel.OrgAccessResponse, error) {

	p, err := orgAccessById(ctx, root, policyId)
	if err != nil {
		return nil, err
	}
	targets, problems := sharingService.ToTargets(req.TargetOrgScope, root.OrgId)
	if len(problems) > 0 {
		return nil, sharingService.BadRequest(strings.Join(problems, "; "))
	}
	wasBlanket, isBlanket := sharingService.IsBlanket(p.Targets), sharingService.IsBlanket(targets)
	switch {
	case wasBlanket && isBlanket:
		resp := toOrgAccessResponse(*p)
		return &resp, nil
	case wasBlanket != isBlanket:
		return nil, sharingService.BadRequest("A change between all_children and child_orgs is not an update. " +
			"Delete the policy, and then create a new one.")
	}
	p.Targets = targets
	orgs, err := validateOrgAccess(ctx, root, *p)
	if err != nil {
		return nil, err
	}
	if err := shareStore.ReplaceTargets(ctx, p.PolicyId, targets); err != nil {
		return nil, err
	}
	initializeSelected(ctx, orgs, *p)
	resp := toOrgAccessResponse(*p)
	return &resp, nil
}

// DeleteOrgAccess deletes the organization access of the root. No sub org can use CDS after it.
// CDS keeps the data of the sub orgs.
func DeleteOrgAccess(ctx context.Context, root model.Organization, policyId string) error {

	p, err := orgAccessById(ctx, root, policyId)
	if err != nil {
		return err
	}
	return shareStore.DeletePolicy(ctx, p.PolicyId)
}

// EnsureDefaultOrgAccess gives a root that enables CDS an all_children organization access, when
// it has none. This keeps the root cascade as the default.
func EnsureDefaultOrgAccess(ctx context.Context, rootHandle string) error {

	root, err := store.GetOrganizationByHandle(ctx, rootHandle)
	if err != nil || root == nil || !root.IsRoot() {
		return err
	}
	existing, err := orgAccessOf(ctx, *root)
	if err != nil || existing != nil {
		return err
	}
	_, err = CreateOrgAccess(ctx, *root, shareModel.PolicyRequest{
		TargetOrgScope: &shareModel.TargetOrgScope{AllChildren: true}})
	return err
}

func orgAccessOf(ctx context.Context, root model.Organization) (*shareModel.Policy, error) {
	return shareStore.GetPolicy(ctx, shareModel.ResourceOrganizationAccess, shareModel.OrganizationAccessResourceId,
		root.OrgId)
}

// orgAccessById returns the organization access policy of the root. A policy of another root, or
// a share policy, is not found.
func orgAccessById(ctx context.Context, root model.Organization, policyId string) (*shareModel.Policy, error) {

	p, err := shareStore.GetPolicyById(ctx, policyId)
	if err != nil {
		return nil, err
	}
	if p == nil || p.ResourceType != shareModel.ResourceOrganizationAccess || p.InitiatingOrgId != root.OrgId {
		return nil, sharingService.PolicyNotFoundError(policyId)
	}
	return p, nil
}

func validateOrgAccess(ctx context.Context, root model.Organization,
	p shareModel.Policy) ([]model.Organization, error) {

	orgs, err := store.GetOrganizationsByRoot(ctx, root.OrgId)
	if err != nil {
		return nil, err
	}
	if problems := sharingService.ValidatePolicy(treeOf(orgs), p); len(problems) > 0 {
		return nil, sharingService.BadRequest(strings.Join(problems, "; "))
	}
	return orgs, nil
}

// initializeSelected initializes each org that the selection reaches and that is not initialized
// yet.
func initializeSelected(ctx context.Context, orgs []model.Organization, p shareModel.Policy) {

	byId := map[string]model.Organization{}
	for _, o := range orgs {
		byId[o.OrgId] = o
	}
	for _, id := range sharingService.Reach(treeOf(orgs), p) {
		org := byId[id]
		config, err := adminConfigStore.GetAdminConfig(ctx, org.OrgHandle)
		if err != nil {
			log.GetLogger().Warn(fmt.Sprintf("Failed to read the configuration of organization '%s'.",
				org.OrgHandle), log.Error(err))
			continue
		}
		if config == nil || !config.InitialSchemaSyncDone {
			initializeOrg(ctx, org)
		}
	}
}

func treeOf(orgs []model.Organization) []sharingService.TreeOrg {

	result := make([]sharingService.TreeOrg, 0, len(orgs))
	for _, o := range orgs {
		result = append(result, sharingService.TreeOrg{Id: o.OrgId, Handle: o.OrgHandle, ParentId: o.ParentOrgId,
			Depth: o.Depth, Active: o.Status == model.StatusActive})
	}
	return result
}

func toOrgAccessResponse(p shareModel.Policy) shareModel.OrgAccessResponse {
	return shareModel.OrgAccessResponse{
		Id:              p.PolicyId,
		OwningOrgId:     p.OwningOrgId,
		InitiatingOrgId: p.InitiatingOrgId,
		TargetOrgScope:  sharingService.ToTargetOrgScope(p.Targets),
	}
}
