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
	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// syncResult summarizes one sync of an organization tree.
type syncResult struct {
	Total   int
	Added   []string
	Deleted []string
}

// syncLocks prevents concurrent syncs of the same organization tree.
var syncLocks sync.Map

// EnsurePrimary stores the primary organization.
func EnsurePrimary(ctx context.Context, primaryHandle string) (*model.Organization, error) {

	primaryInfo, err := idp.GetAdapter().GetPrimaryOrganization(ctx, primaryHandle)
	if err != nil {
		return nil, err
	}
	primary := model.Organization{
		OrgId:        primaryInfo.Id,
		OrgHandle:    primaryHandle,
		OrgName:      primaryInfo.Name,
		PrimaryOrgId: primaryInfo.Id,
		Status:       model.StatusActive,
	}
	if err := store.UpsertOrganization(ctx, primary); err != nil {
		return nil, err
	}
	return &primary, nil
}

// GetOrStoreOrganization returns the stored organization of the handle. When the handle has no row,
// it reads the primary organization from the identity provider and stores it.
func GetOrStoreOrganization(ctx context.Context, orgHandle string) (*model.Organization, error) {

	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org != nil {
		return org, err
	}
	return EnsurePrimary(ctx, orgHandle)
}

// treeSyncRun is the background sync of one organization tree. again requests one more run after
// the current run.
type treeSyncRun struct {
	again bool
}

var (
	treeSyncMu   sync.Mutex
	treeSyncRuns = map[string]*treeSyncRun{}
)

// StartTreeSync starts a background sync of the organization tree and returns the sync state.
func StartTreeSync(ctx context.Context, primaryHandle string) (*model.TreeSyncStatus, error) {

	if _, err := EnsurePrimary(ctx, primaryHandle); err != nil {
		return nil, err
	}
	treeSyncMu.Lock()
	defer treeSyncMu.Unlock()
	status, err := store.GetTreeSyncStatus(ctx, primaryHandle)
	if err != nil {
		return nil, err
	}
	if status.State == model.TreeSyncSyncing {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.TREE_SYNC_RUNNING.Code,
			Message:     errors2.TREE_SYNC_RUNNING.Message,
			Description: errors2.TREE_SYNC_RUNNING.Description,
		}, http.StatusConflict)
	}
	if err := markSyncing(ctx, primaryHandle); err != nil {
		return nil, err
	}
	scheduleLocked(primaryHandle)
	return store.GetTreeSyncStatus(ctx, primaryHandle)
}

// ResetTreeSync clears the tree sync state of the primary organization.
func ResetTreeSync(ctx context.Context, primaryHandle string) error {

	treeSyncMu.Lock()
	defer treeSyncMu.Unlock()
	if run, running := treeSyncRuns[primaryHandle]; running {
		run.again = false
	}
	status, err := store.GetTreeSyncStatus(ctx, primaryHandle)
	if err != nil {
		return err
	}
	if status.State == model.TreeSyncNotSynced && status.LastSyncedAt == nil {
		return nil
	}
	return store.SetTreeSyncValues(ctx, primaryHandle, clearedTreeSyncValues())
}

// clearedTreeSyncValues returns the tree sync keys with empty values.
func clearedTreeSyncValues() map[string]string {
	return map[string]string{
		model.ConfigTreeSyncState:      "",
		model.ConfigTreeSyncStartedAt:  "",
		model.ConfigTreeSyncFinishedAt: "",
		model.ConfigTreeSyncedAt:       "",
		model.ConfigTreeSyncError:      "",
	}
}

// GetTreeSyncStatus returns the tree sync state of the primary organization.
func GetTreeSyncStatus(ctx context.Context, primaryHandle string) (*model.TreeSyncStatus, error) {
	return store.GetTreeSyncStatus(ctx, primaryHandle)
}

