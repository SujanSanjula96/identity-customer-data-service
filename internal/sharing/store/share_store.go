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
	"fmt"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
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

// CreatePolicy stores a new policy with its targets.
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

// targetBatchSize is the maximum number of targets in one insert statement.
const targetBatchSize = 1000

func insertTargets(ctx context.Context, tx *dbmodel.Tx, policyId string, targets []model.Target) error {

	for start := 0; start < len(targets); start += targetBatchSize {
		batch := targets[start:min(start+targetBatchSize, len(targets))]
		values := make([]string, len(batch))
		args := make([]interface{}, 0, 3*len(batch))
		for i, target := range batch {
			values[i] = fmt.Sprintf("($%d, $%d, $%d)", 3*i+1, 3*i+2, 3*i+3)
			args = append(args, policyId, target.Scope, target.OrgId)
		}
		query := scripts.InsertSharePolicyTargets.Format(strings.Join(values, ", "))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return serverError("Failed to store the share policy targets.", err)
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

// GetPolicy returns the policy of the initiating organization for the resource, with its targets,
// or nil.
func GetPolicy(ctx context.Context, resourceType, resourceId, initiatingOrgId string) (*model.Policy, error) {

	results, err := query(ctx, scripts.GetSharePolicy, resourceType, resourceId, initiatingOrgId)
	if err != nil {
		return nil, err
	}
	return withTargets(ctx, results)
}

// GetPoliciesByOwners returns the policies of the resource type that the organizations own, with
// their targets.
func GetPoliciesByOwners(ctx context.Context, resourceType string, ownerOrgIds []string) ([]model.Policy, error) {

	if len(ownerOrgIds) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ownerOrgIds))
	args := make([]interface{}, 0, len(ownerOrgIds)+1)
	args = append(args, resourceType)
	for i, id := range ownerOrgIds {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}
	results, err := query(ctx, scripts.GetSharePoliciesByOwners.Format(strings.Join(placeholders, ", ")), args...)
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

// DeletePolicy deletes the policy with its targets.
func DeletePolicy(ctx context.Context, policyId string) error {

	return inTx(ctx, "delete a share policy", func(tx *dbmodel.Tx) error {
		return deletePolicy(ctx, tx, policyId)
	})
}

// DeletePoliciesOfResource deletes all policies of the resource.
func DeletePoliciesOfResource(ctx context.Context, resourceType, resourceId string) error {

	results, err := query(ctx, scripts.GetSharePolicyIdsByResource, resourceType, resourceId)
	if err != nil || len(results) == 0 {
		return err
	}
	return inTx(ctx, "delete the share policies of a resource", func(tx *dbmodel.Tx) error {
		for _, row := range results {
			if err := deletePolicy(ctx, tx, rows.String(row, "policy_id")); err != nil {
				return err
			}
		}
		return nil
	})
}

func deletePolicy(ctx context.Context, tx *dbmodel.Tx, policyId string) error {

	for _, q := range []dbmodel.DBQuery{scripts.DeleteSharePolicyTargets, scripts.DeleteSharePolicy} {
		if _, err := tx.ExecContext(ctx, q, policyId); err != nil {
			return serverError(fmt.Sprintf("Failed to run the sharing statement %s.", q.ID), err)
		}
	}
	return nil
}
