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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	appProvider "github.com/wso2/identity-customer-data-service/internal/application/provider"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/organization/suborg"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/identitysource"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	psstr "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

type ProfileSchemaServiceInterface interface {
	GetProfileSchema(ctx context.Context, org orgModel.Ref) (map[string]interface{}, error)
	DeleteProfileSchema(ctx context.Context, org orgModel.Ref) error
	AddProfileSchemaAttributesForScope(ctx context.Context,
		attrs []model.ProfileSchemaAttribute, scope string, org orgModel.Ref) ([]model.ProfileSchemaAttribute, error)
	GetProfileSchemaAttributesByScope(ctx context.Context, org orgModel.Ref, scope string) (interface{}, error)
	GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
		org orgModel.Ref, scope string, filters []string) (interface{}, error)
	DeleteProfileSchemaAttributesByScope(ctx context.Context, org orgModel.Ref, scope string) error
	GetProfileSchemaAttributeById(ctx context.Context, org orgModel.Ref, attributeId string) (model.ProfileSchemaAttribute, error)
	GetProfileSchemaAttributeByName(ctx context.Context,
		attributeName string, org orgModel.Ref) (*model.ProfileSchemaAttribute, error)
	UpdateProfileSchemaAttributeById(ctx context.Context,
		org orgModel.Ref, attributeId string, updates map[string]interface{}, scope string) error
	DeleteProfileSchemaAttributeById(ctx context.Context, org orgModel.Ref, attributeId string) error
	SyncProfileSchema(ctx context.Context, org orgModel.Ref) error
	CreateSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeId string,
		req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error)
	ListSharingPolicies(ctx context.Context, initiator orgModel.Organization, scope,
		attributeId string) (*shareModel.PolicyList, error)
	GetSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeId,
		policyId string) (*shareModel.PolicyResponse, error)
	UpdateSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeId,
		policyId string, req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error)
	DeleteSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeId,
		policyId string) error
}

// ProfileSchemaService is the default implementation of the ProfileSchemaServiceInterface.
type ProfileSchemaService struct{}

// GetProfileSchemaService creates a new instance of UnificationRuleService.
func GetProfileSchemaService() ProfileSchemaServiceInterface {

	return &ProfileSchemaService{}
}

// AddProfileSchemaAttributesForScope adds profile schema attributes to the specific scope.
func (s *ProfileSchemaService) AddProfileSchemaAttributesForScope(ctx context.Context,
	schemaAttributes []model.ProfileSchemaAttribute, scope string, org orgModel.Ref) ([]model.ProfileSchemaAttribute, error) {

	if err := suborg.RejectWrite(ctx, org.ID); err != nil {
		return nil, err
	}
	validAttrs := make([]model.ProfileSchemaAttribute, 0, len(schemaAttributes))
	for _, attr := range schemaAttributes {
		if err, isValid := s.validateSchemaAttribute(ctx, org, attr); isValid {
			// Ensure the scope is valid
			parts := strings.SplitN(attr.AttributeName, ".", 2)
			scopeOfAttr := parts[0]
			if scope != scopeOfAttr {
				errorMsg := fmt.Sprintf("Attribute '%s' does not match the scope '%s'", attr.AttributeName, scope)
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.INVALID_ATTRIBUTE.Code,
					Message:     errors2.INVALID_ATTRIBUTE.Message,
					Description: errorMsg,
				}, http.StatusBadRequest)
				return nil, clientError
			}

			if err := checkSharedNameConflict(ctx, org, attr.AttributeName, attr.ApplicationIdentifier); err != nil {
				return nil, err
			}

			existing, err := psstr.GetProfileSchemaAttributeByName(ctx, org.ID, attr.AttributeName)
			if err != nil {
				return nil, err
			}
			if existing != nil {
				// For application_data attributes, check both attribute name AND application identifier
				if scope == constants.ApplicationData {
					if existing.ApplicationIdentifier == attr.ApplicationIdentifier {
						errorMsg := fmt.Sprintf("Attribute '%s' already exists for application '%s' in org '%s'",
							attr.AttributeName, attr.ApplicationIdentifier, org.ID)
						clientError := errors2.NewClientError(errors2.ErrorMessage{
							Code:        errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Code,
							Message:     errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Message,
							Description: errorMsg,
						}, http.StatusConflict)
						return nil, clientError
					}
				} else {
					// For non-application attributes, duplicate names are not allowed
					errorMsg := fmt.Sprintf("Attribute '%s' already exists for org '%s'", attr.AttributeName, org.ID)
					clientError := errors2.NewClientError(errors2.ErrorMessage{
						Code:        errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Code,
						Message:     errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Message,
						Description: errorMsg,
					}, http.StatusConflict)
					return nil, clientError
				}
			}

			if attr.DisplayName == "" {
				log.GetLogger().Debug(fmt.Sprintf("Display name not provided for attribute: %s. "+
					"Resolving display name from attribute name.", attr.AttributeName))
				attr.DisplayName = utils.ResolveDisplayNameFromAttribute(attr.AttributeName)
			}

			validAttrs = append(validAttrs, attr)
		} else {
			return nil, err
		}
	}

	if err := psstr.AddProfileSchemaAttributesForScope(ctx, validAttrs, scope, org.ID); err != nil {
		return nil, err
	}
	return validAttrs, nil
}