// scheduleLocked starts the sync goroutine. The caller holds treeSyncMu.
func scheduleLocked(primaryHandle string) {

	if run, running := treeSyncRuns[primaryHandle]; running {
		run.again = true
		return
	}
	run := &treeSyncRun{}
	treeSyncRuns[primaryHandle] = run
	go func() {
		for {
			runTreeSync(context.Background(), primaryHandle)
			treeSyncMu.Lock()
			if !run.again {
				delete(treeSyncRuns, primaryHandle)
				treeSyncMu.Unlock()
				return
			}
			run.again = false
			err := markSyncing(context.Background(), primaryHandle)
			treeSyncMu.Unlock()
			if err != nil {
				log.GetLogger().Error(fmt.Sprintf("Failed to store the organization sync state of primary "+
					"organization '%s'.", primaryHandle), log.Error(err))
			}
		}
	}()
}

func markSyncing(ctx context.Context, primaryHandle string) error {
	return store.SetTreeSyncValues(ctx, primaryHandle, map[string]string{
		model.ConfigTreeSyncState:      model.TreeSyncSyncing,
		model.ConfigTreeSyncStartedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		model.ConfigTreeSyncFinishedAt: "",
		model.ConfigTreeSyncError:      "",
	})
}

// runTreeSync syncs the organization tree and stores the end state.
func runTreeSync(ctx context.Context, primaryHandle string) {

	logger := log.GetLogger()
	values := map[string]string{}
	_, err := func() (result *syncResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("the organization sync stopped: %v", r)
			}
		}()
		return syncTree(ctx, primaryHandle)
	}()
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	values[model.ConfigTreeSyncFinishedAt] = finished
	if err != nil {
		logger.Error(fmt.Sprintf("The organization sync of primary organization '%s' failed.", primaryHandle),
			log.Error(err))
		values[model.ConfigTreeSyncState] = model.TreeSyncFailed
		values[model.ConfigTreeSyncError] = err.Error()
	} else {
		values[model.ConfigTreeSyncState] = model.TreeSyncSynced
		values[model.ConfigTreeSyncedAt] = finished
		values[model.ConfigTreeSyncError] = ""
	}
	treeSyncMu.Lock()
	defer treeSyncMu.Unlock()
	if config, err := adminConfigStore.GetAdminConfig(ctx, primaryHandle); err == nil &&
		(config == nil || !config.CDSEnabled) {
		values = clearedTreeSyncValues()
		if run, running := treeSyncRuns[primaryHandle]; running {
			run.again = false
		}
	}
	if err := store.SetTreeSyncValues(ctx, primaryHandle, values); err != nil {
		logger.Error(fmt.Sprintf("Failed to store the organization sync state of primary organization '%s'.",
			primaryHandle), log.Error(err))
	}
}

