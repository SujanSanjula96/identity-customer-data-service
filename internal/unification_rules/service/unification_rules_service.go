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

package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/store"
)

type UnificationRuleServiceInterface interface {
	AddUnificationRule(ctx context.Context, rule model.UnificationRule, orgHandle string) error
	GetUnificationRules(ctx context.Context, orgHandle string) ([]model.UnificationRule, error)
	GetUnificationRule(ctx context.Context, ruleId string) (*model.UnificationRule, error)
	// GetUnificationRuleForOrg returns the rule when the org owns it or a shared rule reaches it.
	GetUnificationRuleForOrg(ctx context.Context, ruleId, orgHandle string) (*model.UnificationRule, error)
	// GetOwnedUnificationRule returns the rule when the org owns it. It returns 403 for a shared rule.
	GetOwnedUnificationRule(ctx context.Context, ruleId, orgHandle string) (*model.UnificationRule, error)
	PatchUnificationRule(ctx context.Context, ruleId, orgHandle string, updatedRule model.UnificationRule) error
	DeleteUnificationRule(ctx context.Context, ruleId string) error
}

// UnificationRuleService is the default implementation of the UnificationRuleServiceInterface.
type UnificationRuleService struct{}

// GetUnificationRuleService creates a new instance of UnificationRuleService.
func GetUnificationRuleService() UnificationRuleServiceInterface {

	return &UnificationRuleService{}
}

// AddUnificationRule Adds a new unification rule.
func (urs *UnificationRuleService) AddUnificationRule(ctx context.Context,
	rule model.UnificationRule, orgHandle string) error {

	logger := log.GetLogger()
	// Need to specifically prevent
	if rule.PropertyName == "user_id" || rule.PropertyName == "identity_attributes.user_id" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ADD_UNIFICATION_RULE.Code,
			Message:     errors2.ADD_UNIFICATION_RULE.Message,
			Description: "user_id based unification rule can not be created.",
		}, http.StatusBadRequest)
	}

	if strings.HasPrefix(rule.PropertyName, constants.ApplicationData+".") {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ADD_UNIFICATION_RULE.Code,
			Message:     errors2.ADD_UNIFICATION_RULE.Message,
			Description: "Creating unification rules based on application data is not supported.",
		}, http.StatusBadRequest)
	}

	schemaAttribute, err := findEffectiveAttribute(ctx, rule.PropertyName, orgHandle)

	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while checking for the property: %s", rule.PropertyName)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_UNIFICATION_RULE.Code,
			Message:     errors2.ADD_UNIFICATION_RULE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	if schemaAttribute == nil {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ADD_UNIFICATION_RULE.Code,
			Message:     errors2.ADD_UNIFICATION_RULE.Message,
			Description: fmt.Sprintf("PropertyName  '%s' is not found in schema", rule.PropertyName),
		}, http.StatusBadRequest)
	}
	if schemaAttribute.ValueType == constants.ComplexDataType {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.ADD_UNIFICATION_RULE.Code,
			Message: errors2.ADD_UNIFICATION_RULE.Message,
			Description: "Unification rule with property " + rule.PropertyName + " is not allowed as it is a complex data type. " +
				"Choose the sub-attribute instead.",
		}, http.StatusBadRequest)
	}

	// A local rule cannot use a property that an active shared rule of the org uses.
	sharedRules, err := sharingService.ActiveSharedRules(ctx, orgHandle)
	if err != nil {
		return err
	}
	for _, shared := range sharedRules {
		if shared.Rule.PropertyName == rule.PropertyName {
			return sharingService.ConflictError(fmt.Sprintf("The unification rule '%s' on property '%s' is shared "+
				"with this organization by '%s'.", shared.Rule.RuleName, rule.PropertyName, shared.Rule.OrgHandle))
		}
	}

	// Check if a similar unification rule already exists
	existingRules, err := store.GetUnificationRules(ctx, orgHandle)
	if err != nil {
		return err
	}
	for _, existingRule := range existingRules {
		if existingRule.PropertyName == rule.PropertyName {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UNIFICATION_RULE_ALREADY_EXISTS.Code,
				Message:     errors2.UNIFICATION_RULE_ALREADY_EXISTS.Message,
				Description: fmt.Sprintf("Unification rule with property %s already exists", rule.PropertyName),
			}, http.StatusConflict)
		}
		if existingRule.Priority == rule.Priority {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UNIFICATION_RULE_PRIORITY_EXISTS.Code,
				Message:     errors2.UNIFICATION_RULE_PRIORITY_EXISTS.Message,
				Description: "Unification rule with same priority exist.",
			}, http.StatusBadRequest)
		}
	}
	rule.PropertyId = schemaAttribute.AttributeId
	if err := store.AddUnificationRule(ctx, rule, orgHandle); err != nil {
		return err
	}
	recomputeShares(ctx, orgHandle)
	return nil
}

