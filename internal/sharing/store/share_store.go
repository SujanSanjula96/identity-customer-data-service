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

package store

import (
	"context"
	"encoding/json"
	"fmt"

	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	ruleModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

func serverError(description string, err error) error {
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.SHARE_STORE.Code,
		Message:     errors2.SHARE_STORE.Message,
		Description: description,
	}, err)
}

func query(ctx context.Context, q dbmodel.DBQuery, args ...interface{}) ([]map[string]interface{}, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client for sharing.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, q, args...)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to run the sharing statement %s.", q.ID), err)
	}
	return results, nil
}

// inTx runs the statements of fn in one transaction.
func inTx(ctx context.Context, description string, fn func(tx *dbmodel.Tx) error) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return serverError("Failed to get a database client for sharing.", err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return serverError("Failed to start a transaction to "+description+".", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return serverError("Failed to commit the transaction to "+description+".", err)
	}
	return nil
}

// CreatePolicy stores a new policy with its targets. The unique key on the resource and the
// initiating org refuses a second policy; the service checks it first.
func CreatePolicy(ctx context.Context, p model.Policy) error {

	return inTx(ctx, "create a share policy", func(tx *dbmodel.Tx) error {
		if _, err := tx.ExecContext(ctx, scripts.InsertSharePolicy, p.PolicyId, p.ResourceType, p.ResourceId,
			p.OwningOrgId, p.InitiatingOrgId); err != nil {
			return serverError("Failed to store the share policy.", err)
		}
		return insertTargets(ctx, tx, p.PolicyId, p.Targets)
	})
}

// ReplaceTargets replaces the targets of a policy.
func ReplaceTargets(ctx context.Context, policyId string, targets []model.Target) error {

	return inTx(ctx, "replace the share policy targets", func(tx *dbmodel.Tx) error {
		if _, err := tx.ExecContext(ctx, scripts.DeleteSharePolicyTargets, policyId); err != nil {
			return serverError("Failed to replace the share policy targets.", err)
		}
		return insertTargets(ctx, tx, policyId, targets)
	})
}

func insertTargets(ctx context.Context, tx *dbmodel.Tx, policyId string, targets []model.Target) error {

	for _, target := range targets {
		if _, err := tx.ExecContext(ctx, scripts.InsertSharePolicyTarget, policyId, target.Scope,
			target.OrgId); err != nil {
			return serverError("Failed to store a share policy target.", err)
		}
	}
	return nil
}

func policyOf(row map[string]interface{}) model.Policy {
	return model.Policy{
		PolicyId:        rows.String(row, "policy_id"),
		ResourceType:    rows.String(row, "resource_type"),
		ResourceId:      rows.String(row, "resource_id"),
		OwningOrgId:     rows.String(row, "owning_org_id"),
		InitiatingOrgId: rows.String(row, "initiating_org_id"),
	}
}

func targetOf(row map[string]interface{}) model.Target {
	return model.Target{Scope: rows.String(row, "target_scope"), OrgId: rows.String(row, "target_org_id")}
}

func withTargets(ctx context.Context, results []map[string]interface{}) (*model.Policy, error) {

	if len(results) == 0 {
		return nil, nil
	}
	p := policyOf(results[0])
	targetRows, err := query(ctx, scripts.GetSharePolicyTargets, p.PolicyId)
	if err != nil {
		return nil, err
	}
	for _, row := range targetRows {
		p.Targets = append(p.Targets, targetOf(row))
	}
	return &p, nil
}

// GetPolicyById returns the policy with its targets, or nil.
func GetPolicyById(ctx context.Context, policyId string) (*model.Policy, error) {

	results, err := query(ctx, scripts.GetSharePolicyById, policyId)
	if err != nil {
		return nil, err
	}
	return withTargets(ctx, results)
}

// GetPolicy returns the policy of the initiating org for the resource, with its targets, or nil.
func GetPolicy(ctx context.Context, resourceType, resourceId, initiatingOrgId string) (*model.Policy, error) {

	results, err := query(ctx, scripts.GetSharePolicy, resourceType, resourceId, initiatingOrgId)
	if err != nil {
		return nil, err
	}
	return withTargets(ctx, results)
}

