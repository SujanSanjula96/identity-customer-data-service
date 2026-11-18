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

// Package store persists the admin configuration of an organization as one row per setting.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// storedTrue is how a true boolean configuration value is stored.
const storedTrue = "true"

// GetAdminConfig reads the configuration of the organization. A setting that the organization has not
// stored takes its default.
func GetAdminConfig(ctx context.Context, orgID string) (*model.AdminConfig, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching configurations for the organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAdminConfig.Code,
			Message:     errors2.GetAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetOrgConfigurations
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute query for fetching configurations for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAdminConfig.Code,
			Message:     errors2.GetAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	return scanAdminConfigRows(results), nil
}

// GetCORSAllowedOrigins returns the CORS allowed origins the organization has configured, or nil
// when it has configured none. It reads the one key, which the (org_id, config) key serves.
func GetCORSAllowedOrigins(ctx context.Context, orgID string) ([]string, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get db client for fetching the CORS allowed origins of organization: %s",
			orgID,
		)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAdminConfig.Code,
			Message:     errors2.GetAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrgConfiguration, orgID,
		constants.ConfigCORSAllowedOrigins)
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to execute query for fetching the CORS allowed origins of organization: %s",
			orgID,
		)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAdminConfig.Code,
			Message:     errors2.GetAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		return nil, nil
	}
	value, ok := results[0]["value"].(string)
	if !ok {
		return nil, nil
	}
	var origins []string
	if err := json.Unmarshal([]byte(value), &origins); err != nil || len(origins) == 0 {
		return nil, nil
	}
	return origins, nil
}

// UpdateAdminConfig updates organization-level admin configuration (e.g., CDS enablement, schema sync flags).
func UpdateAdminConfig(ctx context.Context, config model.AdminConfig, orgID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get DB client for updating configurations for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating configurations for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	query := scripts.UpdateOrgConfiguration

	cdsEnabledValue := strconv.FormatBool(config.CDSEnabled)
	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigCDSEnabled, cdsEnabledValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update cds_enabled for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	schemaSyncValue := strconv.FormatBool(config.InitialSchemaSyncDone)
	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigInitialSchemaSyncDone, schemaSyncValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update initial_schema_sync_done for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	systemAppsValue, err := json.Marshal(config.SystemApplications)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to marshal system_applications for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigSystemApplications, string(systemAppsValue))
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update system_applications for organization: %s", orgID)

		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	autoMergeEnabledValue := strconv.FormatBool(config.AutoMergeEnabled)
	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigAutoMergeEnabled, autoMergeEnabledValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update auto_merge_enabled for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigAutoMergeThreshold,
		strconv.FormatFloat(config.AutoMergeThreshold, 'f', -1, 64))
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update auto_merge_threshold for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigManualReviewThreshold,
		strconv.FormatFloat(config.ManualReviewThreshold, 'f', -1, 64))
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update manual_review_threshold for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	deterministicDecisiveValue := strconv.FormatBool(config.DeterministicMatchDecisive)
	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigDeterministicMatchDecisive, deterministicDecisiveValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update deterministic_match_decisive for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	corsOriginsValue, err := json.Marshal(config.CORSAllowedOrigins)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to marshal cors_allowed_origins for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	_, err = tx.ExecContext(ctx, query, orgID, constants.ConfigCORSAllowedOrigins, string(corsOriginsValue))
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to update cors_allowed_origins for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	return tx.Commit()
}

// UpdateInitialSchemaSyncConfig updates organization-level admin configuration (e.g., CDS enablement, schema sync
// flags).
func UpdateInitialSchemaSyncConfig(ctx context.Context, state bool, orgID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get DB client for updating configurations for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating configurations for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}

	stateValue := strconv.FormatBool(state)

	query := scripts.UpdateInitialSchemaSyncDoneConfig
	_, err = tx.ExecContext(ctx, query, orgID, stateValue)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to execute update for configurations for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAdminConfig.Code,
			Message:     errors2.UpdateAdminConfig.Message,
			Description: errorMsg,
		}, err)
	}
	return tx.Commit()
}

// scanAdminConfigRows turns the key/value rows held for an org into an AdminConfig.
//
// Settings live as individual rows, so a key an org has never set is simply absent. Each
// default below is therefore the behavior an org gets by saying nothing, which matters
// most on upgrade: a key introduced by a new feature must default to what the org was
// already doing, not to the zero value.
func scanAdminConfigRows(results []map[string]interface{}) *model.AdminConfig {
	config := &model.AdminConfig{
		CDSEnabled:            false,
		InitialSchemaSyncDone: false,
		SystemApplications:    []string{},
		CORSAllowedOrigins:    []string{},
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
			config.CDSEnabled = value == storedTrue
		case constants.ConfigInitialSchemaSyncDone:
			config.InitialSchemaSyncDone = value == storedTrue
		case constants.ConfigSystemApplications:
			var apps []string
			if err := json.Unmarshal([]byte(value), &apps); err == nil {
				config.SystemApplications = apps
			}
		case constants.ConfigCORSAllowedOrigins:
			var origins []string
			if err := json.Unmarshal([]byte(value), &origins); err == nil {
				config.CORSAllowedOrigins = origins
			}
		case constants.ConfigAutoMergeEnabled:
			config.AutoMergeEnabled = value == storedTrue
		case constants.ConfigAutoMergeThreshold:
			if v, err := strconv.ParseFloat(value, 64); err == nil && v > 0 && v <= 1 {
				config.AutoMergeThreshold = v
			}
		case constants.ConfigManualReviewThreshold:
			if v, err := strconv.ParseFloat(value, 64); err == nil && v > 0 && v <= 1 {
				config.ManualReviewThreshold = v
			}
		case constants.ConfigDeterministicMatchDecisive:
			config.DeterministicMatchDecisive = value == storedTrue
		}
	}

	return config
}
