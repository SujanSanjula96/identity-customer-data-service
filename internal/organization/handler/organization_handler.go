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

package handler

import (
	"encoding/json"
	"net/http"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/service"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

const organizationResource = "organization"

// OrganizationHandler serves the CDS view of the org tree.
type OrganizationHandler struct{}

func NewOrganizationHandler() *OrganizationHandler {
	return &OrganizationHandler{}
}

// ListOrganizations handles GET /organizations. It returns the orgs of the tree of the caller.
func (h *OrganizationHandler) ListOrganizations(w http.ResponseWriter, r *http.Request) {

	if err := security.AuthnAndAuthz(r, "admin_config:view"); err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !adminConfigService.GetAdminConfigService().IsCDSEnabled(r.Context(), orgHandle) {
		utils.HandleError(w, notEnabled())
		return
	}
	orgs, err := service.ListTree(r.Context(), orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, orgs, organizationResource)
}

// SyncOrganization handles POST /organizations/sync. The identity provider pushes org lifecycle
// events here with the admin credentials, like the other sync endpoints.
func (h *OrganizationHandler) SyncOrganization(w http.ResponseWriter, r *http.Request) {

	if err := security.AuthnWithAdminCredentials(r); err != nil {
		utils.HandleError(w, err)
		return
	}
	var event model.SyncEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil || event.OrgId == "" || event.Event == "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: "The body must have 'event' and 'org_id'.",
		}, http.StatusBadRequest))
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	log.GetLogger().Info("Received organization event " + event.Event + " for org " + event.OrgId + " through " +
		orgHandle)
	if err := service.HandleSyncEvent(r.Context(), orgHandle, event); err != nil {
		utils.HandleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReconcileOrganizations handles POST /organizations/reconcile. It reads the full org tree of the
// root from the identity provider now.
func (h *OrganizationHandler) ReconcileOrganizations(w http.ResponseWriter, r *http.Request) {

	if err := security.AuthnAndAuthz(r, "admin_config:update"); err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	rootHandle, known := service.RootHandleOf(r.Context(), orgHandle)
	if !known {
		rootHandle = orgHandle
	}
	if !adminConfigService.GetAdminConfigService().IsCDSEnabled(r.Context(), rootHandle) {
		utils.HandleError(w, notEnabled())
		return
	}
	result, err := service.ProvisionTree(r.Context(), rootHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, result, organizationResource)
}

func notEnabled() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.CDS_NOT_ENABLED.Code,
		Message:     errors2.CDS_NOT_ENABLED.Message,
		Description: errors2.CDS_NOT_ENABLED.Description,
	}, http.StatusBadRequest)
}