// GetUnificationRules returns the rules of the org in the evaluation order. For an org in a B2B
// tree, the shared rules that are active in the org come first (the group of the farthest
// ancestor first, and the owner priority in each group), and then the rules that the org owns,
// in their priority order.
func (urs *UnificationRuleService) GetUnificationRules(ctx context.Context,
	orgHandle string) ([]model.UnificationRule, error) {

	owned, err := store.GetUnificationRules(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return owned, nil
	}

	shared, err := sharingService.ActiveSharedRules(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	effectiveNames, err := effectiveAttributeNames(ctx, orgHandle)
	if err != nil {
		return nil, err
	}

	result := make([]model.UnificationRule, 0, len(shared)+len(owned))
	for _, s := range shared {
		rule := s.Rule
		rule.Origin = shareModel.OriginShared
		rule.OwnerOrgHandle = rule.OrgHandle
		rule.State = shareModel.StateActive
		result = append(result, rule)
	}
	sort.SliceStable(owned, func(i, j int) bool { return owned[i].Priority < owned[j].Priority })
	for _, rule := range owned {
		rule.Origin = shareModel.OriginOwned
		rule.State = shareModel.StateActive
		// A local rule can use a shared attribute. When the attribute stops being visible, the
		// rule does not run.
		if !effectiveNames[rule.PropertyName] {
			rule.State = shareModel.StateInactiveMissingAttribute
		}
		result = append(result, rule)
	}
	for i := range result {
		result[i].Rank = i + 1
	}
	return result, nil
}

// GetUnificationRuleForOrg returns the rule when the org owns it or a shared rule reaches it.
func (urs *UnificationRuleService) GetUnificationRuleForOrg(ctx context.Context,
	ruleId, orgHandle string) (*model.UnificationRule, error) {

	rule, err := store.GetUnificationRule(ctx, ruleId)
	if err != nil {
		return nil, err
	}
	if rule != nil && rule.OrgHandle == orgHandle {
		return rule, nil
	}
	if rule != nil {
		states, err := sharingService.StatesForOrg(ctx, shareModel.ResourceUnificationRule, orgHandle)
		if err != nil {
			return nil, err
		}
		if state, ok := states[ruleId]; ok {
			rule.Origin = shareModel.OriginShared
			rule.OwnerOrgHandle = rule.OrgHandle
			rule.State = state.State
			return rule, nil
		}
	}
	return nil, ruleNotFound(ruleId)
}

// GetOwnedUnificationRule returns the rule when the org owns it. It returns 403 for a shared rule.
func (urs *UnificationRuleService) GetOwnedUnificationRule(ctx context.Context,
	ruleId, orgHandle string) (*model.UnificationRule, error) {

	rule, err := urs.GetUnificationRuleForOrg(ctx, ruleId, orgHandle)
	if err != nil {
		return nil, err
	}
	if rule.Origin == shareModel.OriginShared {
		return nil, sharingService.ReadOnlyError()
	}
	return rule, nil
}

func ruleNotFound(ruleId string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.UNIFICATION_RULE_NOT_FOUND.Code,
		Message:     errors2.UNIFICATION_RULE_NOT_FOUND.Message,
		Description: fmt.Sprintf("Unification rule: '%s' not found", ruleId),
	}, http.StatusNotFound)
}

