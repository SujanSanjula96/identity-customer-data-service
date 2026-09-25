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
//	CDS-SHR  cds_share_policy, cds_share_policy_target, cds_share_policy_exclusion, cds_share_state

const organizationColumns = `org_id, org_handle, org_name, parent_org_id, root_org_id, path, depth, status,
	created_at, updated_at, last_synced_at`

var UpsertOrganization = newQuery("CDS-ORG-01",
	`INSERT INTO organizations (org_id, org_handle, org_name, parent_org_id, root_org_id, path, depth, status,
	created_at, updated_at, last_synced_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $9)
	ON CONFLICT (org_id) DO UPDATE SET org_handle = excluded.org_handle, org_name = excluded.org_name,
	parent_org_id = excluded.parent_org_id, root_org_id = excluded.root_org_id, path = excluded.path,
	depth = excluded.depth, status = excluded.status, updated_at = excluded.updated_at,
	last_synced_at = excluded.last_synced_at`)

var GetOrganizationById = newQuery("CDS-ORG-02",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_id = $1`)

var GetOrganizationByHandle = newQuery("CDS-ORG-03",
	`SELECT `+organizationColumns+` FROM organizations WHERE org_handle = $1`)

var GetOrganizationsByRoot = newQuery("CDS-ORG-04",
	`SELECT `+organizationColumns+` FROM organizations WHERE root_org_id = $1 ORDER BY depth, org_id`)

var UpdateOrganizationStatus = newQuery("CDS-ORG-05",
	`UPDATE organizations SET status = $1, updated_at = $2 WHERE org_id = $3`)

// GetEnabledRootOrgHandles returns the org handles that enabled CDS themselves.
var GetEnabledRootOrgHandles = newQuery("CDS-ORG-06",
	`SELECT org_handle FROM cds_config WHERE config = 'cds_enabled' AND value = 'true'`)

var InsertSharePolicy = newQuery("CDS-SHR-01",
	`INSERT INTO cds_share_policy (policy_id, resource_type, resource_id, owner_org_id, initiating_org_id, stage,
	version, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`)

var TouchSharePolicy = newQuery("CDS-SHR-02",
	`UPDATE cds_share_policy SET version = version + 1, updated_at = $1 WHERE policy_id = $2`)

const sharePolicyColumns = `p.policy_id, p.resource_type, p.resource_id, p.owner_org_id, p.initiating_org_id,
	p.stage, p.version, p.created_at, p.updated_at`

var GetSharePolicy = newQuery("CDS-SHR-03",
	`SELECT `+sharePolicyColumns+` FROM cds_share_policy p
	WHERE p.resource_type = $1 AND p.resource_id = $2 AND p.initiating_org_id = $3`)

// GetSharePoliciesByRoot returns the policies of one customer tree, oldest first.
var GetSharePoliciesByRoot = newQuery("CDS-SHR-04",
	`SELECT `+sharePolicyColumns+` FROM cds_share_policy p JOIN organizations o ON o.org_id = p.owner_org_id
	WHERE o.root_org_id = $1 ORDER BY p.created_at, p.policy_id`)

var GetSharePolicyTargetsByRoot = newQuery("CDS-SHR-05",
	`SELECT t.policy_id, t.target_scope, t.target_org_id FROM cds_share_policy_target t
	JOIN cds_share_policy p ON p.policy_id = t.policy_id JOIN organizations o ON o.org_id = p.owner_org_id
	WHERE o.root_org_id = $1`)

var GetSharePolicyExclusionsByRoot = newQuery("CDS-SHR-06",
	`SELECT e.policy_id, e.excluded_org_id FROM cds_share_policy_exclusion e
	JOIN cds_share_policy p ON p.policy_id = e.policy_id JOIN organizations o ON o.org_id = p.owner_org_id
	WHERE o.root_org_id = $1`)

var InsertSharePolicyTarget = newQuery("CDS-SHR-07",
	`INSERT INTO cds_share_policy_target (policy_id, target_scope, target_org_id) VALUES ($1, $2, $3)
	ON CONFLICT DO NOTHING`)

var InsertSharePolicyExclusion = newQuery("CDS-SHR-08",
	`INSERT INTO cds_share_policy_exclusion (policy_id, excluded_org_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`)

var DeleteSharePolicyTargets = newQuery("CDS-SHR-09",
	`DELETE FROM cds_share_policy_target WHERE policy_id = $1`)

var DeleteSharePolicyExclusions = newQuery("CDS-SHR-10",
	`DELETE FROM cds_share_policy_exclusion WHERE policy_id = $1`)

var DeleteSharePolicy = newQuery("CDS-SHR-11",
	`DELETE FROM cds_share_policy WHERE policy_id = $1`)

var GetSharePolicyIdsByResource = newQuery("CDS-SHR-12",
	`SELECT policy_id FROM cds_share_policy WHERE resource_type = $1 AND resource_id = $2`)

var DeleteShareStatesByRoot = newQuery("CDS-SHR-13",
	`DELETE FROM cds_share_state WHERE root_org_id = $1`)

var InsertShareState = newQuery("CDS-SHR-14",
	`INSERT INTO cds_share_state (resource_type, resource_id, org_id, root_org_id, state, reason,
	conflicting_resource_id, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`)

var GetShareStatesByResource = newQuery("CDS-SHR-15",
	`SELECT s.org_id, o.org_handle, s.state, s.reason, s.conflicting_resource_id FROM cds_share_state s
	JOIN organizations o ON o.org_id = s.org_id
	WHERE s.resource_type = $1 AND s.resource_id = $2 ORDER BY o.depth, o.org_handle`)

var GetShareStatesForOrg = newQuery("CDS-SHR-16",
	`SELECT resource_id, state, reason, conflicting_resource_id FROM cds_share_state
	WHERE resource_type = $1 AND org_id = $2`)

// GetSharedSchemaAttributesForOrg returns the shared attributes that are active in an org. It
// reads only the stored share state, and does not walk the policies.
var GetSharedSchemaAttributesForOrg = newQuery("CDS-SHR-17",
	`SELECT ps.attribute_id, ps.attribute_name, ps.scope, ps.display_name, ps.value_type, ps.merge_strategy,
	ps.mutability, ps.application_identifier, ps.multi_valued, CAST(ps.sub_attributes AS TEXT) AS sub_attributes,
	CAST(ps.canonical_values AS TEXT) AS canonical_values, ps.org_handle AS owner_org_handle
	FROM cds_share_state s JOIN profile_schema ps ON ps.attribute_id = s.resource_id
	WHERE s.resource_type = 'SCHEMA_ATTRIBUTE' AND s.org_id = $1 AND s.state = 'ACTIVE'`)

// GetSharedUnificationRulesForOrg returns the shared rules that are active in an org, with the
// depth of the owner org for the evaluation order.
var GetSharedUnificationRulesForOrg = newQuery("CDS-SHR-18",
	`SELECT r.rule_id, r.org_handle, r.rule_name, r.property_name, r.property_id, r.priority, r.is_active,
	r.created_at, r.updated_at, o.depth AS owner_depth
	FROM cds_share_state s JOIN unification_rules r ON r.rule_id = s.resource_id
	JOIN organizations o ON o.org_handle = r.org_handle
	WHERE s.resource_type = 'UNIFICATION_RULE' AND s.org_id = $1 AND s.state = 'ACTIVE'`)

// GetSchemaAttributesByRoot returns the attributes of all orgs of one customer tree, for the
// share evaluation.
var GetSchemaAttributesByRoot = newQuery("CDS-SHR-19",
	`SELECT ps.attribute_id, ps.attribute_name, ps.scope, ps.value_type, ps.org_handle, o.org_id
	FROM profile_schema ps JOIN organizations o ON o.org_handle = ps.org_handle WHERE o.root_org_id = $1`)

// GetUnificationRulesByRoot returns the rules of all orgs of one customer tree, for the share
// evaluation.
var GetUnificationRulesByRoot = newQuery("CDS-SHR-20",
	`SELECT r.rule_id, r.property_name, r.property_id, r.org_handle, o.org_id
	FROM unification_rules r JOIN organizations o ON o.org_handle = r.org_handle WHERE o.root_org_id = $1`)

var DeleteShareStatesByResource = newQuery("CDS-SHR-21",
	`DELETE FROM cds_share_state WHERE resource_type = $1 AND resource_id = $2`)
