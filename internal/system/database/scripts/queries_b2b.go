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

// Statements for B2B support. The domains are:
//
//	CDS-ORG  organizations
//	CDS-SHR  cds_share_policy, cds_share_policy_target

const organizationColumns = `org_id, org_handle, org_name, parent_org_id, root_org_id, status, created_at,
	updated_at`

const organizationColumnsOfO = `o.org_id, o.org_handle, o.org_name, o.parent_org_id, o.root_org_id, o.status,
	o.created_at, o.updated_at`

var UpsertOrganization = newQuery("CDS-ORG-01",
	`INSERT INTO organizations (org_id, org_handle, org_name, parent_org_id, root_org_id, status, created_at,
	updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	ON CONFLICT (org_id) DO UPDATE SET org_handle = excluded.org_handle, org_name = excluded.org_name,
	parent_org_id = excluded.parent_org_id, root_org_id = excluded.root_org_id, status = excluded.status,
	updated_at = excluded.updated_at`)

var GetOrganizationById = newQuery("CDS-ORG-02",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_id = $1`)

var GetOrganizationByHandle = newQuery("CDS-ORG-03",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_handle = $1`)

var GetOrganizationsByRoot = newQuery("CDS-ORG-04",
	`SELECT `+organizationColumns+` FROM organizations WHERE root_org_id = $1 ORDER BY org_id`)

var UpdateOrganizationStatus = newQuery("CDS-ORG-05",
	`UPDATE organizations SET status = $1, updated_at = $2 WHERE org_id = $3`)

// GetEnabledRootOrgHandles returns the org handles that enabled CDS themselves.
var GetEnabledRootOrgHandles = newQuery("CDS-ORG-06",
	`SELECT org_handle FROM cds_config WHERE config = 'cds_enabled' AND value = 'true'`)

// chainOf is the ancestor chain of the org $1, the org included. hops is 0 for the org, 1 for its
// parent, and so on. CDS stores no depth: the position in the chain gives the order.
const chainOf = `WITH RECURSIVE chain (org_id, parent_org_id, hops) AS (
	SELECT org_id, parent_org_id, 0 FROM organizations WHERE org_id = $1
	UNION ALL
	SELECT o.org_id, o.parent_org_id, c.hops + 1 FROM organizations o JOIN chain c ON o.org_id = c.parent_org_id)
	`

// GetOrganizationChain returns the org $1 and its ancestors, the root first.
var GetOrganizationChain = newQuery("CDS-ORG-07",
	chainOf+`SELECT `+organizationColumnsOfO+` FROM organizations o JOIN chain c ON c.org_id = o.org_id
	ORDER BY c.hops DESC`)

var InsertSharePolicy = newQuery("CDS-SHR-01",
	`INSERT INTO cds_share_policy (policy_id, resource_type, resource_id, owning_org_id, initiating_org_id)
	VALUES ($1, $2, $3, $4, $5)`)

const sharePolicyColumns = `p.policy_id, p.resource_type, p.resource_id, p.owning_org_id, p.initiating_org_id`

var GetSharePolicyById = newQuery("CDS-SHR-02",
	`SELECT `+sharePolicyColumns+` FROM cds_share_policy p WHERE p.policy_id = $1`)

var GetSharePolicy = newQuery("CDS-SHR-03",
	`SELECT `+sharePolicyColumns+` FROM cds_share_policy p
	WHERE p.resource_type = $1 AND p.resource_id = $2 AND p.initiating_org_id = $3`)

// GetSharePoliciesByRoot returns the share policies of one customer tree.
var GetSharePoliciesByRoot = newQuery("CDS-SHR-04",
	`SELECT `+sharePolicyColumns+` FROM cds_share_policy p JOIN organizations o ON o.org_id = p.owning_org_id
	WHERE o.root_org_id = $1 AND p.resource_type <> 'ORGANIZATION_ACCESS' ORDER BY p.policy_id`)