func (s *ProfileSchemaService) validateSchemaAttribute(ctx context.Context, org orgModel.Ref,
	attr model.ProfileSchemaAttribute) (error, bool) {

	parts := strings.Split(attr.AttributeName, ".")
	logger := log.GetLogger()
	if len(parts) < 2 {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: "Attribute name must be in the format <Scope>.<Name>",
		}, http.StatusBadRequest)
		return clientError, false
	}

	if len(parts) > 5 {
		logger.Warn("Attribute exceeds supported nesting. Maximum allowed depth is 4.")
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: "Attribute exceeds the maximum depth of 4.",
		}, http.StatusBadRequest)
		return clientError, false
	}

	scope := parts[0]
	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.INVALID_ATTRIBUTE.Code,
			Message: errors2.INVALID_ATTRIBUTE.Message,
			Description: fmt.Sprintf("Invalid scope: %s. Must be one of identity_attributes, traits, "+
				"application_data", scope),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if scope == constants.ApplicationData {
		if attr.ApplicationIdentifier == "" {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_ATTRIBUTE.Code,
				Message:     errors2.INVALID_ATTRIBUTE.Message,
				Description: "Application identifier is required for application_data scope",
			}, http.StatusBadRequest)
			return clientError, false
		}

		err, validApplicationIdentifier := validateApplicationIdentifierFn(ctx, attr.ApplicationIdentifier, org)
		if err != nil {
			return err, false
		}
		if !validApplicationIdentifier {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_APP_IDENTIFIER.Code,
				Message:     errors2.INVALID_APP_IDENTIFIER.Message,
				Description: fmt.Sprintf("Invalid application identifier: %s", attr.ApplicationIdentifier),
			}, http.StatusBadRequest)
			return clientError, false
		}
	}

	if !constants.AllowedValueTypes[attr.ValueType] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: fmt.Sprintf("Invalid value_type: %s. Must be one of %v", attr.ValueType, keysOf(constants.AllowedValueTypes)),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if len(attr.SubAttributes) > 0 {
		if attr.ValueType != constants.ComplexDataType {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_ATTRIBUTE.Code,
				Message:     errors2.INVALID_ATTRIBUTE.Message,
				Description: fmt.Sprintf("SubAttributes are meant for for value_type: %s", constants.ComplexDataType),
			}, http.StatusBadRequest)
			return clientError, false
		}
	}
	if attr.ValueType == constants.ComplexDataType {
		if len(attr.SubAttributes) == 0 {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
				Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
				Description: fmt.Sprintf("SubAttributes are required for value_type: %s", constants.ComplexDataType),
			}, http.StatusBadRequest)
			return clientError, false
		}

		parentDepth := len(strings.Split(attr.AttributeName, "."))
		for _, subAttr := range attr.SubAttributes {
			subDepth := len(strings.Split(subAttr.AttributeName, "."))

			if !strings.HasPrefix(subAttr.AttributeName, attr.AttributeName+".") {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.INVALID_ATTRIBUTE.Code,
					Message:     errors2.INVALID_ATTRIBUTE.Message,
					Description: fmt.Sprintf("Invalid sub-attribute name: %s. It must start with parent attribute name '%s' followed by a dot and sub-key.", subAttr.AttributeName, attr.AttributeName),
				}, http.StatusBadRequest)
				return clientError, false
			}

			if subDepth != parentDepth+1 {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.INVALID_ATTRIBUTE.Code,
					Message:     errors2.INVALID_ATTRIBUTE.Message,
					Description: fmt.Sprintf("Invalid sub-attribute '%s'. It must be exactly one level deeper than '%s'.", subAttr.AttributeName, attr.AttributeName),
				}, http.StatusBadRequest)
				return clientError, false
			}

			subAttribute, err := s.GetProfileSchemaAttributeById(ctx, org, subAttr.AttributeId)
			if err != nil {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.INVALID_ATTRIBUTE.Code,
					Message:     errors2.INVALID_ATTRIBUTE.Message,
					Description: fmt.Sprintf("Sub-attribute with Id '%s' does not exist.", subAttr.AttributeId),
				}, http.StatusBadRequest)
				return clientError, false
			}
			if subAttribute.AttributeName != subAttr.AttributeName {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.INVALID_ATTRIBUTE.Code,
					Message:     errors2.INVALID_ATTRIBUTE.Message,
					Description: fmt.Sprintf("Sub-attribute name '%s' does not match the expected name.", subAttr.AttributeName),
				}, http.StatusBadRequest)
				return clientError, false
			}
			if subAttribute.ApplicationIdentifier != attr.ApplicationIdentifier {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.INVALID_ATTRIBUTE.Code,
					Message:     errors2.INVALID_ATTRIBUTE.Message,
					Description: fmt.Sprintf("Sub-attribute '%s' must have the same application identifier as the parent attribute '%s'.", subAttr.AttributeName, attr.AttributeName),
				}, http.StatusBadRequest)
				return clientError, false
			}
		}
	}

	if !constants.AllowedMutabilityValues[attr.Mutability] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: fmt.Sprintf("Invalid mutability: '%s'. Must be one of %v", attr.Mutability, keysOf(constants.AllowedMutabilityValues)),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if !constants.AllowedMergeStrategies[attr.MergeStrategy] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: fmt.Sprintf("Invalid merge strategy: '%s'. Must be one of %v", attr.MergeStrategy, keysOf(constants.AllowedMergeStrategies)),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if attr.DisplayName != "" {
		if len(attr.DisplayName) > constants.MaxAttributeDisplayNameLength {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_ATTRIBUTE.Code,
				Message:     errors2.INVALID_ATTRIBUTE.Message,
				Description: fmt.Sprintf("Display name exceeded %d characters", constants.MaxAttributeDisplayNameLength),
			}, http.StatusBadRequest)
			return clientError, false
		}
		if constants.DisplayNameRegex.MatchString(attr.DisplayName) {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_ATTRIBUTE.Code,
				Message:     errors2.INVALID_ATTRIBUTE.Message,
				Description: "Display name contains disallowed characters. It can only contain alphanumerics, periods (.), dashes (-), underscores (_), plus signs (+), and spaces",
			}, http.StatusBadRequest)
			return clientError, false
		}
	}

	return nil, true
}

