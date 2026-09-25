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

// Resource types that can be shared.
const (
	ResourceSchemaAttribute = "SCHEMA_ATTRIBUTE"
	ResourceUnificationRule = "UNIFICATION_RULE"
)

// Policy stages. The owner shares; an org that received the resource reshares.
const (
	StageShare   = "SHARE"
	StageReshare = "RESHARE"
)

// Target scopes.
const (
	// ScopeAllDescendants reaches all current and future orgs below the initiating org.
	ScopeAllDescendants = "ALL_DESCENDANTS"
	// ScopeOrg reaches one direct child of the initiating org.
	ScopeOrg = "ORG"
	// ScopeOrgSubtree reaches one direct child and all current and future orgs below it.
	ScopeOrgSubtree = "ORG_SUBTREE"
)

// Share states of a resource in one org.
const (
	StateActive                   = "ACTIVE"
	StateConflicted               = "CONFLICTED"
	StateInactiveMissingAttribute = "INACTIVE_MISSING_ATTRIBUTE"
)

// Reasons for a state that is not ACTIVE.
const (
	ReasonLocalNameConflict  = "LOCAL_NAME_CONFLICT"
	ReasonSharedNameConflict = "SHARED_NAME_CONFLICT"
	ReasonMissingAttribute   = "MISSING_ATTRIBUTE"
)

// Origin of a resource in the view of an org.
const (
	OriginOwned  = "OWNED"
	OriginShared = "SHARED"
)

// Target is one entry of the reach of a policy.
type Target struct {
	Scope string `json:"scope"`
	OrgId string `json:"org_id,omitempty"`
}

// Policy says which orgs can see one resource. There is one policy for each resource and
// initiating org.
type Policy struct {
	PolicyId        string
	ResourceType    string
	ResourceId      string
	OwnerOrgId      string
	InitiatingOrgId string
	Stage           string
	Version         int
	Targets         []Target
	ExcludedOrgIds  []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// State is the result of the policies for one resource in one org.
type State struct {
	ResourceType          string `json:"-"`
	ResourceId            string `json:"-"`
	OrgId                 string `json:"org_id"`
	OrgHandle             string `json:"org_handle,omitempty"`
	State                 string `json:"state"`
	Reason                string `json:"reason,omitempty"`
	ConflictingResourceId string `json:"conflicting_resource_id,omitempty"`
}

// ShareRequest is the body of a PUT on a share endpoint.
type ShareRequest struct {
	Targets        []Target `json:"targets"`
	ExcludedOrgIds []string `json:"excluded_org_ids,omitempty"`
}

// ShareResponse is the policy of an org for a resource, with the state in each reached org.
type ShareResponse struct {
	PolicyId        string    `json:"policy_id"`
	ResourceType    string    `json:"resource_type"`
	ResourceId      string    `json:"resource_id"`
	OwnerOrgId      string    `json:"owner_org_id"`
	InitiatingOrgId string    `json:"initiating_org_id"`
	Stage           string    `json:"stage"`
	Version         int       `json:"version"`
	Targets         []Target  `json:"targets"`
	ExcludedOrgIds  []string  `json:"excluded_org_ids"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	States          []State   `json:"states"`
}
