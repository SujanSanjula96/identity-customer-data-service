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

// Statements for sharing. The domain is CDS-SHR (resource_sharing_policy, resource_sharing_policy_target).

var InsertSharePolicy = newQuery("CDS-SHR-01",
	`INSERT INTO resource_sharing_policy (policy_id, resource_type, resource_id, owning_org_id, initiating_org_id)
	VALUES ($1, $2, $3, $4, $5)`)

const sharePolicyColumns = `p.policy_id, p.resource_type, p.resource_id, p.owning_org_id, p.initiating_org_id`

var GetSharePolicyById = newQuery("CDS-SHR-02",
	`SELECT `+sharePolicyColumns+` FROM resource_sharing_policy p WHERE p.policy_id = $1`)

var GetSharePolicy = newQuery("CDS-SHR-03",
	`SELECT `+sharePolicyColumns+` FROM resource_sharing_policy p
	WHERE p.resource_type = $1 AND p.resource_id = $2 AND p.initiating_org_id = $3`)

var GetSharePolicyTargets = newQuery("CDS-SHR-06",
	`SELECT t.policy_id, t.target_scope, t.target_org_id FROM resource_sharing_policy_target t WHERE t.policy_id = $1`)

// InsertSharePolicyTargets stores the targets of a policy. The %s is the VALUES list.
var InsertSharePolicyTargets = newQuery("CDS-SHR-07",
	`INSERT INTO resource_sharing_policy_target (policy_id, target_scope, target_org_id) VALUES %s
	ON CONFLICT DO NOTHING`)

var DeleteSharePolicyTargets = newQuery("CDS-SHR-08",
	`DELETE FROM resource_sharing_policy_target WHERE policy_id = $1`)

var DeleteSharePolicy = newQuery("CDS-SHR-09",
	`DELETE FROM resource_sharing_policy WHERE policy_id = $1`)

var GetSharePolicyIdsByResource = newQuery("CDS-SHR-10",
	`SELECT policy_id FROM resource_sharing_policy WHERE resource_type = $1 AND resource_id = $2`)

// GetSharePoliciesByOwners returns the policies of the resource type $1 that the organizations
// own, with their targets. The %s is the IN list of owner organization IDs.
var GetSharePoliciesByOwners = newQuery("CDS-SHR-13",
	`SELECT `+sharePolicyColumns+`, t.target_scope, t.target_org_id
	FROM resource_sharing_policy p JOIN resource_sharing_policy_target t ON t.policy_id = p.policy_id
	WHERE p.resource_type = $1 AND p.owning_org_id IN (%s) ORDER BY p.policy_id`)
