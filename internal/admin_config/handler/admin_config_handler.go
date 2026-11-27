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

// Package handler serves the admin configuration endpoints.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/provider"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
)

// AdminConfigHandler handles GET and PUT operations for admin configurations.
type AdminConfigHandler struct{}

// NewAdminConfigHandler returns a new AdminConfigHandler instance.
func NewAdminConfigHandler() *AdminConfigHandler {
	return &AdminConfigHandler{}
}

// GetAdminConfig handles GET /admin/configs
func (h *AdminConfigHandler) GetAdminConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := security.AuthnAndAuthz(r, "admin_config:view"); err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	adminConfigProvider := provider.NewAdminConfigProvider()
	adminConfigService := adminConfigProvider.GetAdminConfigService()
	config, err := adminConfigService.GetAdminConfig(ctx, org.ID)

	if err != nil {
		utils.HandleError(w, err)
		return
	}

	resp := model.AdminConfigAPI{
		CDSEnabled:                 config.CDSEnabled,
		SystemApplications:         config.SystemApplications,
		AutoMergeEnabled:           config.AutoMergeEnabled,
		AutoMergeThreshold:         config.AutoMergeThreshold,
		ManualReviewThreshold:      config.ManualReviewThreshold,
		DeterministicMatchDecisive: config.DeterministicMatchDecisive,
		CORSAllowedOrigins:         config.CORSAllowedOrigins,
		TemporaryProfileRetentionDays: effectiveSetting(config.TemporaryProfileRetentionDays,
			constants.DefaultTemporaryProfileRetentionDays),
		TemporaryProfileCleanupIntervalDays: effectiveSetting(config.TemporaryProfileCleanupIntervalDays,
			constants.DefaultTemporaryProfileCleanupIntervalDays),
	}
	utils.RespondJSON(w, http.StatusOK, resp, constants.AdminConfigResource)
}

// effectiveSetting returns the stored setting, or its default when none is set.
func effectiveSetting(stored, defaultValue int) int {
	if stored > 0 {
		return stored
	}
	return defaultValue
}

// UpdateAdminConfig handles PATCH /admin/configs
func (h *AdminConfigHandler) UpdateAdminConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := authnAdminConfigUpdate(r); err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	var config model.AdminConfigUpdateAPI

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UpdateConfigBadRequest.Code,
			Message:     errors.UpdateConfigBadRequest.Message,
			Description: utils.HandleDecodeError(err, "admin configuration"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	adminConfigService := provider.NewAdminConfigProvider().GetAdminConfigService()

	existingConfig, err := adminConfigService.GetAdminConfig(ctx, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	configToUpdate := model.AdminConfig{
		OrgID:                         org.ID,
		InitialSchemaSyncDone:         existingConfig.InitialSchemaSyncDone,
		CDSEnabled:                    existingConfig.CDSEnabled,
		SystemApplications:            existingConfig.SystemApplications,
		AutoMergeEnabled:              existingConfig.AutoMergeEnabled,
		AutoMergeThreshold:            existingConfig.AutoMergeThreshold,
		ManualReviewThreshold:         existingConfig.ManualReviewThreshold,
		DeterministicMatchDecisive:    existingConfig.DeterministicMatchDecisive,
		CORSAllowedOrigins:            existingConfig.CORSAllowedOrigins,
		TemporaryProfileRetentionDays: existingConfig.TemporaryProfileRetentionDays,

		TemporaryProfileCleanupIntervalDays: existingConfig.TemporaryProfileCleanupIntervalDays,
	}

	// Update only if provided in request
	if config.CDSEnabled != nil {
		configToUpdate.CDSEnabled = *config.CDSEnabled
	}
	if config.SystemApplications != nil {
		configToUpdate.SystemApplications = config.SystemApplications
	}
	if config.AutoMergeEnabled != nil {
		configToUpdate.AutoMergeEnabled = *config.AutoMergeEnabled
	}
	if config.AutoMergeThreshold != nil {
		configToUpdate.AutoMergeThreshold = *config.AutoMergeThreshold
	}
	if config.ManualReviewThreshold != nil {
		configToUpdate.ManualReviewThreshold = *config.ManualReviewThreshold
	}
	if config.DeterministicMatchDecisive != nil {
		configToUpdate.DeterministicMatchDecisive = *config.DeterministicMatchDecisive
	}
	if config.CORSAllowedOrigins != nil {
		configToUpdate.CORSAllowedOrigins = config.CORSAllowedOrigins
	}
	// Zero is not a value of these settings; the store reads an absent value as "not set".
	for _, setting := range []struct {
		name    string
		given   *int
		target  *int
		minimum int
	}{
		{"temporary_profile_retention_days", config.TemporaryProfileRetentionDays,
			&configToUpdate.TemporaryProfileRetentionDays, constants.MinTemporaryProfileRetentionDays},
		{"temporary_profile_cleanup_interval_days", config.TemporaryProfileCleanupIntervalDays,
			&configToUpdate.TemporaryProfileCleanupIntervalDays, constants.MinTemporaryProfileCleanupIntervalDays},
	} {
		if setting.given == nil {
			continue
		}
		if *setting.given == 0 {
			utils.HandleError(w, errors.NewClientError(errors.ErrorMessage{
				Code:        errors.UpdateConfigBadRequest.Code,
				Message:     errors.UpdateConfigBadRequest.Message,
				Description: fmt.Sprintf("%s must be at least %d.", setting.name, setting.minimum),
			}, http.StatusBadRequest))
			return
		}
		*setting.target = *setting.given
	}

	err = adminConfigService.UpdateAdminConfig(ctx, &configToUpdate, org)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	resp := model.AdminConfigAPI{
		CDSEnabled:                 configToUpdate.CDSEnabled,
		SystemApplications:         configToUpdate.SystemApplications,
		AutoMergeEnabled:           configToUpdate.AutoMergeEnabled,
		AutoMergeThreshold:         configToUpdate.AutoMergeThreshold,
		ManualReviewThreshold:      configToUpdate.ManualReviewThreshold,
		DeterministicMatchDecisive: configToUpdate.DeterministicMatchDecisive,
		CORSAllowedOrigins:         configToUpdate.CORSAllowedOrigins,
		TemporaryProfileRetentionDays: effectiveSetting(configToUpdate.TemporaryProfileRetentionDays,
			constants.DefaultTemporaryProfileRetentionDays),
		TemporaryProfileCleanupIntervalDays: effectiveSetting(configToUpdate.TemporaryProfileCleanupIntervalDays,
			constants.DefaultTemporaryProfileCleanupIntervalDays),
	}
	utils.RespondJSON(w, http.StatusOK, resp, constants.AdminConfigResource)
}

// authnAdminConfigUpdate accepts an OAuth token with the admin_config:update permission, or the admin credentials
// with Basic auth. Identity Server uses the admin credentials to send the tenant CDS enable config
// (PATCH /api/server/v1/configs/cds in Identity Server). The sync listener takes the admin credentials only,
// and AuthnWithAdminCredentials takes them on no other listener when there is a sync listener.
func authnAdminConfigUpdate(r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") || security.OnSyncListener(r) {
		return security.AuthnWithAdminCredentials(r)
	}
	return security.AuthnAndAuthz(r, "admin_config:update")
}