var validateApplicationIdentifierFn = defaultValidateApplicationIdentifier

// defaultValidateApplicationIdentifier validates the application identifier against the identity server.
func defaultValidateApplicationIdentifier(ctx context.Context, appIdentifier string, org orgModel.Ref) (error, bool) {
	if config.GetCDSRuntime().Config.UsesAppIDIdentifier() {
		ok, err := appProvider.NewApplicationProvider().GetApplicationService().
			ResolveAndRegisterApplication(ctx, appIdentifier, org)
		if err != nil {
			return err, false
		}
		return nil, ok
	}

	identityClient := client.NewIdentityClient(config.GetCDSRuntime().Config)
	res, err := identityClient.FetchApplicationIdentifier(appIdentifier, org.Handle)
	if err != nil {
		return err, false
	}
	return nil, len(res.Applications) == 1
}

func (s *ProfileSchemaService) GetProfileSchemaAttributeById(ctx context.Context,
	org orgModel.Ref, attributeId string) (model.ProfileSchemaAttribute, error) {

	attr, err := psstr.GetProfileSchemaAttributeById(ctx, org.ID, attributeId)
	if err == nil {
		return attr, nil
	}
	if _, isClientError := err.(*errors2.ClientError); !isClientError {
		return attr, err
	}
	shared, sharedErr := getVisibleSharedAttribute(ctx, org, attributeId)
	if sharedErr != nil {
		return attr, sharedErr
	}
	if shared != nil {
		return *shared, nil
	}
	inherited, inheritedErr := getInheritedAttribute(ctx, org, attributeId)
	if inheritedErr != nil {
		return attr, inheritedErr
	}
	if inherited != nil {
		return *inherited, nil
	}
	return attr, err
}

// getOwnedAttribute returns the attribute when the organization owns it. Shared and inherited
// attributes are read-only.
func getOwnedAttribute(ctx context.Context, org orgModel.Ref, attributeId string) (model.ProfileSchemaAttribute, error) {

	attr, err := psstr.GetProfileSchemaAttributeById(ctx, org.ID, attributeId)
	if err == nil {
		return attr, nil
	}
	if _, isClientError := err.(*errors2.ClientError); isClientError {
		if shared, sharedErr := getVisibleSharedAttribute(ctx, org, attributeId); sharedErr == nil && shared != nil {
			return attr, sharingService.ReadOnlyError()
		}
		if inherited, inheritedErr := getInheritedAttribute(ctx, org, attributeId); inheritedErr == nil &&
			inherited != nil {
			return attr, inheritedReadOnlyError()
		}
	}
	return attr, err
}

// getOwnedShareableAttribute returns the attribute when the organization owns it and can share it.
func getOwnedShareableAttribute(ctx context.Context, org orgModel.Ref, scope,
	attributeId string) (model.ProfileSchemaAttribute, error) {

	attr, err := getOwnedAttribute(ctx, org, attributeId)
	if err != nil {
		return attr, err
	}
	if !strings.HasPrefix(attr.AttributeName, scope+".") {
		return attr, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ATTRIBUTE_NOT_FOUND.Code,
			Message:     errors2.ATTRIBUTE_NOT_FOUND.Message,
			Description: fmt.Sprintf("Attribute '%s' is not in the scope '%s'.", attributeId, scope),
		}, http.StatusNotFound)
	}
	return attr, isShareableAttribute(ctx, org, attr)
}

func (s *ProfileSchemaService) GetProfileSchemaAttributeByName(ctx context.Context,
	attributeName string, org orgModel.Ref) (*model.ProfileSchemaAttribute, error) {
	return psstr.GetProfileSchemaAttributeByName(ctx, org.ID, attributeName)
}

// GetProfileSchemaAttributesByScope retrieves profile schema attributes for a specific scope.
func (s *ProfileSchemaService) GetProfileSchemaAttributesByScope(ctx context.Context,
	org orgModel.Ref, scope string) (interface{}, error) {

	schemaAttributes, err := psstr.GetProfileSchemaAttributesByScope(ctx, org.ID, scope)
	if err != nil {
		return nil, err
	}
	schemaAttributes, err = withVisibleAttributes(ctx, org, schemaAttributes, scope)
	if err != nil {
		return nil, err
	}

	// todo: ensure to have the addition also in same format
	if scope == constants.ApplicationData {
		grouped := make(map[string][]model.ProfileSchemaAttribute)
		for _, attr := range schemaAttributes {
			appID := attr.ApplicationIdentifier
			if appID == "" {
				log.GetLogger().Warn(fmt.Sprintf("Missing application identifier for application data: %s", attr.AttributeName))
				continue
			}
			// Keep application_identifier in the attribute as required
			grouped[appID] = append(grouped[appID], attr)
		}
		return grouped, err
	}
	return schemaAttributes, nil
}

