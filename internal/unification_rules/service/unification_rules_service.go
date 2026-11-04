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

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/organization/suborg"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/store"
)

type UnificationRuleServiceInterface interface {
	AddUnificationRule(ctx context.Context, rule model.UnificationRule, orgHandle string) error
	GetUnificationRules(ctx context.Context, orgHandle string) ([]model.UnificationRule, error)
	GetUnificationRule(ctx context.Context, ruleId, orgHandle string) (*model.UnificationRule, error)
	// GetUnificationRuleForOrg returns the rule when the organization owns it or when it is an
	// active shared rule in the organization.
	GetUnificationRuleForOrg(ctx context.Context, ruleId, orgHandle string) (*model.UnificationRule, error)
	// GetOwnedUnificationRule returns the rule when the organization owns it.
	GetOwnedUnificationRule(ctx context.Context, ruleId, orgHandle string) (*model.UnificationRule, error)
	PatchUnificationRule(ctx context.Context, ruleId, orgHandle string, updatedRule model.UnificationRule) error
	DeleteUnificationRule(ctx context.Context, ruleId, orgHandle string) error
	CreateSharingPolicy(ctx context.Context, initiator orgModel.Organization, ruleId string,
		req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error)
	ListSharingPolicies(ctx context.Context, initiator orgModel.Organization,
		ruleId string) (*shareModel.PolicyList, error)
	GetSharingPolicy(ctx context.Context, initiator orgModel.Organization, ruleId,
		policyId string) (*shareModel.PolicyResponse, error)
	UpdateSharingPolicy(ctx context.Context, initiator orgModel.Organization, ruleId, policyId string,
		req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error)
	DeleteSharingPolicy(ctx context.Context, initiator orgModel.Organization, ruleId, policyId string) error
	// GetUnificationOptions reads no data, so it takes no context.
	GetUnificationOptions() model.UnificationOptionsResponse
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

	if err := suborg.RejectWrite(ctx, orgHandle); err != nil {
		return err
	}
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

	// A local rule cannot use a property that an active shared rule of the organization uses.
	sharedRules, err := activeSharedRulesOfHandle(ctx, orgHandle)
	if err != nil {
		return err
	}
	for _, shared := range sharedRules {
		if shared.PropertyName == rule.PropertyName {
			return sharingService.ConflictError(fmt.Sprintf("The unification rule '%s' on property '%s' is shared "+
				"with this organization by '%s'.", shared.RuleName, rule.PropertyName, shared.OrgHandle))
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
	return nil
}

// GetUnificationRules returns the rules of the organization in evaluation order: the active shared
// rules first, then the owned rules by priority.
func (urs *UnificationRuleService) GetUnificationRules(ctx context.Context,
	orgHandle string) ([]model.UnificationRule, error) {

	owned, err := store.GetUnificationRules(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	org, err := orgService.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return owned, nil
	}

	shared, err := activeSharedRules(ctx, *org)
	if err != nil {
		return nil, err
	}
	effectiveNames, err := effectiveAttributeNames(ctx, orgHandle)
	if err != nil {
		return nil, err
	}

	result := make([]model.UnificationRule, 0, len(shared)+len(owned))
	for _, rule := range shared {
		rule.Origin = shareModel.OriginShared
		rule.OwnerOrgHandle = rule.OrgHandle
		rule.State = shareModel.StateActive
		result = append(result, rule)
	}
	sort.SliceStable(owned, func(i, j int) bool { return owned[i].Priority < owned[j].Priority })
	for _, rule := range owned {
		rule.Origin = shareModel.OriginOwned
		rule.State = shareModel.StateActive
		// A rule whose attribute is not visible does not run.
		if !effectiveNames[rule.PropertyName] {
			rule.State = model.StateInactiveMissingAttribute
		}
		result = append(result, rule)
	}
	for i := range result {
		result[i].Rank = i + 1
	}
	return result, nil
}

// GetUnificationRuleForOrg returns the rule when the organization owns it or when it is an active
// shared rule in the organization.
func (urs *UnificationRuleService) GetUnificationRuleForOrg(ctx context.Context,
	ruleId, orgHandle string) (*model.UnificationRule, error) {

	rule, err := store.GetUnificationRule(ctx, ruleId, orgHandle)
	if err != nil {
		return nil, err
	}
	if rule != nil {
		return rule, nil
	}
	org, err := orgService.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, unificationRuleNotFoundError(ruleId)
	}
	shared, err := getSharedRules(ctx, *org)
	if err != nil {
		return nil, err
	}
	for _, s := range shared {
		if s.Rule.RuleId == ruleId && s.State == shareModel.StateActive {
			sharedRule := s.Rule
			sharedRule.Origin = shareModel.OriginShared
			sharedRule.OwnerOrgHandle = sharedRule.OrgHandle
			sharedRule.State = s.State
			return &sharedRule, nil
		}
	}
	return nil, unificationRuleNotFoundError(ruleId)
}

// GetOwnedUnificationRule returns the rule when the organization owns it.
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

// GetUnificationRule Fetches a specific resolution rule.
func (urs *UnificationRuleService) GetUnificationRule(ctx context.Context,
	ruleId, orgHandle string) (*model.UnificationRule, error) {

	unificationRule, err := store.GetUnificationRule(ctx, ruleId, orgHandle)
	if err != nil {
		return nil, err
	}
	if unificationRule == nil {
		return nil, unificationRuleNotFoundError(ruleId)
	}
	return unificationRule, err
}

func unificationRuleNotFoundError(ruleId string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.UNIFICATION_RULE_NOT_FOUND.Code,
		Message:     errors2.UNIFICATION_RULE_NOT_FOUND.Message,
		Description: fmt.Sprintf("Unification rule: '%s' not found", ruleId),
	}, http.StatusNotFound)
}