// GetPoliciesByRoot returns the share policies of one customer tree with their targets.
func GetPoliciesByRoot(ctx context.Context, rootOrgId string) ([]model.Policy, error) {

	policyRows, err := query(ctx, scripts.GetSharePoliciesByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}
	targetRows, err := query(ctx, scripts.GetSharePolicyTargetsByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}
	targets := map[string][]model.Target{}
	for _, row := range targetRows {
		id := rows.String(row, "policy_id")
		targets[id] = append(targets[id], targetOf(row))
	}
	policies := make([]model.Policy, 0, len(policyRows))
	for _, row := range policyRows {
		p := policyOf(row)
		p.Targets = targets[p.PolicyId]
		policies = append(policies, p)
	}
	return policies, nil
}

// GetPoliciesReachingOrg returns the share policies whose targets reach the org.
// Each policy has only the targets that reach the org.
func GetPoliciesReachingOrg(ctx context.Context, orgId string) ([]model.Policy, error) {
	return reachingPolicies(ctx, scripts.GetSharePoliciesReachingOrg, orgId)
}

// GetOrgAccessPoliciesReachingOrg returns the organization access policies whose targets reach
// the org.
func GetOrgAccessPoliciesReachingOrg(ctx context.Context, orgId string) ([]model.Policy, error) {
	return reachingPolicies(ctx, scripts.GetOrgAccessPoliciesReachingOrg, orgId)
}

func reachingPolicies(ctx context.Context, q dbmodel.DBQuery, orgId string) ([]model.Policy, error) {

	results, err := query(ctx, q, orgId)
	if err != nil {
		return nil, err
	}
	var policies []model.Policy
	index := map[string]int{}
	for _, row := range results {
		id := rows.String(row, "policy_id")
		i, seen := index[id]
		if !seen {
			i = len(policies)
			index[id] = i
			policies = append(policies, policyOf(row))
		}
		policies[i].Targets = append(policies[i].Targets, targetOf(row))
	}
	return policies, nil
}

// ReachedOrg is an org that a policy reaches.
type ReachedOrg struct {
	OrgId, OrgHandle string
}

// GetReachedOrgsPage returns one page of the active orgs that the share policy reaches and that the
// organization access policy enables, and the total.
func GetReachedOrgsPage(ctx context.Context, policyId, accessPolicyId string, limit,
	offset int) ([]ReachedOrg, int, error) {

	countRows, err := query(ctx, scripts.CountReachedOrgs, policyId, accessPolicyId)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(countRows) > 0 {
		total = rows.Int(countRows[0], "total")
	}
	results, err := query(ctx, scripts.GetReachedOrgsPage, policyId, accessPolicyId, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	orgs := make([]ReachedOrg, 0, len(results))
	for _, row := range results {
		orgs = append(orgs, ReachedOrg{OrgId: rows.String(row, "org_id"), OrgHandle: rows.String(row, "org_handle")})
	}
	return orgs, total, nil
}

// DeletePolicy deletes the policy with its targets.
func DeletePolicy(ctx context.Context, policyId string) error {

	for _, q := range []dbmodel.DBQuery{scripts.DeleteSharePolicyTargets, scripts.DeleteSharePolicy} {
		if _, err := query(ctx, q, policyId); err != nil {
			return err
		}
	}
	return nil
}

// DeletePoliciesOfResource deletes all policies of the resource.
func DeletePoliciesOfResource(ctx context.Context, resourceType, resourceId string) error {

	results, err := query(ctx, scripts.GetSharePolicyIdsByResource, resourceType, resourceId)
	if err != nil {
		return err
	}
	for _, row := range results {
		if err := DeletePolicy(ctx, rows.String(row, "policy_id")); err != nil {
			return err
		}
	}
	return nil
}

// SharedAttribute is a schema attribute that another org shares, with its owner.
type SharedAttribute struct {
	Attribute      schemaModel.ProfileSchemaAttribute
	OwnerOrgHandle string
}

// ChainAttribute is an attribute of an org of the ancestor chain, with the ID of the owner org.
type ChainAttribute struct {
	SharedAttribute
	OwnerOrgId string
}

// GetAttributesOfChain returns the attributes of the org and of its ancestors.
func GetAttributesOfChain(ctx context.Context, orgId string) ([]ChainAttribute, error) {

	results, err := query(ctx, scripts.GetSchemaAttributesOfChain, orgId)
	if err != nil {
		return nil, err
	}
	attrs := make([]ChainAttribute, 0, len(results))
	for _, row := range results {
		var subAttrs []schemaModel.SubAttribute
		if raw := rows.String(row, "sub_attributes"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &subAttrs)
		}
		var canonical []schemaModel.CanonicalValue
		if raw := rows.String(row, "canonical_values"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &canonical)
		}
		attrs = append(attrs, ChainAttribute{
			OwnerOrgId: rows.String(row, "org_id"),
			SharedAttribute: SharedAttribute{
				OwnerOrgHandle: rows.String(row, "org_handle"),
				Attribute: schemaModel.ProfileSchemaAttribute{
					AttributeId:           rows.String(row, "attribute_id"),
					AttributeName:         rows.String(row, "attribute_name"),
					Scope:                 rows.String(row, "scope"),
					DisplayName:           rows.String(row, "display_name"),
					ValueType:             rows.String(row, "value_type"),
					MergeStrategy:         rows.String(row, "merge_strategy"),
					Mutability:            rows.String(row, "mutability"),
					ApplicationIdentifier: rows.String(row, "application_identifier"),
					MultiValued:           rows.Bool(row, "multi_valued"),
					SubAttributes:         subAttrs,
					CanonicalValues:       canonical,
				},
			},
		})
	}
	return attrs, nil
}