func (s *ProfileSchemaService) UpdateProfileSchemaAttributeById(ctx context.Context,
	org orgModel.Ref, attributeId string, updates map[string]interface{}, scope string) error {

	if err := suborg.RejectWrite(ctx, org.ID); err != nil {
		return err
	}
	if len(updates) == 0 {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: "No updates provided for the profile schema attribute",
		}, http.StatusBadRequest)
	}
	attribute, err := getOwnedAttribute(ctx, org, attributeId)
	if err != nil {
		return err
	}
	if newName, ok := updates["attribute_name"].(string); ok && newName != attribute.AttributeName {
		if err := checkSharedNameConflict(ctx, org, newName, attribute.ApplicationIdentifier); err != nil {
			return err
		}
	}
	if attribute.AttributeId == "" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: fmt.Sprintf("Attribute with Id '%s' does not exist", attributeId),
		}, http.StatusNotFound)
	}

	// attribute id cannot be there and also org id. attribute name only can be updated not the scope.
	var canonicalValues []model.CanonicalValue
	if cv, ok := updates["canonical_values"]; ok && cv != nil {
		if cvSlice, ok := cv.([]model.CanonicalValue); ok {
			canonicalValues = cvSlice
		}
	} else {
		canonicalValues = attribute.CanonicalValues // Keep existing canonical values if not updated
	}

	var subAttributes []model.SubAttribute
	if saRaw, ok := updates["sub_attributes"]; ok && saRaw != nil {
		saSlice, ok := saRaw.([]interface{})
		if ok {
			for _, item := range saSlice {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				// Convert map to JSON, then to SubAttribute
				itemBytes, err := json.Marshal(itemMap)
				if err != nil {
					continue
				}

				var subAttr model.SubAttribute
				if err := json.Unmarshal(itemBytes, &subAttr); err == nil {
					subAttributes = append(subAttributes, subAttr)
				}
			}
		}
	} else {
		subAttributes = attribute.SubAttributes // fallback to existing
	}

	var applicationIdentifier string
	if scope == constants.ApplicationData {
		if appID, ok := updates["application_identifier"]; ok && appID != nil {
			if appIDStr, ok := appID.(string); ok && appIDStr != "" {
				applicationIdentifier = appIDStr
			} else {
				// If application_identifier is not provided or is empty, keep the existing one
				applicationIdentifier = attribute.ApplicationIdentifier
			}
		} else {
			applicationIdentifier = attribute.ApplicationIdentifier // Keep existing application identifier if not updated
		}
	}

	multiValued := false // Default to false if not provided
	if mv, ok := updates["multi_valued"]; ok && mv != nil {
		if mvBool, ok := mv.(bool); ok {
			multiValued = mvBool
		} else {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_ATTRIBUTE.Code,
				Message:     "Invalid value for multi_valued",
				Description: "multi_valued must be a boolean",
			}, http.StatusBadRequest)
		}
	} else {
		log.GetLogger().Debug(fmt.Sprintf("multi_valued not provided in patch; defaulting to false for attribute: %s", attributeId))
	}

	requiredFields := []string{"attribute_name", "value_type", "merge_strategy", "mutability"}
	for _, field := range requiredFields {
		v, ok := updates[field].(string)
		if !ok || v == "" {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.INVALID_ATTRIBUTE.Code,
				Message:     errors2.INVALID_ATTRIBUTE.Message,
				Description: fmt.Sprintf("Required field '%s' is missing or empty", field),
			}, http.StatusBadRequest)
		}
	}

	err, isValid := s.validateSchemaAttribute(ctx, org, model.ProfileSchemaAttribute{
		OrgId:                 org.ID,
		AttributeId:           attributeId,
		AttributeName:         updates["attribute_name"].(string),
		ValueType:             updates["value_type"].(string),
		MergeStrategy:         updates["merge_strategy"].(string),
		Mutability:            updates["mutability"].(string),
		MultiValued:           multiValued,
		CanonicalValues:       canonicalValues,
		SubAttributes:         subAttributes,
		ApplicationIdentifier: applicationIdentifier,
	})
	if !isValid {
		if err != nil {
			return err
		}
		// If validation fails, return a bad request error
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: "Invalid updates provided for the profile schema attribute",
		}, http.StatusBadRequest)
	}
	if err := psstr.PatchProfileSchemaAttributeById(ctx, org.ID, attributeId, updates); err != nil {
		return err
	}
	return nil
}

// DeleteProfileSchemaAttributeById deletes a profile schema attribute by its Id.
func (s *ProfileSchemaService) DeleteProfileSchemaAttributeById(ctx context.Context, org orgModel.Ref, attributeId string) error {

	if err := suborg.RejectWrite(ctx, org.ID); err != nil {
		return err
	}
	attribute, err := getOwnedAttribute(ctx, org, attributeId)
	logger := log.GetLogger()
	if err != nil {
		if clientErr, ok := err.(*errors2.ClientError); ok && clientErr.StatusCode == http.StatusForbidden {
			return err
		}
		// If the attribute does not exist, treat delete as a no-op (idempotent).
		if _, ok := err.(*errors2.ClientError); ok {
			logger.Debug(fmt.Sprintf("Attribute with Id '%s' does not exist in organization '%s', skipping delete", attributeId, org.ID))
			return nil
		}
		errMsg := fmt.Sprintf("Error retrieving profile schema attributes for attribute id:%s in organization:%s", attributeId, org.ID)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors2.DELETE_PROFILE_SCHEMA.Message,
			Description: errMsg,
		}, err)
	}

	// If this attribute is referenced as a sub-attribute of a parent, block the deletion.
	// The caller must remove it from the parent attribute first.
	if strings.Count(attribute.AttributeName, ".") >= 2 {
		lastDot := strings.LastIndex(attribute.AttributeName, ".")
		parentName := attribute.AttributeName[:lastDot]
		parent, err := psstr.GetProfileSchemaAttributeByName(ctx, org.ID, parentName)
		if err != nil {
			errMsg := fmt.Sprintf("Error retrieving parent attribute '%s' while validating deletion of '%s'", parentName, attribute.AttributeName)
			logger.Debug(errMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DELETE_PROFILE_SCHEMA.Code,
				Message:     errors2.DELETE_PROFILE_SCHEMA.Message,
				Description: errMsg,
			}, err)
		}
		if parent != nil {
			for _, sa := range parent.SubAttributes {
				if sa.AttributeId == attributeId {
					return errors2.NewClientError(errors2.ErrorMessage{
						Code:        errors2.DELETE_PROFILE_SCHEMA.Code,
						Message:     errors2.DELETE_PROFILE_SCHEMA.Message,
						Description: fmt.Sprintf("Cannot delete attribute '%s' as it is a sub-attribute of '%s'. Remove it from the parent attribute first.", attribute.AttributeName, parentName),
					}, http.StatusBadRequest)
				}
			}
		}
	}

	if err := psstr.DeleteProfileSchemaAttributeById(ctx, org.ID, attributeId); err != nil {
		return err
	}
	// Deleting the attribute deletes its sharing policies.
	return sharingService.DeletePoliciesOfResource(ctx, model.SharingResourceType, attributeId)
}

