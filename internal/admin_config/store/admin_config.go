/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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
	model "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func GetAdminConfig(ctx context.Context, orgHandle string) (*model.AdminConfig, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching configurations for the organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_ADMIN_CONFIG.Code,
			Message:     errors2.GET_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetOrgConfigurations
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute query for fetching configurations for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_ADMIN_CONFIG.Code,
			Message:     errors2.GET_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	config := &model.AdminConfig{
		OrgHandle:             orgHandle,
		CDSEnabled:            false,
		InitialSchemaSyncDone: false,
		SystemApplications:    []string{},
	}

	if len(results) == 0 {
		logger.Warn(fmt.Sprintf("No configurations found for organization: %s", orgHandle))
		return config, nil
	}

	for _, row := range results {
		configKey, ok := row["config"].(string)
		if !ok {
			continue
		}
		value, ok := row["value"].(string)
		if !ok {
			continue
		}

		switch configKey {
		case constants.ConfigCDSEnabled:
			config.CDSEnabled = value == "true"
		case constants.ConfigInitialSchemaSyncDone:
			config.InitialSchemaSyncDone = value == "true"
		case constants.ConfigSystemApplications:
			var apps []string
			if err := json.Unmarshal([]byte(value), &apps); err == nil {
				config.SystemApplications = apps
			}
		}
	}

	return config, nil
}

// UpdateAdminConfig updates organization-level admin configuration (e.g., CDS enablement, schema sync flags).
func UpdateAdminConfig(ctx context.Context, config model.AdminConfig, orgHandle string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get DB client for updating configurations for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating configurations for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	query := scripts.UpdateOrgConfiguration

	cdsEnabledValue := "false"
	if config.CDSEnabled {
		cdsEnabledValue = "true"
	}
	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigCDSEnabled, cdsEnabledValue)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to update cds_enabled for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	schemaSyncValue := "false"
	if config.InitialSchemaSyncDone {
		schemaSyncValue = "true"
	}
	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigInitialSchemaSyncDone, schemaSyncValue)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to update initial_schema_sync_done for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	systemAppsValue, err := json.Marshal(config.SystemApplications)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to marshal system_applications for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}
	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigSystemApplications, string(systemAppsValue))
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to update system_applications for organization: %s", orgHandle)

		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	return tx.Commit()
}

// UpdateInitialSchemaSyncConfig updates organization-level admin configuration (e.g., CDS enablement, schema sync flags).
func UpdateInitialSchemaSyncConfig(ctx context.Context, state bool, orgHandle string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get DB client for updating configurations for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating configurations for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	stateValue := "false"
	if state {
		stateValue = "true"
	}

	query := scripts.UpdateInitialSchemaSyncDoneConfig
	_, err = tx.ExecContext(ctx, query, orgHandle, stateValue)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute update for configurations for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}
	return tx.Commit()
}