// SharedRule is a unification rule that another org shares. OwnerHops is the number of levels from
// the org up to the owner: a larger value is a farther owner.
type SharedRule struct {
	Rule      ruleModel.UnificationRule
	OwnerHops int
}

// ChainRule is a rule of an org of the ancestor chain, with the ID of the owner org.
type ChainRule struct {
	SharedRule
	OwnerOrgId string
}

// GetRulesOfChain returns the rules of the org and of its ancestors.
func GetRulesOfChain(ctx context.Context, orgId string) ([]ChainRule, error) {

	results, err := query(ctx, scripts.GetUnificationRulesOfChain, orgId)
	if err != nil {
		return nil, err
	}
	result := make([]ChainRule, 0, len(results))
	for _, row := range results {
		result = append(result, ChainRule{
			OwnerOrgId: rows.String(row, "org_id"),
			SharedRule: SharedRule{
				OwnerHops: rows.Int(row, "owner_hops"),
				Rule: ruleModel.UnificationRule{
					RuleId:       rows.String(row, "rule_id"),
					OrgHandle:    rows.String(row, "org_handle"),
					RuleName:     rows.String(row, "rule_name"),
					PropertyName: rows.String(row, "property_name"),
					PropertyId:   rows.String(row, "property_id"),
					Priority:     rows.Int(row, "priority"),
					IsActive:     rows.Bool(row, "is_active"),
					CreatedAt:    rows.Time(row, "created_at"),
					UpdatedAt:    rows.Time(row, "updated_at"),
				},
			},
		})
	}
	return result, nil
}

// TreeAttribute is an attribute of one org of the tree, for the share evaluation.
type TreeAttribute struct {
	Id, Name, Scope, ValueType, OrgHandle, OrgId string
}

// GetAttributesByRoot returns the attributes of all orgs of one customer tree.
func GetAttributesByRoot(ctx context.Context, rootOrgId string) ([]TreeAttribute, error) {

	results, err := query(ctx, scripts.GetSchemaAttributesByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}
	attrs := make([]TreeAttribute, 0, len(results))
	for _, row := range results {
		attrs = append(attrs, TreeAttribute{
			Id:        rows.String(row, "attribute_id"),
			Name:      rows.String(row, "attribute_name"),
			Scope:     rows.String(row, "scope"),
			ValueType: rows.String(row, "value_type"),
			OrgHandle: rows.String(row, "org_handle"),
			OrgId:     rows.String(row, "org_id"),
		})
	}
	return attrs, nil
}

// TreeRule is a rule of one org of the tree, for the share evaluation.
type TreeRule struct {
	Id, PropertyName, PropertyId, OrgHandle, OrgId string
}

// GetRulesByRoot returns the rules of all orgs of one customer tree.
func GetRulesByRoot(ctx context.Context, rootOrgId string) ([]TreeRule, error) {

	results, err := query(ctx, scripts.GetUnificationRulesByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}
	result := make([]TreeRule, 0, len(results))
	for _, row := range results {
		result = append(result, TreeRule{
			Id:           rows.String(row, "rule_id"),
			PropertyName: rows.String(row, "property_name"),
			PropertyId:   rows.String(row, "property_id"),
			OrgHandle:    rows.String(row, "org_handle"),
			OrgId:        rows.String(row, "org_id"),
		})
	}
	return result, nil
}
