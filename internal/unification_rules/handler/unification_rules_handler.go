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
	"fmt"
	"net/http"
	"sync"
	"time"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/worker"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/provider"

	"github.com/google/uuid"
	shareHandler "github.com/wso2/identity-customer-data-service/internal/sharing/handler"
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

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// A rule that says nothing about how it matches is an exact match on a plain value, which
	// is how every rule written before typed matching is read. Requiring both fields would
	// make the simple case harder than it was, and would break a client that predates them.
	if ruleInRequest.UnificationMethod == "" {
		ruleInRequest.UnificationMethod = constants.UnificationMethodDeterministic
	}

	// The type is only load-bearing when matching is approximate: it picks the algorithm, and
	// not every kind of value has one. Asking for it then is asking for something the server
	// genuinely cannot infer. Under exact matching it still shapes behaviour — dates and
	// identifiers are normalized before comparing, and it seeds the evidence strengths — but a
	// caller that omits it is choosing the same default the engine has always applied.
	if ruleInRequest.AttributeType == "" {
		if ruleInRequest.UnificationMethod == constants.UnificationMethodFuzzy {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:    errors2.BAD_REQUEST.Code,
				Message: errors2.BAD_REQUEST.Message,
				Description: "attribute_type is required when unification_method is 'fuzzy', because it " +
					"selects how two values are compared. Allowed values for fuzzy matching: " +
					"FUZZY_STRING, NAME, EMAIL, PHONE, LOCATION.",
			}, http.StatusBadRequest)
			utils.WriteErrorResponse(w, clientError)

			return
		}

		ruleInRequest.AttributeType = constants.AttributeTypePrimitiveExact
	}

	// Validate AttributeType.
	if !constants.AllowedAttributeTypes[ruleInRequest.AttributeType] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Invalid attribute_type: '%s'. Allowed values: PRIMITIVE_EXACT, FUZZY_STRING, NAME, EMAIL, PHONE, LOCATION, DATE, UNIQUE_ID.", ruleInRequest.AttributeType),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	// Validate UnificationMethod.
	if ruleInRequest.UnificationMethod != constants.UnificationMethodFuzzy && ruleInRequest.UnificationMethod != constants.UnificationMethodDeterministic {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Invalid unification_method: '%s'. Allowed values: fuzzy, deterministic.", ruleInRequest.UnificationMethod),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	if ruleInRequest.UnificationMethod == constants.UnificationMethodFuzzy && !fuzzyMatchingAllowed() {
		utils.WriteErrorResponse(w, refuseFuzzy())
		return
	}

	// Reject fuzzy for attribute types that only support exact matching.
	if ruleInRequest.UnificationMethod == constants.UnificationMethodFuzzy && !constants.FuzzyCapableAttributeTypes[ruleInRequest.AttributeType] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Attribute type '%s' does not support fuzzy matching. Use 'deterministic' instead.", ruleInRequest.AttributeType),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	// Validate evidence strengths, defaulting each from the attribute type when omitted.
	matchStrength, strengthErr := resolveEvidenceStrength(ruleInRequest.MatchStrength, "match_strength")
	if strengthErr != nil {
		utils.WriteErrorResponse(w, strengthErr)
		return
	}
	mismatchStrength, strengthErr := resolveEvidenceStrength(ruleInRequest.MismatchStrength, "mismatch_strength")
	if strengthErr != nil {
		utils.WriteErrorResponse(w, strengthErr)
		return
	}

	// Set timestamps
	now := time.Now().UTC()
	rule := model.UnificationRule{
		RuleId:            uuid.New().String(),
		OrgId:             org.ID,
		RuleName:          ruleInRequest.RuleName,
		PropertyName:      ruleInRequest.PropertyName,
		Priority:          ruleInRequest.Priority,
		IsActive:          ruleInRequest.IsActive,
		AttributeType:     ruleInRequest.AttributeType,
		UnificationMethod: ruleInRequest.UnificationMethod,
		MatchStrength:     matchStrength,
		MismatchStrength:  mismatchStrength,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	err = ruleService.AddUnificationRule(ctx, rule, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	addedRule, err := ruleService.GetUnificationRule(ctx, rule.RuleId, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	addedRuleResponse := toRuleResponse(*addedRule)
	audit.SetTargetID(ctx, addedRule.RuleId)

	// Trigger reindex if rule is active.
	if addedRule.IsActive {
		utils.SafeGo("unification rule index backfill", func() { worker.IndexNewAttribute(ctx, org.ID, *addedRule) })
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
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	rules, err := ruleService.GetUnificationRules(ctx, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	// Convert rules to API response format
	rulesResponse := make([]model.UnificationRuleAPIResponse, 0, len(rules))
	for _, rule := range rules {
		rulesResponse = append(rulesResponse, toRuleResponse(rule))
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
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
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
	rule, err := ruleService.GetUnificationRuleForOrg(ctx, ruleId, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, toRuleResponse(*rule), constants.UnificationRuleResource)
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
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
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

	// Fetch old rule to detect is_active changes. Only a rule that the organization owns can change.
	oldRule, err := ruleService.GetOwnedUnificationRule(ctx, ruleId, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	wasActive := oldRule.IsActive

	updatedRule := *oldRule

	if ruleUpdateRequest.RuleName != nil {
		updatedRule.RuleName = *ruleUpdateRequest.RuleName
	}

	if ruleUpdateRequest.Priority != nil {
		updatedRule.Priority = *ruleUpdateRequest.Priority
	}

	if ruleUpdateRequest.IsActive != nil {
		updatedRule.IsActive = *ruleUpdateRequest.IsActive
	}

	if ruleUpdateRequest.AttributeType != nil {
		if !constants.AllowedAttributeTypes[*ruleUpdateRequest.AttributeType] {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.BAD_REQUEST.Code,
				Message:     errors2.BAD_REQUEST.Message,
				Description: fmt.Sprintf("Invalid attribute_type: '%s'. Allowed values: PRIMITIVE_EXACT, FUZZY_STRING, NAME, EMAIL, PHONE, LOCATION, DATE, UNIQUE_ID.", *ruleUpdateRequest.AttributeType),
			}, http.StatusBadRequest)
			utils.WriteErrorResponse(w, clientError)
			return
		}
		updatedRule.AttributeType = *ruleUpdateRequest.AttributeType
	}

	if ruleUpdateRequest.UnificationMethod != nil {
		if *ruleUpdateRequest.UnificationMethod != constants.UnificationMethodFuzzy && *ruleUpdateRequest.UnificationMethod != constants.UnificationMethodDeterministic {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.BAD_REQUEST.Code,
				Message:     errors2.BAD_REQUEST.Message,
				Description: fmt.Sprintf("Invalid unification_method: '%s'. Allowed values: fuzzy, deterministic.", *ruleUpdateRequest.UnificationMethod),
			}, http.StatusBadRequest)
			utils.WriteErrorResponse(w, clientError)
			return
		}
		updatedRule.UnificationMethod = *ruleUpdateRequest.UnificationMethod
	}

	if updatedRule.UnificationMethod == constants.UnificationMethodFuzzy && !fuzzyMatchingAllowed() {
		utils.WriteErrorResponse(w, refuseFuzzy())
		return
	}

	// Cross-validate: reject fuzzy for attribute types that only support exact matching.
	if updatedRule.UnificationMethod == constants.UnificationMethodFuzzy && !constants.FuzzyCapableAttributeTypes[updatedRule.AttributeType] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("Attribute type '%s' does not support fuzzy matching. Use 'deterministic' instead.", updatedRule.AttributeType),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	if ruleUpdateRequest.MatchStrength != nil {
		strength, strengthErr := resolveEvidenceStrength(*ruleUpdateRequest.MatchStrength, "match_strength")
		if strengthErr != nil {
			utils.WriteErrorResponse(w, strengthErr)
			return
		}
		updatedRule.MatchStrength = strength
	}
	if ruleUpdateRequest.MismatchStrength != nil {
		strength, strengthErr := resolveEvidenceStrength(*ruleUpdateRequest.MismatchStrength, "mismatch_strength")
		if strengthErr != nil {
			utils.WriteErrorResponse(w, strengthErr)
			return
		}
		updatedRule.MismatchStrength = strength
	}
	err = ruleService.PatchUnificationRule(ctx, ruleId, org.ID, updatedRule)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	newRule, err := ruleService.GetUnificationRule(ctx, ruleId, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	// Detect activation / deactivation and trigger reindex.
	nowActive := newRule.IsActive
	if !wasActive && nowActive {
		utils.SafeGo("unification rule index backfill", func() { worker.IndexNewAttribute(ctx, org.ID, *newRule) })
	}
	if wasActive && !nowActive {
		utils.SafeGo("unification rule index removal", func() { worker.RemoveAttributeIndex(ctx, org.ID, newRule.PropertyName) })
	}

	// Either of these changes the shape of the keys the rule writes, so what is already in
	// the index no longer matches what the rule would produce now.
	//
	// attribute_type decides the normalisation and which extra keys exist at all;
	// unification_method decides whether the recall-widening keys are written — a rule
	// switched from deterministic to fuzzy has only its exact key indexed, so it keeps
	// behaving exactly like a deterministic rule for every existing profile until each one
	// happens to be rewritten. The rule looks enabled and finds nothing new.
	keyShapeChanged := oldRule.AttributeType != newRule.AttributeType ||
		oldRule.UnificationMethod != newRule.UnificationMethod
	if wasActive && nowActive && keyShapeChanged {
		utils.SafeGo("unification rule index rebuild", func() {
			worker.RemoveAttributeIndex(ctx, org.ID, newRule.PropertyName)
			worker.IndexNewAttribute(ctx, org.ID, *newRule)
		})
	}

	ruleResponse := toRuleResponse(*newRule)
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
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
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
	// Delete only a rule that the organization owns.
	rule, err := ruleService.GetOwnedUnificationRule(ctx, ruleId, org.ID)
	if err != nil {
		if clientErr, ok := err.(*errors2.ClientError); ok && clientErr.StatusCode == http.StatusNotFound {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		utils.HandleError(w, err)
		return
	}
	err = ruleService.DeleteUnificationRule(ctx, ruleId, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	// If the deleted rule was active, trigger cleanup of its blocking keys.
	if rule != nil && rule.IsActive {
		utils.SafeGo("unification rule index removal", func() { worker.RemoveAttributeIndex(ctx, org.ID, rule.PropertyName) })
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// GetUnificationOptions handles GET /unification-rules/options.
// Returns the supported attribute types and the matching methods available for each.
func (urh *UnificationRulesHandler) GetUnificationOptions(w http.ResponseWriter, r *http.Request) {
	if err := security.AuthnAndAuthz(r, "unification_rules:view"); err != nil {
		utils.HandleError(w, err)
		return
	}

	ruleService := provider.NewUnificationRuleProvider().GetUnificationRuleService()
	resp := ruleService.GetUnificationOptions()
	utils.RespondJSON(w, http.StatusOK, resp, constants.UnificationOptionsResource)
}

// isCDSEnabled checks if CDS is enabled for the given tenant
func isCDSEnabled(ctx context.Context, orgId string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(ctx, orgId)
}

// CreateSharingPolicy shares a rule of the organization.
func (h *UnificationRulesHandler) CreateSharingPolicy(w http.ResponseWriter, r *http.Request) {

	org, ok := shareHandler.InitiatorOf(w, r, "unification_rules:share_update")
	if !ok {
		return
	}
	req, ok := shareHandler.DecodePolicyRequest(w, r)
	if !ok {
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	resp, err := ruleService.CreateSharingPolicy(r.Context(), *org, r.PathValue("ruleId"), req)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	audit.SetTargetID(r.Context(), resp.Id)
	utils.RespondJSON(w, http.StatusCreated, resp, shareHandler.PolicyResource)
}

// ListSharingPolicies returns the sharing policies of a rule of the organization.
func (h *UnificationRulesHandler) ListSharingPolicies(w http.ResponseWriter, r *http.Request) {

	org, ok := shareHandler.InitiatorOf(w, r, "unification_rules:share_view")
	if !ok {
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	resp, err := ruleService.ListSharingPolicies(r.Context(), *org, r.PathValue("ruleId"))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, shareHandler.PolicyResource)
}

// GetSharingPolicy returns a sharing policy of a rule of the organization.
func (h *UnificationRulesHandler) GetSharingPolicy(w http.ResponseWriter, r *http.Request) {

	org, ok := shareHandler.InitiatorOf(w, r, "unification_rules:share_view")
	if !ok {
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	resp, err := ruleService.GetSharingPolicy(r.Context(), *org,
		r.PathValue("ruleId"), r.PathValue("policyId"))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, shareHandler.PolicyResource)
}

// UpdateSharingPolicy replaces a sharing policy of a rule of the organization.
func (h *UnificationRulesHandler) UpdateSharingPolicy(w http.ResponseWriter, r *http.Request) {

	org, ok := shareHandler.InitiatorOf(w, r, "unification_rules:share_update")
	if !ok {
		return
	}
	req, ok := shareHandler.DecodePolicyRequest(w, r)
	if !ok {
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	resp, err := ruleService.UpdateSharingPolicy(r.Context(), *org,
		r.PathValue("ruleId"), r.PathValue("policyId"), req)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, shareHandler.PolicyResource)
}

// DeleteSharingPolicy removes a sharing policy of a rule of the organization.
func (h *UnificationRulesHandler) DeleteSharingPolicy(w http.ResponseWriter, r *http.Request) {

	org, ok := shareHandler.InitiatorOf(w, r, "unification_rules:share_update")
	if !ok {
		return
	}
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	if err := ruleService.DeleteSharingPolicy(r.Context(), *org,
		r.PathValue("ruleId"), r.PathValue("policyId")); err != nil {
		utils.HandleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fuzzyMatchingAllowed reports whether this server offers tolerant matching at all.
func fuzzyMatchingAllowed() bool {
	return config.GetCDSRuntime().Config.IdentityResolution.FuzzyMatchingEnabled
}

// refuseFuzzy is the error returned when a caller asks for tolerant matching on a server
// where it is switched off. It is refused rather than quietly downgraded to an exact match:
// a caller that believes a rule tolerates typos, and is overruled, would misread every
// merge decision the rule takes part in.
func refuseFuzzy() *errors2.ClientError {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.BAD_REQUEST.Code,
		Message: errors2.BAD_REQUEST.Message,
		Description: "unification_method 'fuzzy' is not available on this server. Enable " +
			"identity_resolution.fuzzy_matching_enabled to use tolerant matching.",
	}, http.StatusBadRequest)
}

// resolveEvidenceStrength decides what to store for a rule's evidence strength.
//
// It returns an empty string when the operator supplied nothing, which is stored as NULL and
// derived from the attribute type every time the rule is read. Persisting the derived value
// instead would freeze it at creation time, so a later change of attribute_type would leave
// the rule scoring as the old type — and, worse, would be indistinguishable from an operator
// having chosen that strength deliberately.
//
// Unless the server allows overrides, a supplied value is refused rather than silently
// dropped: a caller that believes it set the strength and is ignored would misread every
// merge decision that follows.
func resolveEvidenceStrength(supplied string, field string) (string, *errors2.ClientError) {
	if supplied == "" {
		return "", nil
	}
	if !evidenceStrengthOverrideAllowed() {
		return "", errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.BAD_REQUEST.Code,
			Message: errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf(
				"%s cannot be set on this server. It is derived from attribute_type; enable "+
					"identity_resolution.allow_evidence_strength_override to set it per rule.", field),
		}, http.StatusBadRequest)
	}
	if !constants.AllowedEvidenceStrengths[supplied] {
		return "", invalidEvidenceStrength(field, supplied)
	}
	return supplied, nil
}

// toRuleResponse converts a rule to its API response, with the evidence strengths that the server
// exposes.
func toRuleResponse(rule model.UnificationRule) model.UnificationRuleAPIResponse {
	response := model.ToAPIResponse(rule)
	response.MatchStrength = exposedStrength(rule.MatchStrength)
	response.MismatchStrength = exposedStrength(rule.MismatchStrength)
	return response
}

// exposedStrength reports what a response should carry for a rule's evidence strength.
//
// Nothing, unless this server lets an operator set one: while the override is refused on the
// way in, describing a strength on the way out would report a setting no caller can make,
// and the value is derived from attribute_type, which the response already carries.
//
// Where the override is allowed it is the stored value and not the derived one, so an
// omitted field means the rule inherits from its attribute type — the same thing the row
// holds, and the only form a caller can send back without turning an inherited strength
// into a deliberate one.
func exposedStrength(stored string) string {
	if !evidenceStrengthOverrideAllowed() {
		return ""
	}
	return stored
}

// evidenceStrengthOverrideAllowed reports whether this deployment lets a rule carry its own
// evidence strengths.
func evidenceStrengthOverrideAllowed() bool {
	return config.GetCDSRuntime().Config.IdentityResolution.AllowEvidenceStrengthOverride
}

func invalidEvidenceStrength(field, value string) *errors2.ClientError {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.BAD_REQUEST.Code,
		Message: errors2.BAD_REQUEST.Message,
		Description: fmt.Sprintf("Invalid %s: '%s'. Allowed values: %s, %s, %s.", field, value,
			constants.EvidenceStrengthHigh, constants.EvidenceStrengthMedium, constants.EvidenceStrengthLow),
	}, http.StatusBadRequest)
}