func (s *ProfileSchemaService) DeleteProfileSchemaAttributesByScope(ctx context.Context, org orgModel.Ref, scope string) error {
	if err := suborg.RejectWrite(ctx, org.ID); err != nil {
		return err
	}
	if err := psstr.DeleteProfileSchemaAttributes(ctx, org.ID, scope); err != nil {
		return err
	}
	return nil
}

// GetProfileSchema retrieves the complete profile schema for the given organization Id.
func (s *ProfileSchemaService) GetProfileSchema(ctx context.Context, org orgModel.Ref) (map[string]interface{}, error) {

	logger := log.GetLogger()
	// Step 1: Flatten core schema fields from model.CoreSchema
	profileSchema := make(map[string]interface{})
	meta := map[string]map[string]string{}
	for attrName, core := range model.CoreSchema {
		if strings.HasPrefix(attrName, "meta.") {
			field := strings.TrimPrefix(attrName, "meta.")
			meta[field] = map[string]string{
				constants.ValueType:  core[constants.ValueType],
				constants.Mutability: core[constants.Mutability],
			}
		} else {
			profileSchema[attrName] = map[string]string{
				constants.ValueType:  core[constants.ValueType],
				constants.Mutability: core[constants.Mutability],
			}
		}
	}

	// Add meta to the profileSchema
	profileSchema["meta"] = meta

	// Step 2: Fetch the effective schema attributes.
	schemaAttributes, err := GetEffectiveProfileSchemaAttributes(ctx, org)
	if err != nil {
		errMsg := fmt.Sprintf("Error retrieving profile schema attributes for organization: %s", org.ID)
		logger.Debug(errMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_SCHEMA.Code,
			Message:     errors2.GET_PROFILE_SCHEMA.Message,
			Description: errMsg,
		}, err)
	}

	// Step 3: Group them by scope
	identityAttrs := make([]model.ProfileSchemaAttribute, 0)
	appDataAttrs := make(map[string][]model.ProfileSchemaAttribute)
	traitsAttrs := make([]model.ProfileSchemaAttribute, 0)

	for _, attr := range schemaAttributes {
		parts := strings.SplitN(attr.AttributeName, ".", 2)
		if len(parts) != 2 {
			logger.Warn(fmt.Sprintf("Invalid attribute name format: %s", attr.AttributeName))
			continue
		}
		scope := parts[0]

		switch scope {
		case constants.IdentityAttributes:
			identityAttrs = append(identityAttrs, attr)
		case constants.ApplicationData:
			if attr.ApplicationIdentifier == "" {
				logger.Warn(fmt.Sprintf("Missing application identifier for application data: %s", attr.AttributeName))
				continue
			}
			appDataAttrs[attr.ApplicationIdentifier] = append(appDataAttrs[attr.ApplicationIdentifier], attr)
		case constants.Traits:
			traitsAttrs = append(traitsAttrs, attr)
		default:
			logger.Warn(fmt.Sprintf("Unknown attribute scope: %s", scope))
		}
	}

	// Step 4: Add scoped arrays
	profileSchema[constants.IdentityAttributes] = identityAttrs
	profileSchema[constants.ApplicationData] = appDataAttrs
	profileSchema[constants.Traits] = traitsAttrs

	return profileSchema, nil
}

func (s *ProfileSchemaService) DeleteProfileSchema(ctx context.Context, org orgModel.Ref) error {
	if err := suborg.RejectWrite(ctx, org.ID); err != nil {
		return err
	}
	if err := psstr.DeleteProfileSchema(ctx, org.ID); err != nil {
		return err
	}
	return nil
}

func keysOf(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func GetProfileSchemaAttributesWithFilter(ctx context.Context,
	org orgModel.Ref, filters []string) ([]model.ProfileSchemaAttribute, error) {

	allAttrs, err := GetEffectiveProfileSchemaAttributes(ctx, org)
	if err != nil {
		return nil, err
	}

	filtered := make([]model.ProfileSchemaAttribute, 0)

	for _, attr := range allAttrs {
		match := true

		for _, f := range filters {
			field, op, val, err := parseFilter(f)
			if err != nil {
				return nil, err
			}

			if !matches(attr, field, op, val) {
				match = false
				break
			}
		}

		if match {
			filtered = append(filtered, attr)
		}
	}

	return filtered, nil
}

func parseFilter(f string) (field, op, value string, err error) {
	parts := strings.SplitN(f, " ", 3)
	if len(parts) != 3 {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_FILTER_FORMAT.Code,
			Message:     errors2.INVALID_FILTER_FORMAT.Message,
			Description: "Filter must be in the format: field op value",
		}, http.StatusBadRequest)
		return "", "", "", clientError
	}
	return parts[0], parts[1], parts[2], nil
}

func matches(attr model.ProfileSchemaAttribute, field, op, val string) bool {
	switch field {
	case "attribute_name":
		switch op {
		case "eq":
			return attr.AttributeName == val
		case "contains":
			return strings.Contains(attr.AttributeName, val)
		}
	case "application_identifier":
		switch op {
		case "eq":
			return attr.ApplicationIdentifier == val
		case "contains":
			return strings.Contains(attr.ApplicationIdentifier, val)
		}
	}
	return false
}

