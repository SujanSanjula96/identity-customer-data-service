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
	"fmt"
	"net/http"
	"strings"

	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/authn"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/pagination"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

type IdentityResolutionHandler struct{}

func NewIdentityResolutionHandler() *IdentityResolutionHandler {
	return &IdentityResolutionHandler{}
}

func (h *IdentityResolutionHandler) GetReviewTasks(w http.ResponseWriter, r *http.Request) {
	logger := log.GetLogger()

	err := security.AuthnAndAuthz(r, "identity_resolution:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if orgHandle == "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: "Organization handle is required.",
		}, http.StatusBadRequest))
		return
	}

	pageSize, parseErr := pagination.ParsePageSize(r)
	if parseErr != nil {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: parseErr.Error(),
		}, http.StatusBadRequest))
		return
	}

	svc := provider.NewIdentityResolutionProvider().GetIdentityResolutionService()

	profileID := r.URL.Query().Get("profile_id")

	var response *irModel.ReviewTaskListResponse
	if profileID != "" {
		response, err = svc.GetPendingReviewTasksByProfile(r.Context(), orgHandle, profileID, pageSize)
	} else {
		response, err = svc.GetPendingReviewTasks(r.Context(), orgHandle, pageSize)
	}
	if err != nil {
		logger.Error("Handler: failed to fetch review tasks", log.Error(err))
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *IdentityResolutionHandler) ResolveReviewTask(w http.ResponseWriter, r *http.Request) {
	logger := log.GetLogger()

	err := security.AuthnAndAuthz(r, "identity_resolution:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if orgHandle == "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: "Organization handle is required.",
		}, http.StatusBadRequest))
		return
	}
	taskID := r.PathValue("taskId")
	if taskID == "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: "Task ID is required.",
		}, http.StatusBadRequest))
		return
	}

	var resolveReq irModel.ReviewResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&resolveReq); err != nil {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Failed to parse resolve request: %v", err),
		}, http.StatusBadRequest))
		return
	}

	if resolveReq.Decision != constants.ReviewStatusApproved && resolveReq.Decision != constants.ReviewStatusRejected {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.BAD_REQUEST.Code,
			Message: errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Invalid decision '%s'. Must be '%s' or '%s'.",
				resolveReq.Decision, constants.ReviewStatusApproved, constants.ReviewStatusRejected),
		}, http.StatusBadRequest))
		return
	}

	resolvedBy := extractResolverIdentity(r)
	if resolvedBy == "" {
		resolvedBy = constants.MergeByAdmin
	}
	approved := resolveReq.Decision == constants.ReviewStatusApproved

	svc := provider.NewIdentityResolutionProvider().GetIdentityResolutionService()
	err = svc.ResolveReviewTask(r.Context(), orgHandle, taskID, approved, resolvedBy, resolveReq.Notes)
	if err != nil {
		logger.Error("Handler: failed to resolve review task", log.Error(err))
		utils.HandleError(w, err)
		return
	}

	action := "rejected"
	if approved {
		action = "approved"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Review task %s %s", taskID, action),
	})
}

// extractResolverIdentity returns the identity of the caller resolving a review task,
// derived from the validated Bearer token on the request.
func extractResolverIdentity(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := authn.ParseJWTClaims(token)
	if err != nil {
		return ""
	}

	if sub, ok := claims[constants.SUBClaim].(string); ok && sub != "" {
		return sub
	}
	if azp, ok := claims[constants.AZPClaim].(string); ok && azp != "" {
		return azp
	}
	if clientID, ok := claims[constants.ClientIdClaim].(string); ok && clientID != "" {
		return clientID
	}
	return ""
}