// PatchUnificationRule Applies a partial update on a specific resolution rule.
func (urs *UnificationRuleService) PatchUnificationRule(ctx context.Context,
	ruleId, orgHandle string, updatedRule model.UnificationRule) error {

	if err := suborg.RejectWrite(ctx, orgHandle); err != nil {
		return err
	}
	if updatedRule.PropertyName == "user_id" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UNIFICATION_RULE_ALREADY_EXISTS.Code,
			Message:     errors2.UNIFICATION_RULE_ALREADY_EXISTS.Message,
			Description: "user_id based unification rule can not be updated.",
		}, http.StatusBadRequest)
	}

	// Validate that the rule exists in the org, and that the priority is not already in use
	existingRules, err := store.GetUnificationRules(ctx, orgHandle)
	if err != nil {
		return err
	}
	if !containsRule(existingRules, ruleId) {
		return unificationRuleNotFoundError(ruleId)
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
	return store.PatchUnificationRule(ctx, ruleId, orgHandle, updatedRule)
}

// DeleteUnificationRule deletes a unification rule of the organization and its sharing policies.
func (urs *UnificationRuleService) DeleteUnificationRule(ctx context.Context, ruleId, orgHandle string) error {

	if err := suborg.RejectWrite(ctx, orgHandle); err != nil {
		return err
	}
	rule, err := store.GetUnificationRule(ctx, ruleId, orgHandle)
	if err != nil || rule == nil {
		return err
	}
	if err := store.DeleteUnificationRule(ctx, ruleId, orgHandle); err != nil {
		return err
	}
	return sharingService.DeletePoliciesOfResource(ctx, model.SharingResourceType, ruleId)
}

// findEffectiveAttribute returns the attribute with the name from the effective schema of the
// organization.
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

// sharedRule is a rule that an ancestor shares with the organization, with its state in the
// organization.
type sharedRule struct {
	Rule       model.UnificationRule
	OwnerDepth int
	State      string
}

// activeSharedRulesOfHandle returns the active shared rules of the organization of the handle.
func activeSharedRulesOfHandle(ctx context.Context, orgHandle string) ([]model.UnificationRule, error) {

	org, err := orgService.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return nil, err
	}
	return activeSharedRules(ctx, *org)
}

// activeSharedRules returns the active shared rules of the organization in evaluation order. The
// rules of the farthest owner run first.
func activeSharedRules(ctx context.Context, org orgModel.Organization) ([]model.UnificationRule, error) {

	shared, err := getSharedRules(ctx, org)
	if err != nil {
		return nil, err
	}
	var active []sharedRule
	for _, s := range shared {
		if s.State == shareModel.StateActive {
			active = append(active, s)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].OwnerDepth != active[j].OwnerDepth {
			return active[i].OwnerDepth > active[j].OwnerDepth
		}
		return active[i].Rule.Priority < active[j].Rule.Priority
	})
	rules := make([]model.UnificationRule, 0, len(active))
	for _, s := range active {
		rules = append(rules, s.Rule)
	}
	return rules, nil
}