// GetUnificationRule Fetches a specific resolution rule.
func (urs *UnificationRuleService) GetUnificationRule(ctx context.Context,
	ruleId string) (*model.UnificationRule, error) {

	unificationRule, err := store.GetUnificationRule(ctx, ruleId)
	if err != nil {
		return nil, err
	}
	if unificationRule == nil {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UNIFICATION_RULE_NOT_FOUND.Code,
			Message:     errors2.UNIFICATION_RULE_NOT_FOUND.Message,
			Description: fmt.Sprintf("Unification rule: '%s' not found", ruleId),
		}, http.StatusNotFound)
	}
	return unificationRule, err
}

// PatchUnificationRule Applies a partial update on a specific resolution rule.
func (urs *UnificationRuleService) PatchUnificationRule(ctx context.Context,
	ruleId, orgHandle string, updatedRule model.UnificationRule) error {

	if updatedRule.PropertyName == "user_id" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UNIFICATION_RULE_ALREADY_EXISTS.Code,
			Message:     errors2.UNIFICATION_RULE_ALREADY_EXISTS.Message,
			Description: "user_id based unification rule can not be updated.",
		}, http.StatusBadRequest)
	}

	// Validate that the priority is not already in use
	existingRules, err := store.GetUnificationRules(ctx, orgHandle)
	if err != nil {
		return err
	}
	for _, existingRule := range existingRules {
		if existingRule.RuleId != ruleId && existingRule.Priority == updatedRule.Priority {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UNIFICATION_RULE_PRIORITY_EXISTS.Code,
				Message:     errors2.UNIFICATION_RULE_PRIORITY_EXISTS.Message,
				Description: "Unification rule with same priority exist.",
			}, http.StatusBadRequest)
		}
	}
	if err := store.PatchUnificationRule(ctx, ruleId, updatedRule); err != nil {
		return err
	}
	recomputeShares(ctx, orgHandle)
	return nil
}

// DeleteUnificationRule Removes a unification rule. When the rule is shared, CDS deletes all its
// policies.
func (urs *UnificationRuleService) DeleteUnificationRule(ctx context.Context, ruleId string) error {

	rule, err := store.GetUnificationRule(ctx, ruleId)
	if err != nil {
		return err
	}
	if err := store.DeleteUnificationRule(ctx, ruleId); err != nil {
		return err
	}
	if rule == nil {
		return nil
	}
	if org, err := orgStore.GetOrganizationByHandle(ctx, rule.OrgHandle); err == nil && org != nil {
		if err := sharingService.DeletePoliciesOfResource(ctx, shareModel.ResourceUnificationRule, ruleId,
			org.RootOrgId); err != nil {
			return err
		}
	}
	return nil
}

// findEffectiveAttribute returns the attribute with the name from the effective schema of the org:
// an attribute that the org owns, or a shared attribute that is active in the org.
func findEffectiveAttribute(ctx context.Context, attributeName,
	orgHandle string) (*schemaModel.ProfileSchemaAttribute, error) {

	attrs, err := schemaService.GetEffectiveProfileSchemaAttributes(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	for _, attr := range attrs {
		if attr.AttributeName == attributeName {
			found := attr
			return &found, nil
		}
	}
	return nil, nil
}

func effectiveAttributeNames(ctx context.Context, orgHandle string) (map[string]bool, error) {

	attrs, err := schemaService.GetEffectiveProfileSchemaAttributes(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(attrs))
	for _, attr := range attrs {
		names[attr.AttributeName] = true
	}
	return names, nil
}

// ValidateShareableRule returns the rule when the org owns it, so that the org can share it.
func ValidateShareableRule(ctx context.Context, ruleId, orgHandle string) (*model.UnificationRule, error) {
	return GetUnificationRuleService().GetOwnedUnificationRule(ctx, ruleId, orgHandle)
}

func recomputeShares(ctx context.Context, orgHandle string) {

	if err := sharingService.RecomputeForOrgHandle(ctx, orgHandle); err != nil {
		log.GetLogger().Warn(fmt.Sprintf("Failed to evaluate the share states after a rule change in "+
			"organization '%s'.", orgHandle), log.Error(err))
	}
}
