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
	"sort"
	"sync"
	"time"

	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	consentService "github.com/wso2/identity-customer-data-service/internal/consent/service"
	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// ProvisionResult summarizes one provisioning of a customer tree.
type ProvisionResult struct {
	RootOrgId string   `json:"root_org_id"`
	Total     int      `json:"total"`
	Added     []string `json:"added"`
	Deleted   []string `json:"deleted"`
}

// provisionLocks serializes the provisioning of one root, so that an event and a reconcile do
// not provision the same tree at the same time.
var provisionLocks sync.Map

// ProvisionTree reads the full org tree of the root from the identity provider, and makes the
// organizations table match it. It initializes each new org, and evaluates the share policies
// of the tree again. It runs when the root enables CDS, on a reconcile, and when an event
// names an org whose parent CDS does not know.
func ProvisionTree(ctx context.Context, rootHandle string) (*ProvisionResult, error) {

	lock, _ := provisionLocks.LoadOrStore(rootHandle, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	logger := log.GetLogger()
	adapter := idp.GetAdapter()

	rootInfo, err := adapter.GetRootOrganization(ctx, rootHandle)
	if err != nil {
		return nil, err
	}
	root := model.Organization{
		OrgId:     rootInfo.Id,
		OrgHandle: rootHandle,
		OrgName:   rootInfo.Name,
		RootOrgId: rootInfo.Id,
		Path:      model.RootPath(rootInfo.Id),
		Depth:     0,
		Status:    model.StatusActive,
	}
	if err := store.UpsertOrganization(ctx, root); err != nil {
		return nil, err
	}

	descendants, err := adapter.ListDescendants(ctx, rootHandle)
	if err != nil {
		return nil, err
	}
	existing, err := store.GetOrganizationsByRoot(ctx, root.OrgId)
	if err != nil {
		return nil, err
	}
	known := map[string]model.Organization{}
	for _, o := range existing {
		known[o.OrgId] = o
	}

	orgs := buildTree(root, descendants)
	result := &ProvisionResult{RootOrgId: root.OrgId, Total: len(orgs) + 1, Added: []string{}, Deleted: []string{}}
	seen := map[string]bool{root.OrgId: true}
	for _, org := range orgs {
		seen[org.OrgId] = true
		previous, wasKnown := known[org.OrgId]
		if err := store.UpsertOrganization(ctx, org); err != nil {
			return nil, err
		}
		if !wasKnown || previous.Status == model.StatusDeleted {
			initializeOrg(ctx, org)
			result.Added = append(result.Added, org.OrgHandle)
		}
	}
	for _, o := range existing {
		if !seen[o.OrgId] && o.Status != model.StatusDeleted {
			if err := store.UpdateOrganizationStatus(ctx, o.OrgId, model.StatusDeleted); err != nil {
				return nil, err
			}
			result.Deleted = append(result.Deleted, o.OrgHandle)
		}
	}

	if err := sharingService.RecomputeTree(ctx, root.OrgId); err != nil {
		return nil, err
	}
	logger.Info(fmt.Sprintf("Provisioned the org tree of root '%s': %d orgs, %d added, %d deleted.", rootHandle,
		result.Total, len(result.Added), len(result.Deleted)))
	return result, nil
}

// buildTree computes the path and depth of each descendant from the parent links. An org whose
// chain does not reach the root is left out.
func buildTree(root model.Organization, descendants []idp.Organization) []model.Organization {

	byId := map[string]idp.Organization{}
	for _, d := range descendants {
		byId[d.Id] = d
	}
	built := map[string]model.Organization{root.OrgId: root}
	var resolve func(id string, guard int) (model.Organization, bool)
	resolve = func(id string, guard int) (model.Organization, bool) {
		if org, ok := built[id]; ok {
			return org, true
		}
		d, ok := byId[id]
		if !ok || guard > 64 {
			return model.Organization{}, false
		}
		parent, ok := resolve(d.ParentId, guard+1)
		if !ok {
			return model.Organization{}, false
		}
		status := model.StatusActive
		if d.Status == model.StatusDisabled {
			status = model.StatusDisabled
		}
		org := model.Organization{
			OrgId:       d.Id,
			OrgHandle:   d.Handle,
			OrgName:     d.Name,
			ParentOrgId: parent.OrgId,
			RootOrgId:   root.OrgId,
			Path:        parent.ChildPath(d.Id),
			Depth:       parent.Depth + 1,
			Status:      status,
		}
		built[id] = org
		return org, true
	}

	result := make([]model.Organization, 0, len(descendants))
	for _, d := range descendants {
		if org, ok := resolve(d.Id, 0); ok {
			result = append(result, org)
		} else {
			log.GetLogger().Warn(fmt.Sprintf("Skipping organization '%s', because its parent chain does not reach "+
				"the root.", d.Id))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Depth != result[j].Depth {
			return result[i].Depth < result[j].Depth
		}
		return result[i].OrgId < result[j].OrgId
	})
	return result
}

// initializeOrg runs the per-org initialization for a new sub org: it syncs the identity
// attributes and seeds the mandatory consent category. A failure is logged, and the next
// reconcile does not repeat it, so it is visible in the org list as initial_sync_done=false.
func initializeOrg(ctx context.Context, org model.Organization) {

	logger := log.GetLogger()
	if err := schemaService.GetProfileSchemaService().SyncProfileSchema(ctx, org.OrgHandle); err != nil {
		logger.Warn(fmt.Sprintf("Failed to sync the identity attributes of organization '%s'.", org.OrgHandle),
			log.Error(err))
		return
	}
	if err := consentService.GetConsentCategoryService().SeedDefaultConsentCategory(ctx, org.OrgHandle); err != nil {
		logger.Warn(fmt.Sprintf("Failed to seed the consent category of organization '%s'.", org.OrgHandle),
			log.Error(err))
		return
	}
	if err := adminConfigStore.UpdateInitialSchemaSyncConfig(ctx, true, org.OrgHandle); err != nil {
		logger.Warn(fmt.Sprintf("Failed to mark the initial sync of organization '%s'.", org.OrgHandle),
			log.Error(err))
	}
}

// HandleSyncEvent applies an org lifecycle event that the identity provider pushes. pathHandle is
// the org of the request path, which is an org of the same tree.
func HandleSyncEvent(ctx context.Context, pathHandle string, event model.SyncEvent) error {

	rootHandle, err := enabledRootHandle(ctx, pathHandle)
	if err != nil {
		return err
	}
	root, err := store.GetOrganizationByHandle(ctx, rootHandle)
	if err != nil {
		return err
	}
	if root == nil {
		_, err := ProvisionTree(ctx, rootHandle)
		return err
	}

	switch event.Event {
	case model.EventOrgCreated, model.EventOrgUpdated:
		info, err := idp.GetAdapter().GetOrganization(ctx, rootHandle, event.OrgId)
		if err != nil {
			return err
		}
		if info == nil {
			log.GetLogger().Info(fmt.Sprintf("The identity provider does not report organization '%s' under "+
				"root '%s'. Ignoring the event.", event.OrgId, rootHandle))
			return nil
		}
		parent, err := store.GetOrganizationById(ctx, info.ParentId)
		if err != nil {
			return err
		}
		previous, err := store.GetOrganizationById(ctx, info.Id)
		if err != nil {
			return err
		}
		// An unknown parent or a move needs the full tree.
		if parent == nil || (previous != nil && previous.ParentOrgId != info.ParentId) {
			_, err := ProvisionTree(ctx, rootHandle)
			return err
		}
		status := model.StatusActive
		if info.Status == model.StatusDisabled {
			status = model.StatusDisabled
		}
		org := model.Organization{
			OrgId:       info.Id,
			OrgHandle:   info.Handle,
			OrgName:     info.Name,
			ParentOrgId: parent.OrgId,
			RootOrgId:   root.OrgId,
			Path:        parent.ChildPath(info.Id),
			Depth:       parent.Depth + 1,
			Status:      status,
		}
		if err := store.UpsertOrganization(ctx, org); err != nil {
			return err
		}
		if previous == nil || previous.Status == model.StatusDeleted {
			initializeOrg(ctx, org)
		}
		return sharingService.RecomputeTree(ctx, root.OrgId)

	case model.EventOrgDeleted:
		previous, err := store.GetOrganizationById(ctx, event.OrgId)
		if err != nil || previous == nil {
			return err
		}
		if err := store.UpdateOrganizationStatus(ctx, previous.OrgId, model.StatusDeleted); err != nil {
			return err
		}
		return sharingService.RecomputeTree(ctx, root.OrgId)

	default:
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.BAD_REQUEST.Code,
			Message: errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Unsupported organization event '%s'. Use %s, %s, or %s.", event.Event,
				model.EventOrgCreated, model.EventOrgUpdated, model.EventOrgDeleted),
		}, http.StatusBadRequest)
	}
}