func (s *ProfileSchemaService) SyncProfileSchema(ctx context.Context, org orgModel.Ref) error {

	if source, inherits := identitysource.IsInherited(ctx, org); inherits {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.INHERITED_ATTRIBUTE_READ_ONLY.Code,
			Message: errors2.INHERITED_ATTRIBUTE_READ_ONLY.Message,
			Description: fmt.Sprintf("The organization '%s' inherits the identity attributes from '%s'. "+
				"Sync the identity attributes of '%s'.", org.ID, source.ID, source.ID),
		}, http.StatusBadRequest)
	}
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	claims, err := identityClient.GetProfileSchema(org.Handle)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("failed to fetch profile schema from identity server for organization %s:", org.ID)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.SYNC_PROFILE_SCHEMA.Code,
			Message:     errors2.SYNC_PROFILE_SCHEMA.Message,
			Description: errMsg,
		}, err)
	}

	if len(claims) > 0 {
		claims, err = keepIdentityAttributeIds(ctx, org, claims)
		if err != nil {
			return err
		}
		err := psstr.UpsertIdentityAttributes(ctx, org.ID, claims)
		if err != nil {
			errMsg := fmt.Sprintf("failed to persist profile schema for organization %s:", org.ID)
			logger.Debug(errMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.SYNC_PROFILE_SCHEMA.Code,
				Message:     errors2.SYNC_PROFILE_SCHEMA.Message,
				Description: errMsg,
			}, err)
		}
		logger.Info("Profile schema successfully updated for org: " + org.ID)
	}
	return nil
}

func (s *ProfileSchemaService) GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
	org orgModel.Ref, scope string, filters []string) (interface{}, error) {

	validatedFilters := make([]string, 0, len(filters))
	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE_SCHEMA.Code,
				Message:     errors2.GET_PROFILE_SCHEMA.Message,
				Description: fmt.Sprintf("Invalid filter for schema attributes: `%s`. Filter must be in the format: field operator value", f),
			}, http.StatusBadRequest)
		}

		field, operator, value := parts[0], parts[1], parts[2]

		// Validate operator
		switch operator {
		case "eq", "co", "sw":
		default:
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE_SCHEMA.Code,
				Message:     errors2.GET_PROFILE_SCHEMA.Message,
				Description: fmt.Sprintf("Unsupported operator:` %s`  while filtering schema attributes.", operator),
			}, http.StatusBadRequest)
		}

		// Validate field against allowlist
		if !constants.AllowedFilterFieldsForSchema[field] {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE_SCHEMA.Code,
				Message:     errors2.GET_PROFILE_SCHEMA.Message,
				Description: fmt.Sprintf("Unsupported filter field: `%s` when filtering schema attributes.", field),
			}, http.StatusBadRequest)
		}

		validatedFilters = append(validatedFilters, fmt.Sprintf("%s %s %s", field, operator, value))
	}

	schemaAttributes, err := psstr.GetProfileSchemaAttributesByScopeAndFilter(ctx, org.ID, scope, validatedFilters)
	if err != nil {
		return nil, err
	}
	visible, err := withVisibleAttributes(ctx, org, schemaAttributes, scope)
	if err != nil {
		return nil, err
	}
	schemaAttributes = schemaAttributes[:0]
	for _, attr := range visible {
		// The store filters the owned attributes. The shared and inherited attributes are filtered here.
		fromOtherOrg := attr.Origin == shareModel.OriginShared || attr.Origin == model.OriginInherited
		if !fromOtherOrg || matchesAllFilters(attr, validatedFilters) {
			schemaAttributes = append(schemaAttributes, attr)
		}
	}

	if scope == constants.ApplicationData {
		grouped := make(map[string][]model.ProfileSchemaAttribute)
		for _, attr := range schemaAttributes {
			appID := attr.ApplicationIdentifier
			if appID == "" {
				log.GetLogger().Warn(fmt.Sprintf("Missing application identifier for application data: %s", attr.AttributeName))
				continue
			}
			grouped[appID] = append(grouped[appID], attr)
		}
		return grouped, nil
	}
	return schemaAttributes, nil
}

// matchesAllFilters applies validated "field operator value" filters to a shared attribute.
func matchesAllFilters(attr model.ProfileSchemaAttribute, filters []string) bool {

	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			return false
		}
		var actual string
		switch parts[0] {
		case "attribute_name":
			actual = attr.AttributeName
		case "application_identifier":
			actual = attr.ApplicationIdentifier
		default:
			return false
		}
		switch parts[1] {
		case "eq":
			if actual != parts[2] {
				return false
			}
		case "co":
			if !strings.Contains(actual, parts[2]) {
				return false
			}
		case "sw":
			if !strings.HasPrefix(actual, parts[2]) {
				return false
			}
		}
	}
	return true
}

// CreateSharingPolicy shares an attribute that the initiator owns.
func (s *ProfileSchemaService) CreateSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeId string,
	req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error) {

	attr, err := getOwnedShareableAttribute(ctx, initiator.Ref(), scope, attributeId)
	if err != nil {
		return nil, err
	}
	return sharingService.CreatePolicy(ctx, model.SharingResourceType, attr.AttributeId, initiator, req)
}

// ListSharingPolicies returns the sharing policies of an attribute that the initiator owns.
func (s *ProfileSchemaService) ListSharingPolicies(ctx context.Context,
	initiator orgModel.Organization, scope, attributeId string) (*shareModel.PolicyList, error) {

	attr, err := getOwnedShareableAttribute(ctx, initiator.Ref(), scope, attributeId)
	if err != nil {
		return nil, err
	}
	return sharingService.ListPolicies(ctx, model.SharingResourceType, attr.AttributeId, initiator)
}