// getSharedRules returns the rules that the ancestors share with the organization, with the state
// of each rule in the organization.
func getSharedRules(ctx context.Context, org orgModel.Organization) ([]sharedRule, error) {

	shared, err := sharingService.GetSharedResources(ctx, model.SharingResourceType, org)
	if err != nil || len(shared) == 0 {
		return nil, err
	}
	ids := make([]string, 0, len(shared))
	for _, r := range shared {
		ids = append(ids, r.ResourceId)
	}
	rules, err := store.GetUnificationRulesByIds(ctx, ids)
	if err != nil {
		return nil, err
	}
	owned, err := store.GetUnificationRules(ctx, org.OrgHandle)
	if err != nil {
		return nil, err
	}
	visible, err := schemaService.GetEffectiveProfileSchemaAttributes(ctx, org.OrgHandle)
	if err != nil {
		return nil, err
	}
	propertyIds := make([]string, 0, len(rules))
	for _, rule := range rules {
		propertyIds = append(propertyIds, rule.PropertyId)
	}
	properties, err := schemaService.GetProfileSchemaAttributesByIds(ctx, propertyIds)
	if err != nil {
		return nil, err
	}
	return resolveSharedRuleStates(shared, rules, owned, visible, properties), nil
}

// resolveSharedRuleStates sets the state of each shared rule. The shared rule of the farthest owner
// wins a property. A shared rule is CONFLICTED when an owned rule or a winning shared rule uses its
// property, and INACTIVE_MISSING_ATTRIBUTE when the organization sees no compatible attribute for
// its property. The visible attributes are the effective schema of the organization, and the
// properties are the attributes that the rules use.
func resolveSharedRuleStates(shared []shareModel.SharedResource, rules, owned []model.UnificationRule,
	visible, properties []schemaModel.ProfileSchemaAttribute) []sharedRule {

	byId := make(map[string]model.UnificationRule, len(rules))
	for _, rule := range rules {
		byId[rule.RuleId] = rule
	}
	ordered := append([]shareModel.SharedResource(nil), shared...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].OwnerDepth != ordered[j].OwnerDepth {
			return ordered[i].OwnerDepth > ordered[j].OwnerDepth
		}
		return ordered[i].ResourceId < ordered[j].ResourceId
	})
	usedProperties := make(map[string]bool, len(owned)+len(rules))
	for _, rule := range owned {
		usedProperties[rule.PropertyName] = true
	}
	var result []sharedRule
	for _, r := range ordered {
		rule, ok := byId[r.ResourceId]
		if !ok || rule.OrgHandle != r.OwnerOrgHandle {
			continue
		}
		state := shareModel.StateActive
		switch {
		case usedProperties[rule.PropertyName]:
			state = shareModel.StateConflicted
		case !hasCompatibleAttribute(rule, visible, properties):
			state = model.StateInactiveMissingAttribute
		default:
			usedProperties[rule.PropertyName] = true
		}
		result = append(result, sharedRule{Rule: rule, OwnerDepth: r.OwnerDepth, State: state})
	}
	return result
}

// hasCompatibleAttribute reports whether a visible attribute has the name of the rule property, is
// not complex, and has the value type of the attribute that the rule uses.
func hasCompatibleAttribute(rule model.UnificationRule, visible,
	properties []schemaModel.ProfileSchemaAttribute) bool {

	var candidate *schemaModel.ProfileSchemaAttribute
	for i := range visible {
		if visible[i].AttributeName == rule.PropertyName {
			candidate = &visible[i]
			break
		}
	}
	if candidate == nil || candidate.ValueType == constants.ComplexDataType {
		return false
	}
	for _, property := range properties {
		if property.AttributeId == rule.PropertyId {
			return strings.EqualFold(property.ValueType, candidate.ValueType)
		}
	}
	return true
}

// CreateSharingPolicy shares a rule that the initiator owns.
func (urs *UnificationRuleService) CreateSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, ruleId string,
	req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error) {

	rule, err := urs.GetOwnedUnificationRule(ctx, ruleId, initiator.OrgHandle)
	if err != nil {
		return nil, err
	}
	return sharingService.CreatePolicy(ctx, model.SharingResourceType, rule.RuleId, initiator, req)
}

// ListSharingPolicies returns the sharing policies of a rule that the initiator owns.
func (urs *UnificationRuleService) ListSharingPolicies(ctx context.Context,
	initiator orgModel.Organization, ruleId string) (*shareModel.PolicyList, error) {

	rule, err := urs.GetOwnedUnificationRule(ctx, ruleId, initiator.OrgHandle)
	if err != nil {
		return nil, err
	}
	return sharingService.ListPolicies(ctx, model.SharingResourceType, rule.RuleId, initiator)
}

