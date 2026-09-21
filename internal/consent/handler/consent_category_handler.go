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

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	"github.com/wso2/identity-customer-data-service/internal/consent/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

type ConsentCategoryHandler struct{}

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
	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	categories, err := service.GetAllConsentCategories(ctx)
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

	var req consentModel.ConsentCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.ADD_CONSENT_CATEGORY_BAD_REQUEST.Code,
			Message:     errors.ADD_CONSENT_CATEGORY_BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "consent category"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	category := req.ToCategory(orgHandle, "")

	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	consentCat, err := service.AddConsentCategory(ctx, category)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

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

	categoryId := r.PathValue("categoryId")
	if categoryId == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.CONSENT_CAT_ID.Code,
			Message:     errors.CONSENT_CAT_ID.Message,
			Description: "Category Id is required to fetch the consent category",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	category, err := service.GetConsentCategory(ctx, categoryId)
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

	categoryId := r.PathValue("categoryId")
	if categoryId == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.CONSENT_CAT_ID.Code,
			Message:     errors.CONSENT_CAT_ID.Message,
			Description: "Category Id is required to fetch the consent category",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	var req consentModel.ConsentCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.UPDATE_CONSENT_CATEGORY_BAD_REQUEST.Code,
			Message:     errors.UPDATE_CONSENT_CATEGORY_BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "consent category"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	category := req.ToCategory(orgHandle, categoryId)

	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	if err := service.UpdateConsentCategory(ctx, category); err != nil {
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
	categoryId := r.PathValue("categoryId")
	if categoryId == "" {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.CONSENT_CAT_ID.Code,
			Message:     errors.CONSENT_CAT_ID.Message,
			Description: "Category Id is required to fetch the consent category",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	service := provider.NewConsentCategoryProvider().GetConsentCategoryService()
	if err := service.DeleteConsentCategory(ctx, categoryId); err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}
