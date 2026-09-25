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
	"time"

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

// SavePolicy creates the policy, or replaces the targets and exclusions of the existing policy
// of the same resource and initiating org. It returns the stored policy ID.
func SavePolicy(ctx context.Context, p model.Policy) (string, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return "", serverError("Failed to get a database client for sharing.", err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return "", serverError("Failed to start a transaction for a share policy.", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	rowsFound, err := tx.QueryContext(ctx, scripts.GetSharePolicy, p.ResourceType, p.ResourceId, p.InitiatingOrgId)
	if err != nil {
		return "", serverError("Failed to read the share policy.", err)
	}
	existingId := ""
	if rowsFound.Next() {
		var id, resourceType, resourceId, owner, initiator, stage string
		var version int
		var created, updated interface{}
		if err := rowsFound.Scan(&id, &resourceType, &resourceId, &owner, &initiator, &stage, &version, &created,
			&updated); err != nil {
			_ = rowsFound.Close()
			return "", serverError("Failed to read the share policy.", err)
		}
		existingId = id
	}
	_ = rowsFound.Close()

	policyId := existingId
	if existingId == "" {
		policyId = p.PolicyId
		if _, err := tx.ExecContext(ctx, scripts.InsertSharePolicy, policyId, p.ResourceType, p.ResourceId,
			p.OwnerOrgId, p.InitiatingOrgId, p.Stage, now); err != nil {
			return "", serverError("Failed to store the share policy.", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, scripts.TouchSharePolicy, now, policyId); err != nil {
			return "", serverError("Failed to update the share policy.", err)
		}
		if _, err := tx.ExecContext(ctx, scripts.DeleteSharePolicyTargets, policyId); err != nil {
			return "", serverError("Failed to replace the share policy targets.", err)
		}
		if _, err := tx.ExecContext(ctx, scripts.DeleteSharePolicyExclusions, policyId); err != nil {
			return "", serverError("Failed to replace the share policy exclusions.", err)
		}
	}
	for _, target := range p.Targets {
		if _, err := tx.ExecContext(ctx, scripts.InsertSharePolicyTarget, policyId, target.Scope,
			target.OrgId); err != nil {
			return "", serverError("Failed to store a share policy target.", err)
		}
	}
	for _, excluded := range p.ExcludedOrgIds {
		if _, err := tx.ExecContext(ctx, scripts.InsertSharePolicyExclusion, policyId, excluded); err != nil {
			return "", serverError("Failed to store a share policy exclusion.", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", serverError("Failed to commit the share policy.", err)
	}
	committed = true
	return policyId, nil
}

// GetPoliciesByRoot returns the policies of one customer tree with their targets and
// exclusions, oldest first.
func GetPoliciesByRoot(ctx context.Context, rootOrgId string) ([]model.Policy, error) {

	policyRows, err := query(ctx, scripts.GetSharePoliciesByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}
	targetRows, err := query(ctx, scripts.GetSharePolicyTargetsByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}
	exclusionRows, err := query(ctx, scripts.GetSharePolicyExclusionsByRoot, rootOrgId)
	if err != nil {
		return nil, err
	}

	targets := map[string][]model.Target{}
	for _, row := range targetRows {
		id := rows.String(row, "policy_id")
		targets[id] = append(targets[id], model.Target{Scope: rows.String(row, "target_scope"),
			OrgId: rows.String(row, "target_org_id")})
	}
	exclusions := map[string][]string{}
	for _, row := range exclusionRows {
		id := rows.String(row, "policy_id")
		exclusions[id] = append(exclusions[id], rows.String(row, "excluded_org_id"))
	}

	policies := make([]model.Policy, 0, len(policyRows))
	for _, row := range policyRows {
		id := rows.String(row, "policy_id")
		policies = append(policies, model.Policy{
			PolicyId:        id,
			ResourceType:    rows.String(row, "resource_type"),
			ResourceId:      rows.String(row, "resource_id"),
			OwnerOrgId:      rows.String(row, "owner_org_id"),
			InitiatingOrgId: rows.String(row, "initiating_org_id"),
			Stage:           rows.String(row, "stage"),
			Version:         rows.Int(row, "version"),
			Targets:         targets[id],
			ExcludedOrgIds:  exclusions[id],
			CreatedAt:       rows.Time(row, "created_at"),
			UpdatedAt:       rows.Time(row, "updated_at"),
		})
	}
	return policies, nil
}

// DeletePolicy deletes the policy with its targets and exclusions.
func DeletePolicy(ctx context.Context, policyId string) error {

	for _, q := range []dbmodel.DBQuery{scripts.DeleteSharePolicyTargets, scripts.DeleteSharePolicyExclusions,
		scripts.DeleteSharePolicy} {
		if _, err := query(ctx, q, policyId); err != nil {
			return err
		}
	}
	return nil
}

// DeletePoliciesOfResource deletes all policies of the resource, and its states.
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
	_, err = query(ctx, scripts.DeleteShareStatesByResource, resourceType, resourceId)
	return err
}

// ReplaceStatesOfRoot replaces all share states of one customer tree in one transaction.
func ReplaceStatesOfRoot(ctx context.Context, rootOrgId string, states []model.State) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return serverError("Failed to get a database client for sharing.", err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return serverError("Failed to start a transaction for share states.", err)
	}
	if _, err := tx.ExecContext(ctx, scripts.DeleteShareStatesByRoot, rootOrgId); err != nil {
		_ = tx.Rollback()
		return serverError("Failed to clear the share states.", err)
	}
	now := time.Now().UTC()
	for _, s := range states {
		if _, err := tx.ExecContext(ctx, scripts.InsertShareState, s.ResourceType, s.ResourceId, s.OrgId, rootOrgId,
			s.State, s.Reason, s.ConflictingResourceId, now); err != nil {
			_ = tx.Rollback()
			return serverError("Failed to store a share state.", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return serverError("Failed to commit the share states.", err)
	}
	return nil
}

// GetStatesOfResource returns the state of the resource in each org that a policy reaches.
func GetStatesOfResource(ctx context.Context, resourceType, resourceId string) ([]model.State, error) {

	results, err := query(ctx, scripts.GetShareStatesByResource, resourceType, resourceId)
	if err != nil {
		return nil, err
	}
	states := make([]model.State, 0, len(results))
	for _, row := range results {
		states = append(states, model.State{
			ResourceType:          resourceType,
			ResourceId:            resourceId,
			OrgId:                 rows.String(row, "org_id"),
			OrgHandle:             rows.String(row, "org_handle"),
			State:                 rows.String(row, "state"),
			Reason:                rows.String(row, "reason"),
			ConflictingResourceId: rows.String(row, "conflicting_resource_id"),
		})
	}
	return states, nil
}

// GetStatesForOrg returns the states of all shared resources of one type in one org, by
// resource ID.
func GetStatesForOrg(ctx context.Context, resourceType, orgId string) (map[string]model.State, error) {

	results, err := query(ctx, scripts.GetShareStatesForOrg, resourceType, orgId)
	if err != nil {
		return nil, err
	}
	states := map[string]model.State{}
	for _, row := range results {
		id := rows.String(row, "resource_id")
		states[id] = model.State{
			ResourceType:          resourceType,
			ResourceId:            id,
			OrgId:                 orgId,
			State:                 rows.String(row, "state"),
			Reason:                rows.String(row, "reason"),
			ConflictingResourceId: rows.String(row, "conflicting_resource_id"),
		}
	}
	return states, nil
}

// SharedAttribute is a schema attribute that another org shares, with its owner.
type SharedAttribute struct {
	Attribute      schemaModel.ProfileSchemaAttribute
	OwnerOrgHandle string
}

// GetActiveSharedAttributes returns the shared attributes that are active in the org.
func GetActiveSharedAttributes(ctx context.Context, orgId string) ([]SharedAttribute, error) {

	results, err := query(ctx, scripts.GetSharedSchemaAttributesForOrg, orgId)
	if err != nil {
		return nil, err
	}
	attrs := make([]SharedAttribute, 0, len(results))
	for _, row := range results {
		var subAttrs []schemaModel.SubAttribute
		if raw := rows.String(row, "sub_attributes"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &subAttrs)
		}
		var canonical []schemaModel.CanonicalValue
		if raw := rows.String(row, "canonical_values"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &canonical)
		}
		attrs = append(attrs, SharedAttribute{
			OwnerOrgHandle: rows.String(row, "owner_org_handle"),
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
		})
	}
	return attrs, nil
}

// SharedRule is a unification rule that another org shares, with the depth of its owner.
type SharedRule struct {
	Rule       ruleModel.UnificationRule
	OwnerDepth int
}

// GetActiveSharedRules returns the shared rules that are active in the org.
func GetActiveSharedRules(ctx context.Context, orgId string) ([]SharedRule, error) {

	results, err := query(ctx, scripts.GetSharedUnificationRulesForOrg, orgId)
	if err != nil {
		return nil, err
	}
	result := make([]SharedRule, 0, len(results))
	for _, row := range results {
		result = append(result, SharedRule{
			OwnerDepth: rows.Int(row, "owner_depth"),
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