// syncTree makes the stored organizations of a tree match the source tree.
func syncTree(ctx context.Context, primaryHandle string) (*syncResult, error) {

	lock, _ := syncLocks.LoadOrStore(primaryHandle, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	started := time.Now().UTC()
	primary, err := EnsurePrimary(ctx, primaryHandle)
	if err != nil {
		return nil, err
	}
	existing, err := store.GetOrganizationsByPrimaryOrg(ctx, primary.OrgId)
	if err != nil {
		return nil, err
	}
	known := map[string]model.Organization{}
	for _, o := range existing {
		known[o.OrgId] = o
	}
	descendants, err := idp.GetAdapter().ListDescendants(ctx, primaryHandle)
	if err != nil {
		return nil, err
	}

	orgs := buildTree(*primary, descendants)
	result := &syncResult{Total: len(orgs) + 1, Added: []string{}, Deleted: []string{}}
	seen := map[string]bool{primary.OrgId: true}
	for _, org := range orgs {
		seen[org.OrgId] = true
		previous, wasKnown := known[org.OrgId]
		if err := store.UpsertOrganization(ctx, org); err != nil {
			return nil, err
		}
		if !wasKnown || previous.Status == model.StatusDeleted {
			result.Added = append(result.Added, org.OrgHandle)
		}
	}
	for _, o := range existing {
		if seen[o.OrgId] || o.Status == model.StatusDeleted {
			continue
		}
		current, err := store.GetOrganizationById(ctx, o.OrgId)
		if err != nil {
			return nil, err
		}
		if current == nil || current.Status == model.StatusDeleted || current.UpdatedAt.After(started) {
			continue
		}
		if err := store.UpdateOrganizationStatus(ctx, o.OrgId, model.StatusDeleted); err != nil {
			return nil, err
		}
		result.Deleted = append(result.Deleted, o.OrgHandle)
	}

	log.GetLogger().Info(fmt.Sprintf("Synced the org tree of primary organization '%s': %d orgs, %d added, "+
		"%d deleted.", primaryHandle, result.Total, len(result.Added), len(result.Deleted)))
	return result, nil
}

// buildTree orders the organizations so that each parent comes before its children. Organizations
// that are not connected to the primary organization are left out.
func buildTree(primary model.Organization, descendants []idp.Organization) []model.Organization {

	byId := map[string]idp.Organization{}
	for _, d := range descendants {
		byId[d.Id] = d
	}
	built := map[string]model.Organization{primary.OrgId: primary}
	level := map[string]int{primary.OrgId: 0}
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
			OrgId:        d.Id,
			OrgHandle:    d.Handle,
			OrgName:      d.Name,
			ParentOrgId:  parent.OrgId,
			PrimaryOrgId: primary.OrgId,
			Status:       status,
		}
		built[id] = org
		level[id] = level[parent.OrgId] + 1
		return org, true
	}

	result := make([]model.Organization, 0, len(descendants))
	for _, d := range descendants {
		if org, ok := resolve(d.Id, 0); ok {
			result = append(result, org)
		} else {
			log.GetLogger().Warn(fmt.Sprintf("Skipping organization '%s', because its parent chain does not reach "+
				"the primary organization.", d.Id))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if level[result[i].OrgId] != level[result[j].OrgId] {
			return level[result[i].OrgId] < level[result[j].OrgId]
		}
		return result[i].OrgId < result[j].OrgId
	})
	return result
}

// HandleSyncEvent applies an organization lifecycle event. Events are ignored until the first tree
// sync of the primary organization starts.
func HandleSyncEvent(ctx context.Context, pathHandle string, event model.SyncEvent) error {

	switch event.Event {
	case model.EventOrgCreated, model.EventOrgUpdated, model.EventOrgDeleted:
	default:
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.BAD_REQUEST.Code,
			Message: errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Unsupported organization event '%s'. Use %s, %s, or %s.", event.Event,
				model.EventOrgCreated, model.EventOrgUpdated, model.EventOrgDeleted),
		}, http.StatusBadRequest)
	}
	primaryHandle, err := enabledPrimaryHandle(ctx, pathHandle)
	if err != nil {
		return err
	}
	status, err := store.GetTreeSyncStatus(ctx, primaryHandle)
	if err != nil {
		return err
	}
	primary, err := store.GetOrganizationByHandle(ctx, primaryHandle)
	if err != nil {
		return err
	}
	if status.State == model.TreeSyncNotSynced || primary == nil {
		log.GetLogger().Debug(fmt.Sprintf("Ignoring the organization event %s of '%s', because the organizations "+
			"of primary organization '%s' are not synced.", event.Event, event.OrgId, primaryHandle))
		return nil
	}

	switch event.Event {
	case model.EventOrgCreated, model.EventOrgUpdated:
		info, err := idp.GetAdapter().GetOrganization(ctx, primaryHandle, event.OrgId)
		if err != nil {
			return err
		}
		if info == nil {
			log.GetLogger().Info(fmt.Sprintf("The identity provider does not report organization '%s' under "+
				"primary organization '%s'. Ignoring the event.", event.OrgId, primaryHandle))
			return nil
		}
		parent, err := store.GetOrganizationById(ctx, info.ParentId)
		if err != nil {
			return err
		}
		if parent == nil {
			log.GetLogger().Info(fmt.Sprintf("Ignoring the organization event %s of '%s', because its parent "+
				"'%s' is not stored.", event.Event, info.Id, info.ParentId))
			return nil
		}
		status := model.StatusActive
		if info.Status == model.StatusDisabled {
			status = model.StatusDisabled
		}
		org := model.Organization{
			OrgId:        info.Id,
			OrgHandle:    info.Handle,
			OrgName:      info.Name,
			ParentOrgId:  parent.OrgId,
			PrimaryOrgId: primary.OrgId,
			Status:       status,
		}
		return store.UpsertOrganization(ctx, org)

	case model.EventOrgDeleted:
		previous, err := store.GetOrganizationById(ctx, event.OrgId)
		if err != nil || previous == nil {
			return err
		}
		if previous.IsPrimary() || previous.PrimaryOrgId != primary.OrgId {
			log.GetLogger().Info(fmt.Sprintf("Ignoring the organization event %s of '%s', because it is not a sub "+
				"organization of primary organization '%s'.", event.Event, event.OrgId, primaryHandle))
			return nil
		}
		return store.UpdateOrganizationStatus(ctx, previous.OrgId, model.StatusDeleted)
	}
	return nil
}