// GetSharingPolicy returns a sharing policy of a rule that the initiator owns.
func (urs *UnificationRuleService) GetSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, ruleId, policyId string) (*shareModel.PolicyResponse, error) {

	rule, err := urs.GetOwnedUnificationRule(ctx, ruleId, initiator.OrgHandle)
	if err != nil {
		return nil, err
	}
	return sharingService.GetPolicy(ctx, model.SharingResourceType, rule.RuleId, policyId, initiator)
}

// UpdateSharingPolicy replaces a sharing policy of a rule that the initiator owns.
func (urs *UnificationRuleService) UpdateSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, ruleId, policyId string,
	req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error) {

	rule, err := urs.GetOwnedUnificationRule(ctx, ruleId, initiator.OrgHandle)
	if err != nil {
		return nil, err
	}
	return sharingService.UpdatePolicy(ctx, model.SharingResourceType, rule.RuleId, policyId, initiator, req)
}

// DeleteSharingPolicy removes a sharing policy of a rule that the initiator owns.
func (urs *UnificationRuleService) DeleteSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, ruleId, policyId string) error {

	rule, err := urs.GetOwnedUnificationRule(ctx, ruleId, initiator.OrgHandle)
	if err != nil {
		return err
	}
	return sharingService.DeletePolicy(ctx, model.SharingResourceType, rule.RuleId, policyId, initiator)
}

func containsRule(rules []model.UnificationRule, ruleId string) bool {
	for _, rule := range rules {
		if rule.RuleId == ruleId {
			return true
		}
	}
	return false
}

// attributeTypeOptions is the ordered list of all supported attribute types and their allowed
// matching methods, derived entirely from system constants — no database access required.
var attributeTypeOptions = []model.AttributeTypeOption{
	{
		Value: constants.AttributeTypePrimitiveExact,
		Label: constants.AttributeTypeLabelPrimitiveExact,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
		},
	},
	{
		Value: constants.AttributeTypeFuzzyString,
		Label: constants.AttributeTypeLabelFuzzyString,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
			{Value: constants.UnificationMethodFuzzy, Label: constants.UnificationMethodLabelFuzzyGeneral},
		},
	},
	{
		Value: constants.AttributeTypeName,
		Label: constants.AttributeTypeLabelName,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
			{Value: constants.UnificationMethodFuzzy, Label: constants.UnificationMethodLabelFuzzyPhonetic},
		},
	},
	{
		Value: constants.AttributeTypeEmail,
		Label: constants.AttributeTypeLabelEmail,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
			{Value: constants.UnificationMethodFuzzy, Label: constants.UnificationMethodLabelFuzzyGeneral},
		},
	},
	{
		Value: constants.AttributeTypePhone,
		Label: constants.AttributeTypeLabelPhone,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
			{Value: constants.UnificationMethodFuzzy, Label: constants.UnificationMethodLabelFuzzyFormat},
		},
	},
	{
		Value: constants.AttributeTypeLocation,
		Label: constants.AttributeTypeLabelLocation,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
			{Value: constants.UnificationMethodFuzzy, Label: constants.UnificationMethodLabelFuzzyGeneral},
		},
	},
	{
		Value: constants.AttributeTypeDate,
		Label: constants.AttributeTypeLabelDate,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
		},
	},
	{
		Value: constants.AttributeTypeUniqueID,
		Label: constants.AttributeTypeLabelUniqueID,
		AllowedMethods: []model.MethodOption{
			{Value: constants.UnificationMethodDeterministic, Label: constants.UnificationMethodLabelDeterministic},
		},
	},
}

// GetUnificationOptions returns the supported attribute types and their allowed matching
// methods.
//
// Where the server has tolerant matching switched off, fuzzy is not offered for any type.
// This endpoint is what a client builds its method picker from, so it is the one place that
// has to agree with what the create and patch handlers will accept.
func (urs *UnificationRuleService) GetUnificationOptions() model.UnificationOptionsResponse {
	if config.GetCDSRuntime().Config.IdentityResolution.FuzzyMatchingEnabled {
		return model.UnificationOptionsResponse{AttributeTypes: attributeTypeOptions}
	}

	exactOnly := make([]model.AttributeTypeOption, 0, len(attributeTypeOptions))
	for _, option := range attributeTypeOptions {
		methods := make([]model.MethodOption, 0, len(option.AllowedMethods))
		for _, method := range option.AllowedMethods {
			if method.Value != constants.UnificationMethodFuzzy {
				methods = append(methods, method)
			}
		}
		option.AllowedMethods = methods
		exactOnly = append(exactOnly, option)
	}

	return model.UnificationOptionsResponse{AttributeTypes: exactOnly}
}
