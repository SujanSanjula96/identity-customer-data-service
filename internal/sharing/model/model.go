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

// The organization access policy selects the sub organizations that can use CDS. Each shareable
// resource defines its own resource type.
const (
	ResourceOrganizationAccess   = "ORGANIZATION_ACCESS"
	OrganizationAccessResourceId = "CDS"
)

// Stored target scopes.
const (
	// ScopeAllChildren reaches all current and future organizations below the initiating
	// organization. The target row stores the initiating organization.
	ScopeAllChildren = "ALL_CHILDREN"
	// ScopeOrg reaches one direct child of the initiating organization.
	ScopeOrg = "ORG"
	// ScopeOrgSubtree reaches one direct child and all current and future organizations below it.
	ScopeOrgSubtree = "ORG_SUBTREE"
)

// Share states of a resource in one organization. A resource can add other states.
const (
	StateActive     = "ACTIVE"
	StateConflicted = "CONFLICTED"
)

// Origin of a resource in the view of an organization.
const (
	OriginOwned  = "OWNED"
	OriginShared = "SHARED"
)

// Target is one stored target of a policy.
type Target struct {
	Scope string
	OrgId string
}

// Policy defines which organizations can see one resource. There is one policy for each resource
// and initiating organization.
type Policy struct {
	PolicyId        string
	ResourceType    string
	ResourceId      string
	OwningOrgId     string
	InitiatingOrgId string
	Targets         []Target
}

// SharedResource is a resource that an ancestor shares with an organization. OwnerDepth is the
// number of levels from the organization up to the owner. The parent has the depth 1.
type SharedResource struct {
	ResourceId     string
	OwnerOrgId     string
	OwnerOrgHandle string
	OwnerDepth     int
}

// Modes of child_orgs.
const (
	// ChildOrgsModeAll reaches all current and future organizations below the initiating
	// organization.
	ChildOrgsModeAll = "all"
	// ChildOrgsModeSelected reaches the organizations that `orgs` names.
	ChildOrgsModeSelected = "selected"
)

// Reaches of a selected organization.
const (
	// ReachOrg reaches the selected organization only.
	ReachOrg = "org"
	// ReachSubtree reaches the selected organization and all current and future organizations below
	// it.
	ReachSubtree = "subtree"
)

// ChildOrg is one selected organization of child_orgs, with its reach.
type ChildOrg struct {
	OrgId string `json:"org_id"`
	Reach string `json:"reach"`
}

// ChildOrgs is the reach of a policy in the API. Mode is all organizations below the initiating
// organization, or the selected organizations.
type ChildOrgs struct {
	Mode string     `json:"mode"`
	Orgs []ChildOrg `json:"orgs,omitempty"`
}

// PolicyRequest is the body of a POST or a PUT on a sharing policy.
type PolicyRequest struct {
	ChildOrgs *ChildOrgs `json:"child_orgs"`
}

// PolicyResponse is a sharing policy in the API.
type PolicyResponse struct {
	Id              string    `json:"id"`
	ResourceType    string    `json:"resource_type"`
	ResourceId      string    `json:"resource_id"`
	OwningOrgId     string    `json:"owning_org_id"`
	InitiatingOrgId string    `json:"initiating_org_id"`
	ChildOrgs       ChildOrgs `json:"child_orgs"`
}

// PolicyList is the list of the policies of a resource.
type PolicyList struct {
	TotalResults int              `json:"total_results"`
	Policies     []PolicyResponse `json:"policies"`
}

// OrgAccessResponse is the organization access policy of a primary organization in the API.
type OrgAccessResponse struct {
	Id              string    `json:"id"`
	OwningOrgId     string    `json:"owning_org_id"`
	InitiatingOrgId string    `json:"initiating_org_id"`
	ChildOrgs       ChildOrgs `json:"child_orgs"`
}

// OrgAccessList is the list of the organization access policies of a primary organization.
type OrgAccessList struct {
	TotalResults int                 `json:"total_results"`
	Policies     []OrgAccessResponse `json:"policies"`
}