var GetSharePolicyTargetsByRoot = newQuery("CDS-SHR-05",
	`SELECT t.policy_id, t.target_scope, t.target_org_id FROM cds_share_policy_target t
	JOIN cds_share_policy p ON p.policy_id = t.policy_id JOIN organizations o ON o.org_id = p.owning_org_id
	WHERE o.root_org_id = $1 AND p.resource_type <> 'ORGANIZATION_ACCESS'`)

var GetSharePolicyTargets = newQuery("CDS-SHR-06",
	`SELECT t.policy_id, t.target_scope, t.target_org_id FROM cds_share_policy_target t WHERE t.policy_id = $1`)

var InsertSharePolicyTarget = newQuery("CDS-SHR-07",
	`INSERT INTO cds_share_policy_target (policy_id, target_scope, target_org_id) VALUES ($1, $2, $3)
	ON CONFLICT DO NOTHING`)

var DeleteSharePolicyTargets = newQuery("CDS-SHR-08",
	`DELETE FROM cds_share_policy_target WHERE policy_id = $1`)

var DeleteSharePolicy = newQuery("CDS-SHR-09",
	`DELETE FROM cds_share_policy WHERE policy_id = $1`)

var GetSharePolicyIdsByResource = newQuery("CDS-SHR-10",
	`SELECT policy_id FROM cds_share_policy WHERE resource_type = $1 AND resource_id = $2`)

// reachingOrg selects the targets that reach the org $1: an ORG target names the org, an
// ORG_SUBTREE target names the org or an ancestor, and an ALL_CHILDREN target names an ancestor.
const reachingOrg = chainOf + `SELECT ` + sharePolicyColumns + `, t.target_scope, t.target_org_id
	FROM cds_share_policy_target t
	JOIN chain c ON c.org_id = t.target_org_id
	JOIN cds_share_policy p ON p.policy_id = t.policy_id
	WHERE ((t.target_scope = 'ORG' AND t.target_org_id = $1)
		OR t.target_scope = 'ORG_SUBTREE'
		OR (t.target_scope = 'ALL_CHILDREN' AND t.target_org_id <> $1))`

// GetSharePoliciesReachingOrg returns the share policies whose targets reach the org $1, with the
// targets that reach it.
var GetSharePoliciesReachingOrg = newQuery("CDS-SHR-11",
	reachingOrg+` AND p.resource_type <> 'ORGANIZATION_ACCESS' ORDER BY p.policy_id`)

// GetOrgAccessPoliciesReachingOrg returns the organization access policies whose targets reach
// the org $1.
var GetOrgAccessPoliciesReachingOrg = newQuery("CDS-SHR-12",
	reachingOrg+` AND p.resource_type = 'ORGANIZATION_ACCESS'`)

// reached returns a recursive CTE with the orgs that the policy of the parameter reaches. recurse
// marks the orgs whose children are reached too, and hops counts the levels below the initiating
// org. A share policy names only direct children in child_orgs (oneHop). Organization access can
// name any descendant of the root.
func reached(name, param string, oneHop bool) string {

	parent := ""
	if oneHop {
		parent = ` AND o.parent_org_id = p.initiating_org_id`
	}
	return name + ` (org_id, recurse, hops) AS (
	SELECT o.org_id, 1, 1 FROM cds_share_policy_target t
	JOIN organizations o ON o.parent_org_id = t.target_org_id
	WHERE t.policy_id = ` + param + ` AND t.target_scope = 'ALL_CHILDREN'
	UNION
	SELECT o.org_id, CASE WHEN t.target_scope = 'ORG_SUBTREE' THEN 1 ELSE 0 END, 1 FROM cds_share_policy_target t
	JOIN cds_share_policy p ON p.policy_id = t.policy_id
	JOIN organizations o ON o.org_id = t.target_org_id` + parent + `
	WHERE t.policy_id = ` + param + ` AND t.target_scope IN ('ORG', 'ORG_SUBTREE')
	UNION
	SELECT o.org_id, 1, r.hops + 1 FROM organizations o JOIN ` + name + ` r ON o.parent_org_id = r.org_id
	WHERE r.recurse = 1)`
}