// enabledRootHandle returns the handle of the root of the org, when the root enabled CDS.
func enabledRootHandle(ctx context.Context, orgHandle string) (string, error) {

	rootHandle := orgHandle
	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return "", err
	}
	if org != nil && !org.IsRoot() {
		root, err := store.GetOrganizationById(ctx, org.RootOrgId)
		if err != nil {
			return "", err
		}
		if root != nil {
			rootHandle = root.OrgHandle
		}
	}
	config, err := adminConfigStore.GetAdminConfig(ctx, rootHandle)
	if err != nil {
		return "", err
	}
	if config == nil || !config.CDSEnabled {
		return "", errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
	}
	return rootHandle, nil
}

// negativeCache remembers the handles that no enabled tree has, so that the just-in-time
// lookup does not call the identity provider on each request.
var negativeCache sync.Map

const negativeCacheTTL = 2 * time.Minute

// RootHandleOf returns the handle of the root of the org. When CDS does not know the org, it
// reconciles the trees of the roots that enabled CDS, once in each cache period (the
// just-in-time safety net). The bool is false when no enabled tree has the org.
func RootHandleOf(ctx context.Context, orgHandle string) (string, bool) {

	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err == nil && org == nil {
		org = provisionJustInTime(ctx, orgHandle)
	}
	if org == nil {
		return "", false
	}
	if org.IsRoot() {
		return org.OrgHandle, true
	}
	if org.Status != model.StatusActive {
		return "", false
	}
	root, err := store.GetOrganizationById(ctx, org.RootOrgId)
	if err != nil || root == nil {
		return "", false
	}
	return root.OrgHandle, true
}

