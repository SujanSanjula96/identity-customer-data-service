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
	"time"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func serverError(description string, err error) error {
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.ORGANIZATION_STORE.Code,
		Message:     errors2.ORGANIZATION_STORE.Message,
		Description: description,
	}, err)
}

// UpsertOrganization inserts the org, or updates it when it exists.
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
		parent, org.RootOrgId, org.Path, org.Depth, org.Status, time.Now().UTC())
	if err != nil {
		return serverError(fmt.Sprintf("Failed to store the organization: %s", org.OrgId), err)
	}
	return nil
}

// GetOrganizationById returns the org, or nil when CDS does not know it.
func GetOrganizationById(ctx context.Context, orgId string) (*model.Organization, error) {
	return getOne(ctx, scripts.GetOrganizationById, orgId)
}

// GetOrganizationByHandle returns the org, or nil when CDS does not know it.
func GetOrganizationByHandle(ctx context.Context, orgHandle string) (*model.Organization, error) {
	return getOne(ctx, scripts.GetOrganizationByHandle, orgHandle)
}

// GetOrganizationsByRoot returns all orgs of one customer tree, the root first.
func GetOrganizationsByRoot(ctx context.Context, rootOrgId string) ([]model.Organization, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read organizations.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrganizationsByRoot, rootOrgId)
	if err != nil {
		return nil, serverError(fmt.Sprintf("Failed to read the organizations of root: %s", rootOrgId), err)
	}
	orgs := make([]model.Organization, 0, len(results))
	for _, row := range results {
		orgs = append(orgs, mapRow(row))
	}
	return orgs, nil
}

// UpdateOrganizationStatus sets the status of the org.
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

// GetEnabledRootOrgHandles returns the handles of the orgs that enabled CDS themselves.
func GetEnabledRootOrgHandles(ctx context.Context) ([]string, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read configurations.", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetEnabledRootOrgHandles)
	if err != nil {
		return nil, serverError("Failed to read the organizations that enabled CDS.", err)
	}
	handles := make([]string, 0, len(results))
	for _, row := range results {
		handles = append(handles, rows.String(row, "org_handle"))
	}
	return handles, nil
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
		RootOrgId:    rows.String(row, "root_org_id"),
		Path:         rows.String(row, "path"),
		Depth:        rows.Int(row, "depth"),
		Status:       rows.String(row, "status"),
		CreatedAt:    rows.Time(row, "created_at"),
		UpdatedAt:    rows.Time(row, "updated_at"),
		LastSyncedAt: rows.Time(row, "last_synced_at"),
	}
}
