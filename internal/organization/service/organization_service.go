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

// Package service resolves organizations by handle or ID and keeps the stored organization tree in
// sync with the identity provider.
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
	return StorePrimary(ctx, primaryHandle, primaryInfo)
}

// StorePrimary stores a primary organization that the identity provider has returned.
func StorePrimary(ctx context.Context, primaryHandle string, primaryInfo idp.Organization) (*model.Organization,
	error) {
	primary := model.Organization{
		OrgID:        primaryInfo.ID,
		OrgHandle:    primaryHandle,
		OrgName:      primaryInfo.Name,
		PrimaryOrgID: primaryInfo.ID,
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
func StartTreeSync(ctx context.Context, primary model.Ref) (*model.TreeSyncStatus, error) {
	if _, err := EnsurePrimary(ctx, primary.Handle); err != nil {
		return nil, err
	}
	treeSyncMu.Lock()
	defer treeSyncMu.Unlock()
	status, err := store.GetTreeSyncStatus(ctx, primary.ID)
	if err != nil {
		return nil, err
	}
	if status.State == model.TreeSyncSyncing {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.TreeSyncRunning.Code,
			Message:     errors2.TreeSyncRunning.Message,
			Description: errors2.TreeSyncRunning.Description,
		}, http.StatusConflict)
	}
	if err := markSyncing(ctx, primary.ID); err != nil {
		return nil, err
	}
	scheduleLocked(primary)
	return store.GetTreeSyncStatus(ctx, primary.ID)
}

