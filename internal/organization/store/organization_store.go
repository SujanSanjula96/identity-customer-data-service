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
	"time"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
)

func serverError(description string, err error) error {
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.ORGANIZATION_STORE.Code,
		Message:     errors2.ORGANIZATION_STORE.Message,
		Description: description,
	}, err)
}

// UpsertOrganization inserts the organization, or updates it when it exists.
func UpsertOrganization(ctx context.Context, org model.Organization) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return serverError("Failed to get a database client to store an organization.", err)
	}
	defer dbClient.Close()

	var parent interface{}
	if org.ParentOrgId != "" {
		parent = org.ParentOrgId
	}
	_, err = dbClient.ExecuteQueryContext(ctx, scripts.UpsertOrganization, org.OrgId, org.OrgHandle, org.OrgName,
		parent, org.PrimaryOrgId, org.Status, time.Now().UTC())
	if err != nil {
		return serverError(fmt.Sprintf("Failed to store the organization: %s", org.OrgId), err)
	}
	orgkey.Invalidate(org.OrgHandle)
	return nil
}

// GetOrganizationById returns the organization, or nil when it is not stored or organization
// management is disabled.
func GetOrganizationById(ctx context.Context, orgId string) (*model.Organization, error) {
	if !config.OrganizationManagementEnabled() {
		return nil, nil
	}
	return getOne(ctx, scripts.GetOrganizationById, orgId)
}

// GetOrganizationByHandle returns the organization, or nil when it is not stored or organization
// management is disabled.
func GetOrganizationByHandle(ctx context.Context, orgHandle string) (*model.Organization, error) {
	if !config.OrganizationManagementEnabled() {
		return nil, nil
	}
	return getOne(ctx, scripts.GetOrganizationByHandle, orgHandle)
}

// LookupOrganizationByHandle returns the organization, or nil when it is not stored. Unlike
// GetOrganizationByHandle it reads the table when organization management is disabled: the org key
// of every deployment comes from it.
func LookupOrganizationByHandle(ctx context.Context, orgHandle string) (*model.Organization, error) {
	return getOne(ctx, scripts.GetOrganizationByHandle, orgHandle)
}

// GetOrganizationsByPrimaryOrg returns all organizations of an organization tree.
func GetOrganizationsByPrimaryOrg(ctx context.Context, primaryOrgId string) ([]model.Organization, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read organizations.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrganizationsByPrimaryOrg, primaryOrgId)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to read the organizations of primary organization: %s",
			primaryOrgId), err)
	}
	orgs := make([]model.Organization, 0, len(results))
	for _, row := range results {
		orgs = append(orgs, mapRow(row))
	}
	return orgs, nil
}

// idBatchSize is the maximum number of IDs in one IN list.
const idBatchSize = 1000

// GetOrganizationsByIds returns the stored organizations with the IDs.
func GetOrganizationsByIds(ctx context.Context, orgIds []string) ([]model.Organization, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read organizations.", err)
	}
	defer dbClient.Close()

	orgs := make([]model.Organization, 0, len(orgIds))
	for start := 0; start < len(orgIds); start += idBatchSize {
		batch := orgIds[start:min(start+idBatchSize, len(orgIds))]
		placeholders := make([]string, len(batch))
		args := make([]interface{}, len(batch))
		for i, id := range batch {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}
		results, err := dbClient.ExecuteQueryContext(ctx,
			scripts.GetOrganizationsByIds.Format(strings.Join(placeholders, ", ")), args...)
		if err != nil {
			return nil, serverError("Failed to read organizations by ID.", err)
		}
		for _, row := range results {
			orgs = append(orgs, mapRow(row))
		}
	}
	return orgs, nil
}