// GetSharingPolicy returns a sharing policy of an attribute that the initiator owns.
func (s *ProfileSchemaService) GetSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeId,
	policyId string) (*shareModel.PolicyResponse, error) {

	attr, err := getOwnedShareableAttribute(ctx, initiator.Ref(), scope, attributeId)
	if err != nil {
		return nil, err
	}
	return sharingService.GetPolicy(ctx, model.SharingResourceType, attr.AttributeId, policyId, initiator)
}

// UpdateSharingPolicy replaces a sharing policy of an attribute that the initiator owns.
func (s *ProfileSchemaService) UpdateSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeId, policyId string,
	req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error) {

	attr, err := getOwnedShareableAttribute(ctx, initiator.Ref(), scope, attributeId)
	if err != nil {
		return nil, err
	}
	return sharingService.UpdatePolicy(ctx, model.SharingResourceType, attr.AttributeId, policyId, initiator, req)
}

// DeleteSharingPolicy removes a sharing policy of an attribute that the initiator owns.
func (s *ProfileSchemaService) DeleteSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeId, policyId string) error {

	attr, err := getOwnedShareableAttribute(ctx, initiator.Ref(), scope, attributeId)
	if err != nil {
		return err
	}
	return sharingService.DeletePolicy(ctx, model.SharingResourceType, attr.AttributeId, policyId, initiator)
}

// The effective schema of an organization contains the attributes that it owns, the identity
// attributes that it inherits, and the shared attributes that are active in it.

// GetEffectiveProfileSchemaAttributes returns the effective schema of the organization.
func GetEffectiveProfileSchemaAttributes(ctx context.Context,
	org orgModel.Ref) ([]model.ProfileSchemaAttribute, error) {

	owned, err := psstr.GetProfileSchemaAttributesForOrg(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	return withVisibleAttributes(ctx, org, owned, "")
}

// withVisibleAttributes adds the inherited and the active shared attributes to the owned
// attributes. A non-empty scope limits the result to that scope.
func withVisibleAttributes(ctx context.Context, org orgModel.Ref, owned []model.ProfileSchemaAttribute,
	scope string) ([]model.ProfileSchemaAttribute, error) {

	stored, err := orgService.GetOrganizationById(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return owned, nil
	}
	source, inherits := identitysource.IsInherited(ctx, org)
	result := make([]model.ProfileSchemaAttribute, 0, len(owned))
	for _, attr := range owned {
		attr.Origin = shareModel.OriginOwned
		result = append(result, attr)
	}
	if inherits && (scope == "" || scope == constants.IdentityAttributes) {
		inherited, err := inheritedAttributes(ctx, source)
		if err != nil {
			return nil, err
		}
		result = append(result, inherited...)
	}
	shared, err := activeSharedAttributes(ctx, *stored)
	if err != nil {
		return nil, err
	}
	for _, attr := range shared {
		if scope != "" && attr.Scope != scope {
			continue
		}
		attr.Origin = shareModel.OriginShared
		result = append(result, attr)
	}
	return result, nil
}

// getVisibleSharedAttribute returns the shared attribute when it is active in the organization, or
// nil.
func getVisibleSharedAttribute(ctx context.Context, org orgModel.Ref,
	attributeId string) (*model.ProfileSchemaAttribute, error) {

	shared, err := activeSharedAttributesOf(ctx, org)
	if err != nil {
		return nil, err
	}
	for _, attr := range shared {
		if attr.AttributeId == attributeId {
			attr.Origin = shareModel.OriginShared
			return &attr, nil
		}
	}
	return nil, nil
}

// checkSharedNameConflict rejects a local attribute name that an active shared attribute uses.
// Application data attribute names are scoped to their application.
func checkSharedNameConflict(ctx context.Context, org orgModel.Ref, attributeName, applicationIdentifier string) error {

	shared, err := activeSharedAttributesOf(ctx, org)
	if err != nil {
		return err
	}
	for _, attr := range shared {
		if attr.AttributeName == attributeName && attr.ApplicationIdentifier == applicationIdentifier {
			return sharingService.ConflictError(fmt.Sprintf("The attribute '%s' is shared with this organization "+
				"by a parent organization.", attributeName))
		}
	}
	return nil
}

// inheritedAttributes returns the identity attributes of the source organization.
func inheritedAttributes(ctx context.Context, source orgModel.Ref) ([]model.ProfileSchemaAttribute, error) {

	attrs, err := psstr.GetProfileSchemaAttributesByScope(ctx, source.ID, constants.IdentityAttributes)
	if err != nil {
		return nil, err
	}
	for i := range attrs {
		attrs[i].Origin = model.OriginInherited
	}
	return attrs, nil
}

// getInheritedAttribute returns the inherited identity attribute with the ID, or nil.
func getInheritedAttribute(ctx context.Context, org orgModel.Ref,
	attributeId string) (*model.ProfileSchemaAttribute, error) {

	source, inherits := identitysource.IsInherited(ctx, org)
	if !inherits {
		return nil, nil
	}
	attr, err := psstr.GetProfileSchemaAttributeById(ctx, source.ID, attributeId)
	if err != nil {
		if _, isClientError := err.(*errors2.ClientError); isClientError {
			return nil, nil
		}
		return nil, err
	}
	if attr.Scope != constants.IdentityAttributes {
		return nil, nil
	}
	attr.Origin = model.OriginInherited
	return &attr, nil
}

// inheritedReadOnlyError is the error for a write to an inherited identity attribute.
func inheritedReadOnlyError() error {

	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.INHERITED_ATTRIBUTE_READ_ONLY.Code,
		Message:     errors2.INHERITED_ATTRIBUTE_READ_ONLY.Message,
		Description: errors2.INHERITED_ATTRIBUTE_READ_ONLY.Description,
	}, http.StatusForbidden)
}