func provisionJustInTime(ctx context.Context, orgHandle string) *model.Organization {

	if until, ok := negativeCache.Load(orgHandle); ok && time.Now().Before(until.(time.Time)) {
		return nil
	}
	logger := log.GetLogger()
	roots, err := store.GetEnabledRootOrgHandles(ctx)
	if err != nil {
		return nil
	}
	for _, rootHandle := range roots {
		if rootHandle == orgHandle {
			continue
		}
		rootOrg, err := store.GetOrganizationByHandle(ctx, rootHandle)
		if err != nil || (rootOrg != nil && !rootOrg.IsRoot()) {
			continue
		}
		if _, err := ProvisionTree(ctx, rootHandle); err != nil {
			logger.Debug(fmt.Sprintf("Just-in-time provisioning of root '%s' failed.", rootHandle), log.Error(err))
			continue
		}
		if org, err := store.GetOrganizationByHandle(ctx, orgHandle); err == nil && org != nil {
			logger.Info(fmt.Sprintf("Provisioned organization '%s' just in time.", orgHandle))
			return org
		}
	}
	negativeCache.Store(orgHandle, time.Now().Add(negativeCacheTTL))
	return nil
}

// ListTree returns the orgs of the tree of the org.
func ListTree(ctx context.Context, orgHandle string) ([]model.Organization, error) {

	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return []model.Organization{}, nil
	}
	return store.GetOrganizationsByRoot(ctx, org.RootOrgId)
}