// enabledPrimaryHandle returns the handle of the primary organization when CDS is enabled for it.
func enabledPrimaryHandle(ctx context.Context, orgHandle string) (string, error) {

	primaryHandle := orgHandle
	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return "", err
	}
	if org != nil && !org.IsPrimary() {
		primary, err := store.GetOrganizationById(ctx, org.PrimaryOrgId)
		if err != nil {
			return "", err
		}
		if primary != nil {
			primaryHandle = primary.OrgHandle
		}
	}
	config, err := adminConfigStore.GetAdminConfig(ctx, primaryHandle)
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
	return primaryHandle, nil
}

// PrimaryHandleOf returns the handle of the primary organization of the organization. The bool is
// false when the organization is not stored or not active.
func PrimaryHandleOf(ctx context.Context, orgHandle string) (string, bool) {

	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return "", false
	}
	if org.IsPrimary() {
		return org.OrgHandle, true
	}
	if org.Status != model.StatusActive {
		return "", false
	}
	primary, err := store.GetOrganizationById(ctx, org.PrimaryOrgId)
	if err != nil || primary == nil {
		return "", false
	}
	return primary.OrgHandle, true
}

// SubOrgHandleOf returns the handle of a sub organization in the tree of the primary organization.
func SubOrgHandleOf(ctx context.Context, primaryHandle, orgId string) (string, error) {

	org, err := store.GetOrganizationById(ctx, orgId)
	if err != nil {
		return "", err
	}
	notFound := errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.ORGANIZATION_NOT_FOUND.Code,
		Message: errors2.ORGANIZATION_NOT_FOUND.Message,
		Description: fmt.Sprintf("The organization '%s' is not in the organization tree of '%s'.", orgId,
			primaryHandle),
	}, http.StatusNotFound)
	if org == nil || org.IsPrimary() || org.Status == model.StatusDeleted {
		return "", notFound
	}
	primary, err := store.GetOrganizationById(ctx, org.PrimaryOrgId)
	if err != nil {
		return "", err
	}
	if primary == nil || primary.OrgHandle != primaryHandle {
		return "", notFound
	}
	return org.OrgHandle, nil
}

// IsSubOrgHandle reports whether the handle belongs to a stored sub organization.
func IsSubOrgHandle(ctx context.Context, orgHandle string) (bool, error) {

	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return false, err
	}
	return org != nil && !org.IsPrimary(), nil
}

// GetOrganizationByHandle returns the stored organization of the handle, or nil.
func GetOrganizationByHandle(ctx context.Context, orgHandle string) (*model.Organization, error) {
	return store.GetOrganizationByHandle(ctx, orgHandle)
}

// GetOrganizationsByIds returns the stored organizations with the IDs.
func GetOrganizationsByIds(ctx context.Context, orgIds []string) ([]model.Organization, error) {
	return store.GetOrganizationsByIds(ctx, orgIds)
}

// GetAncestors returns the ancestors of the organization, the parent first. The result is empty
// for a primary organization.
func GetAncestors(ctx context.Context, org model.Organization) ([]model.Ancestor, error) {

	if org.IsPrimary() {
		return nil, nil
	}
	return store.GetAncestors(ctx, org.OrgId)
}
