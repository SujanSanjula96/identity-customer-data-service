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
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/provider"

	"github.com/google/uuid"
)

type UnificationRulesHandler struct {
	store map[string]model.UnificationRule
	mu    *sync.RWMutex
}

func NewUnificationRulesHandler() *UnificationRulesHandler {

	return &UnificationRulesHandler{
		store: make(map[string]model.UnificationRule),
		mu:    &sync.RWMutex{},
	}
}

// AddUnificationRule handles adding a new rule
func (urh *UnificationRulesHandler) AddUnificationRule(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "unification_rules:create")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	var ruleInRequest model.UnificationRuleAPIRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ruleInRequest); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "unification rule"),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	// Set timestamps
	now := time.Now().UTC()
	rule := model.UnificationRule{
		RuleId:       uuid.New().String(),
		OrgHandle:    orgHandle,
		RuleName:     ruleInRequest.RuleName,
		PropertyName: ruleInRequest.PropertyName,
		Priority:     ruleInRequest.Priority,
		IsActive:     ruleInRequest.IsActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	err = ruleService.AddUnificationRule(ctx, rule, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	addedRule, err := ruleService.GetUnificationRule(ctx, rule.RuleId)
	addedRuleResponse := model.UnificationRuleAPIResponse{
		RuleId:       addedRule.RuleId,
		RuleName:     addedRule.RuleName,
		PropertyName: addedRule.PropertyName,
		Priority:     addedRule.Priority,
		IsActive:     addedRule.IsActive,
	}
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusCreated, addedRuleResponse, constants.UnificationRuleResource)
}

// GetUnificationRules handles fetching all rules
func (urh *UnificationRulesHandler) GetUnificationRules(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "unification_rules:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	rules, err := ruleService.GetUnificationRules(ctx, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	// Convert rules to API response format
	rulesResponse := make([]model.UnificationRuleAPIResponse, 0, len(rules))
	for _, rule := range rules {
		rulesResponse = append(rulesResponse, model.ToAPIResponse(rule))
	}
	utils.RespondJSON(w, http.StatusOK, rulesResponse, constants.UnificationRuleResource)
}

// GetUnificationRule Fetches a specific resolution rule.
func (urh *UnificationRulesHandler) GetUnificationRule(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "unification_rules:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	ruleId := r.PathValue("ruleId")
	if ruleId == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GET_UNIFICATION_RULE_WITHOUT_ID.Code,
			Message:     errors2.GET_UNIFICATION_RULE_WITHOUT_ID.Message,
			Description: "Invalid path for unification rule retrieval",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	rule, err := ruleService.GetUnificationRuleForOrg(ctx, ruleId, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, model.ToAPIResponse(*rule), constants.UnificationRuleResource)
}

// PatchUnificationRule applies partial updates to a unification rule.
func (urh *UnificationRulesHandler) PatchUnificationRule(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "unification_rules:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	ruleId := r.PathValue("ruleId")
	if ruleId == "" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	var ruleUpdateRequest model.UnificationRuleUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ruleUpdateRequest); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "unification rule"),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	updatedRule, err := ruleService.GetOwnedUnificationRule(ctx, ruleId, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	if ruleUpdateRequest.RuleName != nil {
		updatedRule.RuleName = *ruleUpdateRequest.RuleName
	}

	if ruleUpdateRequest.Priority != nil {
		updatedRule.Priority = *ruleUpdateRequest.Priority
	}

	if ruleUpdateRequest.IsActive != nil {
		updatedRule.IsActive = *ruleUpdateRequest.IsActive
	}

	err = ruleService.PatchUnificationRule(ctx, ruleId, orgHandle, *updatedRule)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	rule, err := ruleService.GetUnificationRule(ctx, ruleId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	ruleResponse := model.UnificationRuleAPIResponse{
		RuleId:       rule.RuleId,
		RuleName:     rule.RuleName,
		PropertyName: rule.PropertyName,
		Priority:     rule.Priority,
		IsActive:     rule.IsActive,
	}
	utils.RespondJSON(w, http.StatusOK, ruleResponse, constants.UnificationRuleResource)
}

// DeleteUnificationRule removes a resolution rule.
func (urh *UnificationRulesHandler) DeleteUnificationRule(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "unification_rules:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	ruleId := r.PathValue("ruleId")
	if ruleId == "" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	// Delete only a rule that the org owns. A rule of another org is not visible, so the delete is a
	// no-op for it.
	if _, err := ruleService.GetOwnedUnificationRule(ctx, ruleId, orgHandle); err != nil {
		if clientErr, ok := err.(*errors2.ClientError); ok && clientErr.StatusCode == http.StatusNotFound {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		utils.HandleError(w, err)
		return
	}
	err = ruleService.DeleteUnificationRule(ctx, ruleId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// isCDSEnabled checks if CDS is enabled for the given tenant
func isCDSEnabled(ctx context.Context, orgHandle string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(ctx, orgHandle)
}
