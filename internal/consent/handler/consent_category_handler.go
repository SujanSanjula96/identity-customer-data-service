/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

// Package handler serves the consent category endpoints.
package handler

import (
	"context"
	"encoding/json"
	"net/http"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	"github.com/wso2/identity-customer-data-service/internal/consent/provider"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// ConsentCategoryHandler handles the consent category endpoints.
type ConsentCategoryHandler struct{}

// NewConsentCategoryHandler returns a new ConsentCategoryHandler instance.
func NewConsentCategoryHandler() *ConsentCategoryHandler {
	return &ConsentCategoryHandler{}
}

// GetAllConsentCategories handles GET /consent-categories
func (h *ConsentCategoryHandler) GetAllConsentCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "consent_category:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}
	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	categories, err := service.GetAllConsentCategories(ctx, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	responses := make([]consentModel.ConsentCategoryResponse, 0, len(categories))
	for _, c := range categories {
		responses = append(responses, c.ToResponse())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responses)
}

// AddConsentCategory handles POST /consent-categories
func (h *ConsentCategoryHandler) AddConsentCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "consent_category:create")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}

	var req consentModel.ConsentCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.AddConsentCategoryBadRequest.Code,
			Message:     errors.AddConsentCategoryBadRequest.Message,
			Description: utils.HandleDecodeError(err, "consent category"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	category := req.ToCategory(org.ID, "")

	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	consentCat, err := service.AddConsentCategory(ctx, org.ID, category)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	audit.SetTargetID(ctx, consentCat.CategoryIdentifier)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(consentCat.ToResponse())
}

// GetConsentCategory handles GET /consent-categories/{id}
func (h *ConsentCategoryHandler) GetConsentCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "consent_category:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	categoryID := r.PathValue("categoryId")
	if categoryID == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.ConsentCatID.Code,
			Message:     errors.ConsentCatID.Message,
			Description: "Category Id is required to fetch the consent category",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}
	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	category, err := service.GetConsentCategory(ctx, categoryID, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(category.ToResponse())
}

// UpdateConsentCategory handles PUT /consent-categories/{id}
func (h *ConsentCategoryHandler) UpdateConsentCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "consent_category:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	categoryID := r.PathValue("categoryId")
	if categoryID == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.ConsentCatID.Code,
			Message:     errors.ConsentCatID.Message,
			Description: "Category Id is required to fetch the consent category",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}

	var req consentModel.ConsentCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UpdateConsentCategoryBadRequest.Code,
			Message:     errors.UpdateConsentCategoryBadRequest.Message,
			Description: utils.HandleDecodeError(err, "consent category"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	category := req.ToCategory(org.ID, categoryID)

	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	if err := service.UpdateConsentCategory(ctx, org.ID, category); err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(category.ToResponse())
}

// DeleteConsentCategory handles Delete /consent-categories/{id}
func (h *ConsentCategoryHandler) DeleteConsentCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "consent_category:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	categoryID := r.PathValue("categoryId")
	if categoryID == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.ConsentCatID.Code,
			Message:     errors.ConsentCatID.Message,
			Description: "Category Id is required to fetch the consent category",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}
	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	if err := service.DeleteConsentCategory(ctx, categoryID, org.ID); err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// isCDSEnabled reports whether CDS is enabled for the organization. A sub organization is enabled only when
// the organization access of its primary organization includes it.
func isCDSEnabled(ctx context.Context, orgID string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(ctx, orgID)
}

func cdsNotEnabled() error {
	return errors.NewClientError(errors.ErrorMessage{
		Code:        errors.CDSNotEnabled.Code,
		Message:     errors.CDSNotEnabled.Message,
		Description: errors.CDSNotEnabled.Description,
	}, http.StatusBadRequest)
}