// reachedAndEnabled is the active orgs that the share policy $1 reaches and that the organization
// access policy $2 enables. A share applies only in enabled orgs.
var reachedAndEnabled = `WITH RECURSIVE ` + reached("shared", "$1", true) + `,
	` + reached("enabled", "$2", false) + `
	SELECT o.org_id, o.org_handle, MIN(s.hops) AS hops FROM shared s JOIN organizations o ON o.org_id = s.org_id
	WHERE o.status = 'ACTIVE' AND o.org_id IN (SELECT org_id FROM enabled)
	GROUP BY o.org_id, o.org_handle`

// GetReachedOrgsPage returns one page of the enabled, active orgs that the share policy $1 reaches,
// nearest level first, then by handle. $2 is the organization access policy of the root.
var GetReachedOrgsPage = newQuery("CDS-SHR-13",
	`SELECT org_id, org_handle FROM (`+reachedAndEnabled+`) x ORDER BY hops, org_handle LIMIT $3 OFFSET $4`)

// CountReachedOrgs returns the number of enabled, active orgs that the share policy $1 reaches.
var CountReachedOrgs = newQuery("CDS-SHR-14",
	`SELECT COUNT(*) AS total FROM (`+reachedAndEnabled+`) x`)

const attributeColumns = `ps.attribute_id, ps.attribute_name, ps.scope, ps.display_name, ps.value_type,
	ps.merge_strategy, ps.mutability, ps.application_identifier, ps.multi_valued,
	CAST(ps.sub_attributes AS TEXT) AS sub_attributes, CAST(ps.canonical_values AS TEXT) AS canonical_values,
	ps.org_handle, o.org_id, c.hops AS owner_hops`

// GetSchemaAttributesOfChain returns the attributes of the org $1 and of its ancestors. The read
// for one org needs them: the local attributes, and the attributes that the ancestors share.
var GetSchemaAttributesOfChain = newQuery("CDS-SHR-15",
	chainOf+`SELECT `+attributeColumns+` FROM profile_schema ps JOIN organizations o ON o.org_handle = ps.org_handle
	JOIN chain c ON c.org_id = o.org_id`)

// GetUnificationRulesOfChain returns the rules of the org $1 and of its ancestors, with the number
// of levels from the org to the owner.
var GetUnificationRulesOfChain = newQuery("CDS-SHR-16",
	chainOf+`SELECT r.rule_id, r.org_handle, r.rule_name, r.property_name, r.property_id, r.priority, r.is_active,
	r.created_at, r.updated_at, o.org_id, c.hops AS owner_hops
	FROM unification_rules r JOIN organizations o ON o.org_handle = r.org_handle
	JOIN chain c ON c.org_id = o.org_id`)

// GetSchemaAttributesByRoot returns the attributes of all orgs of one customer tree, for the
// share-time check.
var GetSchemaAttributesByRoot = newQuery("CDS-SHR-17",
	`SELECT ps.attribute_id, ps.attribute_name, ps.scope, ps.value_type, ps.application_identifier,
	ps.org_handle, o.org_id
	FROM profile_schema ps JOIN organizations o ON o.org_handle = ps.org_handle WHERE o.root_org_id = $1`)

// GetUnificationRulesByRoot returns the rules of all orgs of one customer tree, for the share-time
// check.
var GetUnificationRulesByRoot = newQuery("CDS-SHR-18",
	`SELECT r.rule_id, r.property_name, r.property_id, r.org_handle, o.org_id
	FROM unification_rules r JOIN organizations o ON o.org_handle = r.org_handle WHERE o.root_org_id = $1`)
