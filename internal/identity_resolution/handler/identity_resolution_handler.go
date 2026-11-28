/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
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

// Package handler serves the identity resolution REST APIs for listing and resolving merge review tasks.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/provider"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/authn"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/pagination"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// IdentityResolutionHandler handles the identity resolution review task requests.
type IdentityResolutionHandler struct{}

// NewIdentityResolutionHandler returns a new IdentityResolutionHandler.
func NewIdentityResolutionHandler() *IdentityResolutionHandler {
	return &IdentityResolutionHandler{}
}

// GetReviewTasks lists the organization's pending review tasks, only those involving the profile_id query
// parameter when it is given. The caller needs the identity_resolution:view scope.
func (h *IdentityResolutionHandler) GetReviewTasks(w http.ResponseWriter, r *http.Request) {
	logger := log.GetLogger()

	err := security.AuthnAndAuthz(r, "identity_resolution:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(r.Context(), org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}

	pageSize, parseErr := pagination.ParsePageSize(r)
	if parseErr != nil {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BadRequest.Code,
			Message:     errors2.BadRequest.Message,
			Description: parseErr.Error(),
		}, http.StatusBadRequest))
		return
	}

	svc := provider.NewIdentityResolutionProvider().GetIdentityResolutionService()

	profileID := r.URL.Query().Get("profile_id")

	var response *irModel.ReviewTaskListResponse
	if profileID != "" {
		response, err = svc.GetPendingReviewTasksByProfile(r.Context(), org.ID, profileID, pageSize)
	} else {
		response, err = svc.GetPendingReviewTasks(r.Context(), org.ID, pageSize)
	}
	if err != nil {
		logger.Error("Handler: failed to fetch review tasks", log.Error(err))
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// ResolveReviewTask approves or rejects the review task named in the path, as the request body's decision says.
// The caller needs the identity_resolution:update scope.
func (h *IdentityResolutionHandler) ResolveReviewTask(w http.ResponseWriter, r *http.Request) {
	logger := log.GetLogger()

	err := security.AuthnAndAuthz(r, "identity_resolution:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(r.Context(), org.ID) {
		utils.HandleError(w, cdsNotEnabled())
		return
	}
	taskID := r.PathValue("taskId")
	if taskID == "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BadRequest.Code,
			Message:     errors2.BadRequest.Message,
			Description: "Task ID is required.",
		}, http.StatusBadRequest))
		return
	}

	var resolveReq irModel.ReviewResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&resolveReq); err != nil {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BadRequest.Code,
			Message:     errors2.BadRequest.Message,
			Description: utils.HandleDecodeError(err, "review task resolve"),
		}, http.StatusBadRequest))
		return
	}

	if resolveReq.Decision != constants.ReviewStatusApproved && resolveReq.Decision != constants.ReviewStatusRejected {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.BadRequest.Code,
			Message: errors2.BadRequest.Message,
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
	audit.AddDetail(r.Context(), "decision", resolveReq.Decision)

	svc := provider.NewIdentityResolutionProvider().GetIdentityResolutionService()
	err = svc.ResolveReviewTask(r.Context(), org.ID, taskID, approved, resolvedBy, resolveReq.Notes)
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
	if clientID, ok := claims[constants.ClientIDClaim].(string); ok && clientID != "" {
		return clientID
	}
	return ""
}

// isCDSEnabled reports whether CDS is enabled for the organization. A sub organization is enabled only when
// the organization access of its primary organization includes it.
func isCDSEnabled(ctx context.Context, orgID string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(ctx, orgID)
}

func cdsNotEnabled() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.CDSNotEnabled.Code,
		Message:     errors2.CDSNotEnabled.Message,
		Description: errors2.CDSNotEnabled.Description,
	}, http.StatusBadRequest)
}
