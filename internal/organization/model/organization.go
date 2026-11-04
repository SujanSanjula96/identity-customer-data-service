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

package model

import "time"

// Organization status values.
const (
	StatusActive   = "ACTIVE"
	StatusDisabled = "DISABLED"
	StatusDeleted  = "DELETED"
)

// Organization holds the details of an organization.
type Organization struct {
	OrgId        string    `json:"org_id"`
	OrgHandle    string    `json:"org_handle"`
	OrgName      string    `json:"org_name,omitempty"`
	ParentOrgId  string    `json:"parent_org_id,omitempty"`
	PrimaryOrgId string    `json:"primary_org_id"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IsPrimary reports whether the organization is a primary organization.
func (o Organization) IsPrimary() bool {
	return o.OrgId == o.PrimaryOrgId
}

// IsActive reports whether the organization is active.
func (o Organization) IsActive() bool {
	return o.Status == StatusActive
}

// Ancestor is an organization above another organization. Depth is the number of levels between
// them. The parent has the depth 1.
type Ancestor struct {
	Organization
	Depth int
}

// SyncEvent is an organization lifecycle event.
type SyncEvent struct {
	Event string `json:"event"`
	OrgId string `json:"org_id"`
}

// Organization lifecycle event types.
const (
	EventOrgCreated = "ORG_CREATED"
	EventOrgUpdated = "ORG_UPDATED"
	EventOrgDeleted = "ORG_DELETED"
)

// Tree sync states. NOT_SYNCED means that no tree sync has started.
const (
	TreeSyncNotSynced = "NOT_SYNCED"
	TreeSyncSyncing   = "SYNCING"
	TreeSyncSynced    = "SYNCED"
	TreeSyncFailed    = "FAILED"
)

// Configuration keys that hold the tree sync state.
const (
	ConfigTreeSyncState      = "tree_sync_state"
	ConfigTreeSyncStartedAt  = "tree_sync_started_at"
	ConfigTreeSyncFinishedAt = "tree_sync_finished_at"
	ConfigTreeSyncedAt       = "tree_synced_at"
	ConfigTreeSyncError      = "tree_sync_error"
)

// TreeSyncStatus is the state of the organization tree sync. LastSyncedAt is the end time of the
// last completed sync.
type TreeSyncStatus struct {
	State        string     `json:"state"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	Error        string     `json:"error,omitempty"`
}