// GetOrganizationChain returns the organization and its ancestors, the primary organization first.
// The chain is empty when organization management is disabled.
func GetOrganizationChain(ctx context.Context, orgId string) ([]model.Organization, error) {

	if !config.OrganizationManagementEnabled() {
		return nil, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read organizations.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrganizationChain, orgId)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to read the ancestors of organization: %s", orgId), err)
	}
	orgs := make([]model.Organization, 0, len(results))
	for _, row := range results {
		orgs = append(orgs, mapRow(row))
	}
	return orgs, nil
}

// GetAncestors returns the ancestors of the organization, the parent first.
func GetAncestors(ctx context.Context, orgId string) ([]model.Ancestor, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read organizations.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrganizationAncestors, orgId)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to read the ancestors of organization: %s", orgId), err)
	}
	ancestors := make([]model.Ancestor, 0, len(results))
	for _, row := range results {
		ancestors = append(ancestors, model.Ancestor{Organization: mapRow(row), Depth: rows.Int(row, "hops")})
	}
	return ancestors, nil
}

// UpdateOrganizationStatus sets the status of the organization.
func UpdateOrganizationStatus(ctx context.Context, orgId, status string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return serverError("Failed to get a database client to update an organization.", err)
	}
	defer dbClient.Close()

	if _, err := dbClient.ExecuteQueryContext(ctx, scripts.UpdateOrganizationStatus, status, time.Now().UTC(),
		orgId); err != nil {
		return serverError(fmt.Sprintf("Failed to update the status of organization: %s", orgId), err)
	}
	return nil
}

// GetTreeSyncStatus returns the tree sync state of the primary organization.
func GetTreeSyncStatus(ctx context.Context, primaryHandle string) (*model.TreeSyncStatus, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read the organization sync state.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrgConfigurations, primaryHandle)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to read the organization sync state of: %s", primaryHandle), err)
	}
	status := &model.TreeSyncStatus{State: model.TreeSyncNotSynced}
	for _, row := range results {
		value := rows.String(row, "value")
		switch rows.String(row, "config") {
		case model.ConfigTreeSyncState:
			if value != "" {
				status.State = value
			}
		case model.ConfigTreeSyncStartedAt:
			status.StartedAt = parseTime(value)
		case model.ConfigTreeSyncFinishedAt:
			status.FinishedAt = parseTime(value)
		case model.ConfigTreeSyncedAt:
			status.LastSyncedAt = parseTime(value)
		case model.ConfigTreeSyncError:
			status.Error = value
		}
	}
	return status, nil
}

// SetTreeSyncValues writes the tree sync keys of the primary organization in one transaction. An
// empty value clears the key.
func SetTreeSyncValues(ctx context.Context, primaryHandle string, values map[string]string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return serverError("Failed to get a database client to store the organization sync state.", err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return serverError(fmt.Sprintf("Failed to store the organization sync state of: %s", primaryHandle), err)
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, scripts.UpdateOrgConfiguration, primaryHandle, key, value); err != nil {
			_ = tx.Rollback()
			return serverError(fmt.Sprintf("Failed to store the organization sync state of: %s", primaryHandle), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return serverError(fmt.Sprintf("Failed to store the organization sync state of: %s", primaryHandle), err)
	}
	return nil
}

func parseTime(value string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &t
}

func getOne(ctx context.Context, query dbmodel.DBQuery, arg string) (*model.Organization, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read an organization.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, query, arg)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to read the organization: %s", arg), err)
	}
	if len(results) == 0 {
		return nil, nil
	}
	org := mapRow(results[0])
	return &org, nil
}

func mapRow(row map[string]interface{}) model.Organization {
	return model.Organization{
		OrgId:        rows.String(row, "org_id"),
		OrgHandle:    rows.String(row, "org_handle"),
		OrgName:      rows.String(row, "org_name"),
		ParentOrgId:  rows.String(row, "parent_org_id"),
		PrimaryOrgId: rows.String(row, "primary_org_id"),
		Status:       rows.String(row, "status"),
		CreatedAt:    rows.Time(row, "created_at"),
		UpdatedAt:    rows.Time(row, "updated_at"),
	}
}