// ResetTreeSync clears the tree sync state of the primary organization.
func ResetTreeSync(ctx context.Context, primaryID string) error {
	treeSyncMu.Lock()
	defer treeSyncMu.Unlock()
	if run, running := treeSyncRuns[primaryID]; running {
		run.again = false
	}
	status, err := store.GetTreeSyncStatus(ctx, primaryID)
	if err != nil {
		return err
	}
	if status.State == model.TreeSyncNotSynced && status.LastSyncedAt == nil {
		return nil
	}
	return store.SetTreeSyncValues(ctx, primaryID, clearedTreeSyncValues())
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
func GetTreeSyncStatus(ctx context.Context, primaryID string) (*model.TreeSyncStatus, error) {
	return store.GetTreeSyncStatus(ctx, primaryID)
}

// scheduleLocked starts the sync goroutine. The caller holds treeSyncMu.
func scheduleLocked(primary model.Ref) {
	if run, running := treeSyncRuns[primary.ID]; running {
		run.again = true
		return
	}
	run := &treeSyncRun{}
	treeSyncRuns[primary.ID] = run
	go func() {
		for {
			runTreeSync(context.Background(), primary)
			treeSyncMu.Lock()
			if !run.again {
				delete(treeSyncRuns, primary.ID)
				treeSyncMu.Unlock()
				return
			}
			run.again = false
			err := markSyncing(context.Background(), primary.ID)
			treeSyncMu.Unlock()
			if err != nil {
				log.GetLogger().Error(fmt.Sprintf("Failed to store the organization sync state of primary "+
					"organization '%s'.", primary.ID), log.Error(err))
			}
		}
	}()
}

func markSyncing(ctx context.Context, primaryID string) error {
	return store.SetTreeSyncValues(ctx, primaryID, map[string]string{
		model.ConfigTreeSyncState:      model.TreeSyncSyncing,
		model.ConfigTreeSyncStartedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		model.ConfigTreeSyncFinishedAt: "",
		model.ConfigTreeSyncError:      "",
	})
}

// runTreeSync syncs the organization tree and stores the end state.
func runTreeSync(ctx context.Context, primary model.Ref) {
	logger := log.GetLogger()
	values := map[string]string{}
	_, err := func() (result *syncResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("the organization sync stopped: %v", r)
			}
		}()
		return syncTree(ctx, primary.Handle)
	}()
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	values[model.ConfigTreeSyncFinishedAt] = finished
	if err != nil {
		logger.Error(fmt.Sprintf("The organization sync of primary organization '%s' failed.", primary.ID),
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
	if config, err := adminConfigStore.GetAdminConfig(ctx, primary.ID); err == nil &&
		(config == nil || !config.CDSEnabled) {
		values = clearedTreeSyncValues()
		if run, running := treeSyncRuns[primary.ID]; running {
			run.again = false
		}
	}
	if err := store.SetTreeSyncValues(ctx, primary.ID, values); err != nil {
		logger.Error(fmt.Sprintf("Failed to store the organization sync state of primary organization '%s'.",
			primary.ID), log.Error(err))
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
	existing, err := store.GetOrganizationsByPrimaryOrg(ctx, primary.OrgID)
	if err != nil {
		return nil, err
	}
	known := map[string]model.Organization{}
	for _, o := range existing {
		known[o.OrgID] = o
	}
	descendants, err := idp.GetAdapter().ListDescendants(ctx, primaryHandle)
	if err != nil {
		return nil, err
	}

	orgs := buildTree(*primary, descendants)
	result := &syncResult{Total: len(orgs) + 1, Added: []string{}, Deleted: []string{}}
	seen := map[string]bool{primary.OrgID: true}
	for _, org := range orgs {
		seen[org.OrgID] = true
		previous, wasKnown := known[org.OrgID]
		if err := store.UpsertOrganization(ctx, org); err != nil {
			return nil, err
		}
		if !wasKnown || previous.Status == model.StatusDeleted {
			result.Added = append(result.Added, org.OrgHandle)
		}
	}
	for _, o := range existing {
		if seen[o.OrgID] || o.Status == model.StatusDeleted {
			continue
		}
		current, err := store.GetOrganizationByID(ctx, o.OrgID)
		if err != nil {
			return nil, err
		}
		if current == nil || current.Status == model.StatusDeleted || current.UpdatedAt.After(started) {
			continue
		}
		if err := store.UpdateOrganizationStatus(ctx, o.OrgID, model.StatusDeleted); err != nil {
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
	byID := map[string]idp.Organization{}
	for _, d := range descendants {
		byID[d.ID] = d
	}
	built := map[string]model.Organization{primary.OrgID: primary}
	level := map[string]int{primary.OrgID: 0}
	var resolve func(id string, guard int) (model.Organization, bool)
	resolve = func(id string, guard int) (model.Organization, bool) {
		if org, ok := built[id]; ok {
			return org, true
		}
		d, ok := byID[id]
		if !ok || guard > 64 {
			return model.Organization{}, false
		}
		parent, ok := resolve(d.ParentID, guard+1)
		if !ok {
			return model.Organization{}, false
		}
		status := model.StatusActive
		if d.Status == model.StatusDisabled {
			status = model.StatusDisabled
		}
		org := model.Organization{
			OrgID:        d.ID,
			OrgHandle:    d.Handle,
			OrgName:      d.Name,
			ParentOrgID:  parent.OrgID,
			PrimaryOrgID: primary.OrgID,
			Status:       status,
		}
		built[id] = org
		level[id] = level[parent.OrgID] + 1
		return org, true
	}

	result := make([]model.Organization, 0, len(descendants))
	for _, d := range descendants {
		if org, ok := resolve(d.ID, 0); ok {
			result = append(result, org)
		} else {
			log.GetLogger().Warn(fmt.Sprintf("Skipping organization '%s', because its parent chain does not reach "+
				"the primary organization.", d.ID))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if level[result[i].OrgID] != level[result[j].OrgID] {
			return level[result[i].OrgID] < level[result[j].OrgID]
		}
		return result[i].OrgID < result[j].OrgID
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
			Code:    errors2.BadRequest.Code,
			Message: errors2.BadRequest.Message,
			Description: fmt.Sprintf("Unsupported organization event '%s'. Use %s, %s, or %s.", event.Event,
				model.EventOrgCreated, model.EventOrgUpdated, model.EventOrgDeleted),
		}, http.StatusBadRequest)
	}
	primary, err := enabledPrimary(ctx, pathHandle)
	if err != nil {
		return err
	}
	primaryHandle := primary.OrgHandle
	status, err := store.GetTreeSyncStatus(ctx, primary.OrgID)
	if err != nil {
		return err
	}
	if status.State == model.TreeSyncNotSynced {
		log.GetLogger().Debug(fmt.Sprintf("Ignoring the organization event %s of '%s', because the organizations "+
			"of primary organization '%s' are not synced.", event.Event, event.OrgID, primaryHandle))
		return nil
	}

	switch event.Event {
	case model.EventOrgCreated, model.EventOrgUpdated:
		info, err := idp.GetAdapter().GetOrganization(ctx, primaryHandle, event.OrgID)
		if err != nil {
			return err
		}
		if info == nil {
			log.GetLogger().Info(fmt.Sprintf("The identity provider does not report organization '%s' under "+
				"primary organization '%s'. Ignoring the event.", event.OrgID, primaryHandle))
			return nil
		}
		parent, err := store.GetOrganizationByID(ctx, info.ParentID)
		if err != nil {
			return err
		}
		if parent == nil {
			log.GetLogger().Info(fmt.Sprintf("Ignoring the organization event %s of '%s', because its parent "+
				"'%s' is not stored.", event.Event, info.ID, info.ParentID))
			return nil
		}
		status := model.StatusActive
		if info.Status == model.StatusDisabled {
			status = model.StatusDisabled
		}
		org := model.Organization{
			OrgID:        info.ID,
			OrgHandle:    info.Handle,
			OrgName:      info.Name,
			ParentOrgID:  parent.OrgID,
			PrimaryOrgID: primary.OrgID,
			Status:       status,
		}
		return store.UpsertOrganization(ctx, org)

	case model.EventOrgDeleted:
		previous, err := store.GetOrganizationByID(ctx, event.OrgID)
		if err != nil || previous == nil {
			return err
		}
		if previous.IsPrimary() || previous.PrimaryOrgID != primary.OrgID {
			log.GetLogger().Info(fmt.Sprintf("Ignoring the organization event %s of '%s', because it is not a sub "+
				"organization of primary organization '%s'.", event.Event, event.OrgID, primaryHandle))
			return nil
		}
		return store.UpdateOrganizationStatus(ctx, previous.OrgID, model.StatusDeleted)
	}
	return nil
}

// enabledPrimary returns the primary organization when CDS is enabled for it.
func enabledPrimary(ctx context.Context, orgHandle string) (*model.Organization, error) {
	org, err := store.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, cdsNotEnabled()
	}
	primary := org
	if !org.IsPrimary() {
		primary, err = store.GetOrganizationByID(ctx, org.PrimaryOrgID)
		if err != nil {
			return nil, err
		}
		if primary == nil {
			return nil, cdsNotEnabled()
		}
	}
	config, err := adminConfigStore.GetAdminConfig(ctx, primary.OrgID)
	if err != nil {
		return nil, err
	}
	if config == nil || !config.CDSEnabled {
		return nil, cdsNotEnabled()
	}
	return primary, nil
}

func cdsNotEnabled() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.CDSNotEnabled.Code,
		Message:     errors2.CDSNotEnabled.Message,
		Description: errors2.CDSNotEnabled.Description,
	}, http.StatusBadRequest)
}

