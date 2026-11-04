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
	"strconv"

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

	return scanAdminConfigRows(orgHandle, results), nil
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
		_ = tx.Rollback()
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
		_ = tx.Rollback()
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
		_ = tx.Rollback()
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
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update system_applications for organization: %s", orgHandle)

		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	autoMergeEnabledValue := "false"
	if config.AutoMergeEnabled {
		autoMergeEnabledValue = "true"
	}
	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigAutoMergeEnabled, autoMergeEnabledValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update auto_merge_enabled for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigAutoMergeThreshold,
		strconv.FormatFloat(config.AutoMergeThreshold, 'f', -1, 64))
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update auto_merge_threshold for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigManualReviewThreshold,
		strconv.FormatFloat(config.ManualReviewThreshold, 'f', -1, 64))
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update manual_review_threshold for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_ADMIN_CONFIG.Code,
			Message:     errors2.UPDATE_ADMIN_CONFIG.Message,
			Description: errorMsg,
		}, err)
	}

	deterministicDecisiveValue := "false"
	if config.DeterministicMatchDecisive {
		deterministicDecisiveValue = "true"
	}
	_, err = tx.ExecContext(ctx, query, orgHandle, constants.ConfigDeterministicMatchDecisive, deterministicDecisiveValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update deterministic_match_decisive for organization: %s", orgHandle)
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
		_ = tx.Rollback()
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

// scanAdminConfigRows turns the key/value rows held for an org into an AdminConfig.
//
// Settings live as individual rows, so a key an org has never set is simply absent. Each
// default below is therefore the behaviour an org gets by saying nothing, which matters
// most on upgrade: a key introduced by a new feature must default to what the org was
// already doing, not to the zero value.
func scanAdminConfigRows(orgHandle string, results []map[string]interface{}) *model.AdminConfig {

	config := &model.AdminConfig{
		OrgHandle:             orgHandle,
		CDSEnabled:            false,
		InitialSchemaSyncDone: false,
		SystemApplications:    []string{},
		// Enabled unless the org has explicitly turned it off. An org configured before
		// this key existed has no row for it, and reading that absence as "disabled" would
		// silently stop every merge the tenant relied on and route it to review instead.
		AutoMergeEnabled: true,
		// On unless the org has explicitly turned it off. Before typed matching, any
		// deterministic rule matching merged the pair outright; an org that has never seen
		// this key must keep that, and opt into letting other rules object.
		DeterministicMatchDecisive: true,
	}

	if len(results) == 0 {
		return config
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
		case constants.ConfigAutoMergeEnabled:
			config.AutoMergeEnabled = value == "true"
		case constants.ConfigAutoMergeThreshold:
			if v, err := strconv.ParseFloat(value, 64); err == nil && v > 0 && v <= 1 {
				config.AutoMergeThreshold = v
			}
		case constants.ConfigManualReviewThreshold:
			if v, err := strconv.ParseFloat(value, 64); err == nil && v > 0 && v <= 1 {
				config.ManualReviewThreshold = v
			}
		case constants.ConfigDeterministicMatchDecisive:
			config.DeterministicMatchDecisive = value == "true"
		}
	}

	return config
}