// keepIdentityAttributeIds keeps the stored ID of each synced identity attribute, so that the
// unification rules that use it stay valid.
func keepIdentityAttributeIds(ctx context.Context, org orgModel.Ref, incoming []model.ProfileSchemaAttribute) (
	[]model.ProfileSchemaAttribute, error) {

	existing, err := psstr.GetProfileSchemaAttributesByScope(ctx, org.ID, constants.IdentityAttributes)
	if err != nil {
		return nil, err
	}
	idByName := map[string]string{}
	for _, attr := range existing {
		idByName[attr.AttributeName] = attr.AttributeId
	}
	finalIdByName := map[string]string{}
	for i := range incoming {
		incoming[i].OrgId = org.ID
		if id, ok := idByName[incoming[i].AttributeName]; ok {
			incoming[i].AttributeId = id
		}
		finalIdByName[incoming[i].AttributeName] = incoming[i].AttributeId
	}
	for i := range incoming {
		for j := range incoming[i].SubAttributes {
			if id, ok := finalIdByName[incoming[i].SubAttributes[j].AttributeName]; ok {
				incoming[i].SubAttributes[j].AttributeId = id
			}
		}
	}
	return incoming, nil
}

// isShareableAttribute checks that the organization can share the attribute. Only a primary
// organization can share an application data attribute.
func isShareableAttribute(ctx context.Context, org orgModel.Ref, attr model.ProfileSchemaAttribute) error {

	reject := func(description string) error {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.SHARE_BAD_REQUEST.Code,
			Message:     errors2.SHARE_BAD_REQUEST.Message,
			Description: description,
		}, http.StatusBadRequest)
	}
	scope := strings.SplitN(attr.AttributeName, ".", 2)[0]
	switch {
	case scope == constants.IdentityAttributes:
		return reject("Identity attributes are not shared. The identity provider controls them for each organization.")
	case scope == constants.ApplicationData && attr.ApplicationIdentifier == "":
		return reject("The application data attribute has no application identifier.")
	case scope == constants.ApplicationData && !isPrimaryOrg(ctx, org):
		return reject("Only the primary organization can share application data attributes.")
	case attr.ValueType == constants.ComplexDataType:
		return reject("Sharing of complex attributes is not supported.")
	case strings.Count(attr.AttributeName, ".") > 1:
		return reject("Sharing of a sub-attribute is not supported.")
	}
	return nil
}

// isPrimaryOrg reports whether the organization is a primary organization or is not in an
// organization tree.
func isPrimaryOrg(ctx context.Context, org orgModel.Ref) bool {

	stored, err := orgService.GetOrganizationById(ctx, org.ID)
	return err == nil && (stored == nil || stored.IsPrimary())
}

// GetProfileSchemaAttributesByIds returns the attributes with the IDs, in any organization.
// OwnerOrgId of each attribute is set.
func GetProfileSchemaAttributesByIds(ctx context.Context,
	attributeIds []string) ([]model.ProfileSchemaAttribute, error) {
	return psstr.GetProfileSchemaAttributesByIds(ctx, attributeIds)
}

// activeSharedAttributesOf returns the shared attributes that are active in the organization.
func activeSharedAttributesOf(ctx context.Context, org orgModel.Ref) ([]model.ProfileSchemaAttribute, error) {

	stored, err := orgService.GetOrganizationById(ctx, org.ID)
	if err != nil || stored == nil {
		return nil, err
	}
	return activeSharedAttributes(ctx, *stored)
}

// activeSharedAttributes returns the shared attributes that are active in the organization, the
// nearest owner first. OwnerOrgId of each attribute is set.
func activeSharedAttributes(ctx context.Context, org orgModel.Organization) ([]model.ProfileSchemaAttribute, error) {

	shared, err := sharingService.GetSharedResources(ctx, model.SharingResourceType, org)
	if err != nil || len(shared) == 0 {
		return nil, err
	}
	ids := make([]string, 0, len(shared))
	for _, r := range shared {
		ids = append(ids, r.ResourceId)
	}
	attrs, err := psstr.GetProfileSchemaAttributesByIds(ctx, ids)
	if err != nil {
		return nil, err
	}
	owned, err := psstr.GetProfileSchemaAttributesForOrg(ctx, org.OrgId)
	if err != nil {
		return nil, err
	}
	return selectActiveSharedAttributes(shared, attrs, owned), nil
}

// selectActiveSharedAttributes returns the shared attributes that are active, in the order of the
// shared resources. A shared attribute is not active when an owned attribute or the shared
// attribute of a nearer owner has the same key.
func selectActiveSharedAttributes(shared []shareModel.SharedResource, attrs,
	owned []model.ProfileSchemaAttribute) []model.ProfileSchemaAttribute {

	byId := make(map[string]model.ProfileSchemaAttribute, len(attrs))
	for _, attr := range attrs {
		byId[attr.AttributeId] = attr
	}
	usedKeys := make(map[string]bool, len(owned)+len(shared))
	for _, attr := range owned {
		usedKeys[attributeKey(attr)] = true
	}
	var result []model.ProfileSchemaAttribute
	for _, r := range shared {
		attr, ok := byId[r.ResourceId]
		if !ok || attr.OwnerOrgId != r.OwnerOrgId || usedKeys[attributeKey(attr)] {
			continue
		}
		usedKeys[attributeKey(attr)] = true
		result = append(result, attr)
	}
	return result
}

// attributeKey returns the key of an attribute for name conflicts. Application data attribute
// names are scoped to their application.
func attributeKey(attr model.ProfileSchemaAttribute) string {

	if !strings.HasPrefix(attr.AttributeName, constants.ApplicationData+".") || attr.ApplicationIdentifier == "" {
		return attr.AttributeName
	}
	return attr.ApplicationIdentifier + "\x00" + attr.AttributeName
}