// PrimaryOf returns the primary organization of the organization with the org ID. The bool is
// false when the organization is not stored or not active.
func PrimaryOf(ctx context.Context, orgID string) (model.Ref, bool) {
	org, err := store.GetOrganizationByID(ctx, orgID)
	if err != nil || org == nil {
		return model.Ref{}, false
	}
	if org.IsPrimary() {
		return org.Ref(), true
	}
	if org.Status != model.StatusActive {
		return model.Ref{}, false
	}
	primary, err := store.GetOrganizationByID(ctx, org.PrimaryOrgID)
	if err != nil || primary == nil {
		return model.Ref{}, false
	}
	return primary.Ref(), true
}

// SubOrgHandleOf returns the handle of a sub organization in the tree of the primary organization.
func SubOrgHandleOf(ctx context.Context, primaryHandle, orgID string) (string, error) {
	org, err := store.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return "", err
	}
	notFound := errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.OrganizationNotFound.Code,
		Message: errors2.OrganizationNotFound.Message,
		Description: fmt.Sprintf("The organization '%s' is not in the organization tree of '%s'.", orgID,
			primaryHandle),
	}, http.StatusNotFound)
	if org == nil || org.IsPrimary() || org.Status == model.StatusDeleted {
		return "", notFound
	}
	primary, err := store.GetOrganizationByID(ctx, org.PrimaryOrgID)
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

// GetOrganizationByID returns the stored organization with the ID, or nil.
func GetOrganizationByID(ctx context.Context, orgID string) (*model.Organization, error) {
	return store.GetOrganizationByID(ctx, orgID)
}

// GetOrganizationsByIDs returns the stored organizations with the IDs.
func GetOrganizationsByIDs(ctx context.Context, orgIDs []string) ([]model.Organization, error) {
	return store.GetOrganizationsByIDs(ctx, orgIDs)
}

// GetAncestors returns the ancestors of the organization, the parent first. The result is empty
// for a primary organization.
func GetAncestors(ctx context.Context, org model.Organization) ([]model.Ancestor, error) {
	if org.IsPrimary() {
		return nil, nil
	}
	return store.GetAncestors(ctx, org.OrgID)
}
