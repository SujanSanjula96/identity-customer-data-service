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

package scripts

// Statements for organization management. The domain is CDS-ORG (organizations).

const organizationColumns = `org_id, org_handle, org_name, parent_org_id, primary_org_id, status, created_at,
	updated_at`

const organizationColumnsOfO = `o.org_id, o.org_handle, o.org_name, o.parent_org_id, o.primary_org_id, o.status,
	o.created_at, o.updated_at`

var UpsertOrganization = newQuery("CDS-ORG-01",
	`INSERT INTO organizations (org_id, org_handle, org_name, parent_org_id, primary_org_id, status, created_at,
	updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	ON CONFLICT (org_id) DO UPDATE SET org_handle = excluded.org_handle, org_name = excluded.org_name,
	parent_org_id = excluded.parent_org_id, primary_org_id = excluded.primary_org_id, status = excluded.status,
	updated_at = excluded.updated_at`)

var GetOrganizationById = newQuery("CDS-ORG-02",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_id = $1`)

var GetOrganizationByHandle = newQuery("CDS-ORG-03",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_handle = $1`)

var GetOrganizationsByPrimaryOrg = newQuery("CDS-ORG-04",
	`SELECT `+organizationColumns+` FROM organizations WHERE primary_org_id = $1 ORDER BY org_id`)

var UpdateOrganizationStatus = newQuery("CDS-ORG-05",
	`UPDATE organizations SET status = $1, updated_at = $2 WHERE org_id = $3`)

// maxChainHops limits the ancestor chain, so that a parent cycle cannot make the query endless.
const maxChainHops = "64"

// chainOf is the ancestor chain of the organization $1, the organization included. hops is the
// distance from the organization.
const chainOf = `WITH RECURSIVE chain (org_id, parent_org_id, hops) AS (
	SELECT org_id, parent_org_id, 0 FROM organizations WHERE org_id = $1
	UNION ALL
	SELECT o.org_id, o.parent_org_id, c.hops + 1 FROM organizations o JOIN chain c ON o.org_id = c.parent_org_id
	WHERE c.hops < ` + maxChainHops + `)
	`

// GetOrganizationChain returns the organization $1 and its ancestors, the primary organization
// first.
var GetOrganizationChain = newQuery("CDS-ORG-07",
	chainOf+`SELECT `+organizationColumnsOfO+` FROM organizations o JOIN chain c ON c.org_id = o.org_id
	ORDER BY c.hops DESC`)

// GetOrganizationsByIds returns the organizations with the IDs. The %s is the IN list.
var GetOrganizationsByIds = newQuery("CDS-ORG-08",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_id IN (%s)`)

// GetOrganizationAncestors returns the ancestors of the organization $1, the parent first. hops is
// the distance from the organization.
var GetOrganizationAncestors = newQuery("CDS-ORG-09",
	chainOf+`SELECT `+organizationColumnsOfO+`, c.hops FROM organizations o JOIN chain c ON c.org_id = o.org_id
	WHERE c.hops > 0 ORDER BY c.hops`)
