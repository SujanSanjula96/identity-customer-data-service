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

import (
	"strings"
	"time"
)

// Organization status values.
const (
	StatusActive   = "ACTIVE"
	StatusDisabled = "DISABLED"
	StatusDeleted  = "DELETED"
)

// Organization is the CDS view of one org of the identity provider. CDS keys an org by the
// stable org ID of the identity provider. The handle is an alias for routing.
type Organization struct {
	OrgId        string    `json:"org_id"`
	OrgHandle    string    `json:"org_handle"`
	OrgName      string    `json:"org_name,omitempty"`
	ParentOrgId  string    `json:"parent_org_id,omitempty"`
	RootOrgId    string    `json:"root_org_id"`
	Path         string    `json:"path"`
	Depth        int       `json:"depth"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
}

// IsRoot reports whether the org is the root of a customer tree.
func (o Organization) IsRoot() bool {
	return o.OrgId == o.RootOrgId
}

// IsDescendantOf reports whether the org is below the other org in the tree.
func (o Organization) IsDescendantOf(other Organization) bool {
	return o.OrgId != other.OrgId && strings.HasPrefix(o.Path, other.Path)
}

// ChildPath returns the materialized path of a direct child of this org.
func (o Organization) ChildPath(childId string) string {
	return o.Path + childId + "/"
}

// RootPath returns the materialized path of a root org.
func RootPath(rootId string) string {
	return "/" + rootId + "/"
}

// SyncEvent is an org lifecycle event that the identity provider pushes to CDS.
type SyncEvent struct {
	Event       string `json:"event"`
	OrgId       string `json:"org_id"`
	ParentOrgId string `json:"parent_org_id,omitempty"`
	OrgHandle   string `json:"org_handle,omitempty"`
	OrgName     string `json:"org_name,omitempty"`
}

// Org lifecycle event names that CDS accepts. They are neutral to the identity provider. The IS
// extension maps the IS event names to them.
const (
	EventOrgCreated = "ORG_CREATED"
	EventOrgUpdated = "ORG_UPDATED"
	EventOrgDeleted = "ORG_DELETED"
)
