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
	shareHandler "github.com/wso2/identity-customer-data-service/internal/sharing/handler"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

const (
	orgAccessResource = "organization access"
	treeSyncResource  = "organization sync"
)

// OrganizationHandler handles the organization endpoints.
type OrganizationHandler struct{}

func NewOrganizationHandler() *OrganizationHandler {
	return &OrganizationHandler{}
}

// SyncOrganization applies an organization lifecycle event.
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

// StartTreeSync starts a background sync of the organization tree.
func (h *OrganizationHandler) StartTreeSync(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:update")
	if !ok {
		return
	}
	status, err := service.StartTreeSync(r.Context(), primary.OrgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusAccepted, status, treeSyncResource)
}

// GetTreeSync returns the state of the organization tree sync.
func (h *OrganizationHandler) GetTreeSync(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:view")
	if !ok {
		return
	}
	status, err := service.GetTreeSyncStatus(r.Context(), primary.OrgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, status, treeSyncResource)
}

// Organization access endpoints.

func (h *OrganizationHandler) CreateOrgAccess(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:update")
	if !ok {
		return
	}
	req, ok := shareHandler.DecodePolicyRequest(w, r)
	if !ok {
		return
	}
	resp, err := sharingService.CreateOrgAccess(r.Context(), *primary, req)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusCreated, resp, orgAccessResource)
}

func (h *OrganizationHandler) ListOrgAccess(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:view")
	if !ok {
		return
	}
	resp, err := sharingService.ListOrgAccess(r.Context(), *primary)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, orgAccessResource)
}

func (h *OrganizationHandler) GetOrgAccess(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:view")
	if !ok {
		return
	}
	resp, err := sharingService.GetOrgAccess(r.Context(), *primary, r.PathValue("orgPolicyId"))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, orgAccessResource)
}

func (h *OrganizationHandler) UpdateOrgAccess(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:update")
	if !ok {
		return
	}
	req, ok := shareHandler.DecodePolicyRequest(w, r)
	if !ok {
		return
	}
	resp, err := sharingService.UpdateOrgAccess(r.Context(), *primary, r.PathValue("orgPolicyId"), req)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, orgAccessResource)
}

func (h *OrganizationHandler) DeleteOrgAccess(w http.ResponseWriter, r *http.Request) {

	primary, ok := primaryOf(w, r, "organization:update")
	if !ok {
		return
	}
	if err := sharingService.DeleteOrgAccess(r.Context(), *primary, r.PathValue("orgPolicyId")); err != nil {
		utils.HandleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// primaryOf authenticates the request and returns the primary organization of the path. It writes
// an error response when the organization is not a primary organization with CDS enabled.
func primaryOf(w http.ResponseWriter, r *http.Request, operation string) (*model.Organization, bool) {

	if err := security.AuthnAndAuthzPrimary(r, operation); err != nil {
		utils.HandleError(w, err)
		return nil, false
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !adminConfigService.GetAdminConfigService().IsCDSEnabled(r.Context(), orgHandle) {
		utils.HandleError(w, notEnabled())
		return nil, false
	}
	org, err := service.GetOrStoreOrganization(r.Context(), orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return nil, false
	}
	if org == nil || !org.IsPrimary() {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.SUB_ORG_CONFIG_NOT_ALLOWED.Code,
			Message:     errors2.SUB_ORG_CONFIG_NOT_ALLOWED.Message,
			Description: "Only the primary organization manages the organization sync and the organization access.",
		}, http.StatusBadRequest))
		return nil, false
	}
	return org, true
}

func notEnabled() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.CDS_NOT_ENABLED.Code,
		Message:     errors2.CDS_NOT_ENABLED.Message,
		Description: errors2.CDS_NOT_ENABLED.Description,
	}, http.StatusBadRequest)
}
