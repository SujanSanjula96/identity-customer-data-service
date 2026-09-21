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
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/provider"
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

	profileSchemaService := provider.NewProfileSchemaProvider().GetProfileSchemaService()
	schemaAttribute, err := profileSchemaService.GetProfileSchemaAttributeByName(ctx, rule.PropertyName, rule.OrgHandle)

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
	return store.AddUnificationRule(ctx, rule, orgHandle)
}

// GetUnificationRules Fetches all resolution rules.
func (urs *UnificationRuleService) GetUnificationRules(ctx context.Context,
	orgHandle string) ([]model.UnificationRule, error) {
	return store.GetUnificationRules(ctx, orgHandle)
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
	return store.PatchUnificationRule(ctx, ruleId, updatedRule)
}

// DeleteUnificationRule Removes a unification rule.
func (urs *UnificationRuleService) DeleteUnificationRule(ctx context.Context, ruleId string) error {

	return store.DeleteUnificationRule(ctx, ruleId)
}
