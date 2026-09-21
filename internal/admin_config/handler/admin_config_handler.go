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

package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/provider"
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
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	adminConfigProvider := provider.NewAdminConfigProvider()
	adminConfigService := adminConfigProvider.GetAdminConfigService()
	config, err := adminConfigService.GetAdminConfig(ctx, orgHandle)

	if err != nil {
		utils.HandleError(w, err)
		return
	}

	resp := model.AdminConfigAPI{
		CDSEnabled:         config.CDSEnabled,
		SystemApplications: config.SystemApplications,
	}
	utils.RespondJSON(w, http.StatusOK, resp, constants.AdminConfigResource)
}

// UpdateAdminConfig handles PATCH /admin/configs
func (h *AdminConfigHandler) UpdateAdminConfig(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	if err := security.AuthnAndAuthz(r, "admin_config:update"); err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	var config model.AdminConfigUpdateAPI

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UPDATE_CONFIG_BAD_REQUEST.Code,
			Message:     errors.UPDATE_CONFIG_BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "admin configuration"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	adminConfigService := provider.NewAdminConfigProvider().GetAdminConfigService()

	existingConfig, err := adminConfigService.GetAdminConfig(ctx, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	configToUpdate := model.AdminConfig{
		OrgHandle:             orgHandle,
		InitialSchemaSyncDone: existingConfig.InitialSchemaSyncDone,
		CDSEnabled:            existingConfig.CDSEnabled,
		SystemApplications:    existingConfig.SystemApplications,
	}

	// Update only if provided in request
	if config.CDSEnabled != nil {
		configToUpdate.CDSEnabled = *config.CDSEnabled
	}
	if config.SystemApplications != nil {
		configToUpdate.SystemApplications = config.SystemApplications
	}

	err = adminConfigService.UpdateAdminConfig(ctx, configToUpdate, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	resp := model.AdminConfigAPI{
		CDSEnabled:         configToUpdate.CDSEnabled,
		SystemApplications: configToUpdate.SystemApplications,
	}
	utils.RespondJSON(w, http.StatusOK, resp, constants.AdminConfigResource)
}
