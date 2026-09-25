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
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
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
	GetProfileSchema(ctx context.Context, orgId string) (map[string]interface{}, error)
	DeleteProfileSchema(ctx context.Context, orgId string) error
	AddProfileSchemaAttributesForScope(ctx context.Context,
		attrs []model.ProfileSchemaAttribute, scope, orgId string) ([]model.ProfileSchemaAttribute, error)
	GetProfileSchemaAttributesByScope(ctx context.Context, orgId, scope string) (interface{}, error)
	GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
		id, scope string, filters []string) (interface{}, error)
	DeleteProfileSchemaAttributesByScope(ctx context.Context, orgId, scope string) error
	GetProfileSchemaAttributeById(ctx context.Context, orgId, attributeId string) (model.ProfileSchemaAttribute, error)
	GetProfileSchemaAttributeByName(ctx context.Context,
		attributeName, orgId string) (*model.ProfileSchemaAttribute, error)
	UpdateProfileSchemaAttributeById(ctx context.Context,
		orgId, attributeId string, updates map[string]interface{}, scope string) error
	DeleteProfileSchemaAttributeById(ctx context.Context, orgId, attributeId string) error
	SyncProfileSchema(ctx context.Context, orgId string) error
}

// ProfileSchemaService is the default implementation of the ProfileSchemaServiceInterface.
type ProfileSchemaService struct{}

// GetProfileSchemaService creates a new instance of UnificationRuleService.
func GetProfileSchemaService() ProfileSchemaServiceInterface {

	return &ProfileSchemaService{}
}

// AddProfileSchemaAttributesForScope adds profile schema attributes to the specific scope.
func (s *ProfileSchemaService) AddProfileSchemaAttributesForScope(ctx context.Context,
	schemaAttributes []model.ProfileSchemaAttribute, scope, orgId string) ([]model.ProfileSchemaAttribute, error) {

	validAttrs := make([]model.ProfileSchemaAttribute, 0, len(schemaAttributes))
	for _, attr := range schemaAttributes {
		if err, isValid := s.validateSchemaAttribute(ctx, attr); isValid {
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

			if err := checkSharedNameConflict(ctx, orgId, attr.AttributeName); err != nil {
				return nil, err
			}

			existing, err := psstr.GetProfileSchemaAttributeByName(ctx, attr.OrgId, attr.AttributeName)
			if err != nil {
				return nil, err
			}
			if existing != nil {
				// For application_data attributes, check both attribute name AND application identifier
				if scope == constants.ApplicationData {
					if existing.ApplicationIdentifier == attr.ApplicationIdentifier {
						errorMsg := fmt.Sprintf("Attribute '%s' already exists for application '%s' in org '%s'",
							attr.AttributeName, attr.ApplicationIdentifier, attr.OrgId)
						clientError := errors2.NewClientError(errors2.ErrorMessage{
							Code:        errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Code,
							Message:     errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Message,
							Description: errorMsg,
						}, http.StatusConflict)
						return nil, clientError
					}
				} else {
					// For non-application attributes, duplicate names are not allowed
					errorMsg := fmt.Sprintf("Attribute '%s' already exists for org '%s'", attr.AttributeName, attr.OrgId)
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

	if err := psstr.AddProfileSchemaAttributesForScope(ctx, validAttrs, scope, orgId); err != nil {
		return nil, err
	}
	recomputeShares(ctx, orgId)
	return validAttrs, nil
}

func (s *ProfileSchemaService) validateSchemaAttribute(ctx context.Context,
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

		err, validApplicationIdentifier := validateApplicationIdentifierFn(ctx, attr.ApplicationIdentifier, attr.OrgId)
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

			subAttribute, err := s.GetProfileSchemaAttributeById(ctx, attr.OrgId, subAttr.AttributeId)
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
func defaultValidateApplicationIdentifier(ctx context.Context, appIdentifier, orgHandle string) (error, bool) {
	if config.GetCDSRuntime().Config.UsesAppIDIdentifier() {
		ok, err := appProvider.NewApplicationProvider().GetApplicationService().
			ResolveAndRegisterApplication(ctx, appIdentifier, orgHandle)
		if err != nil {
			return err, false
		}
		return nil, ok
	}

	identityClient := client.NewIdentityClient(config.GetCDSRuntime().Config)
	res, err := identityClient.FetchApplicationIdentifier(appIdentifier, orgHandle)
	if err != nil {
		return err, false
	}
	return nil, len(res.Applications) == 1
}

func (s *ProfileSchemaService) GetProfileSchemaAttributeById(ctx context.Context,
	orgId, attributeId string) (model.ProfileSchemaAttribute, error) {

	attr, err := psstr.GetProfileSchemaAttributeById(ctx, orgId, attributeId)
	if err == nil {
		return attr, nil
	}
	if _, isClientError := err.(*errors2.ClientError); !isClientError {
		return attr, err
	}
	shared, sharedErr := getVisibleSharedAttribute(ctx, orgId, attributeId)
	if sharedErr != nil {
		return attr, sharedErr
	}
	if shared != nil {
		return *shared, nil
	}
	return attr, err
}

// getOwnedAttribute returns the attribute when the org owns it. A write to a shared attribute
// from a target org returns 403.
func getOwnedAttribute(ctx context.Context, orgId, attributeId string) (model.ProfileSchemaAttribute, error) {

	attr, err := psstr.GetProfileSchemaAttributeById(ctx, orgId, attributeId)
	if err == nil {
		return attr, nil
	}
	if _, isClientError := err.(*errors2.ClientError); isClientError {
		if shared, sharedErr := getVisibleSharedAttribute(ctx, orgId, attributeId); sharedErr == nil && shared != nil {
			return attr, sharingService.ReadOnlyError()
		}
	}
	return attr, err
}

// GetOwnedShareableAttribute returns the attribute when the org owns it and it can be shared in
// this phase.
func GetOwnedShareableAttribute(ctx context.Context, orgId, scope,
	attributeId string) (model.ProfileSchemaAttribute, error) {

	attr, err := getOwnedAttribute(ctx, orgId, attributeId)
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
	return attr, isShareableAttribute(attr)
}

func (s *ProfileSchemaService) GetProfileSchemaAttributeByName(ctx context.Context,
	attributeName, orgId string) (*model.ProfileSchemaAttribute, error) {
	return psstr.GetProfileSchemaAttributeByName(ctx, orgId, attributeName)
}

// GetProfileSchemaAttributesByScope retrieves profile schema attributes for a specific scope.
func (s *ProfileSchemaService) GetProfileSchemaAttributesByScope(ctx context.Context,
	orgId, scope string) (interface{}, error) {

	schemaAttributes, err := psstr.GetProfileSchemaAttributesByScope(ctx, orgId, scope)
	if err != nil {
		return nil, err
	}
	schemaAttributes, err = withSharedAttributes(ctx, orgId, schemaAttributes, scope)
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
	orgId, attributeId string, updates map[string]interface{}, scope string) error {

	if len(updates) == 0 {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: "No updates provided for the profile schema attribute",
		}, http.StatusBadRequest)
	}
	attribute, err := getOwnedAttribute(ctx, orgId, attributeId)
	if err != nil {
		return err
	}
	if newName, ok := updates["attribute_name"].(string); ok && newName != attribute.AttributeName {
		if err := checkSharedNameConflict(ctx, orgId, newName); err != nil {
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

	err, isValid := s.validateSchemaAttribute(ctx, model.ProfileSchemaAttribute{
		OrgId:                 orgId,
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
	if err := psstr.PatchProfileSchemaAttributeById(ctx, orgId, attributeId, updates); err != nil {
		return err
	}
	recomputeShares(ctx, orgId)
	return nil
}

// DeleteProfileSchemaAttributeById deletes a profile schema attribute by its Id.
func (s *ProfileSchemaService) DeleteProfileSchemaAttributeById(ctx context.Context, orgId, attributeId string) error {

	attribute, err := getOwnedAttribute(ctx, orgId, attributeId)
	logger := log.GetLogger()
	if err != nil {
		if clientErr, ok := err.(*errors2.ClientError); ok && clientErr.StatusCode == http.StatusForbidden {
			return err
		}
		// If the attribute does not exist, treat delete as a no-op (idempotent).
		if _, ok := err.(*errors2.ClientError); ok {
			logger.Debug(fmt.Sprintf("Attribute with Id '%s' does not exist in organization '%s', skipping delete", attributeId, orgId))
			return nil
		}
		errMsg := fmt.Sprintf("Error retrieving profile schema attributes for attribute id:%s in organization:%s", attributeId, orgId)
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
		parent, err := psstr.GetProfileSchemaAttributeByName(ctx, orgId, parentName)
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

	if err := psstr.DeleteProfileSchemaAttributeById(ctx, orgId, attributeId); err != nil {
		return err
	}
	// The owner deletes the attribute, so CDS deletes all its policies. The values in the target
	// orgs stay stored but hidden.
	if org, orgErr := orgStore.GetOrganizationByHandle(ctx, orgId); orgErr == nil && org != nil {
		if err := sharingService.DeletePoliciesOfResource(ctx, shareModel.ResourceSchemaAttribute, attributeId,
			org.RootOrgId); err != nil {
			return err
		}
	}
	recomputeShares(ctx, orgId)
	return nil
}

func (s *ProfileSchemaService) DeleteProfileSchemaAttributesByScope(ctx context.Context, orgId, scope string) error {
	if err := psstr.DeleteProfileSchemaAttributes(ctx, orgId, scope); err != nil {
		return err
	}
	recomputeShares(ctx, orgId)
	return nil
}

// GetProfileSchema retrieves the complete profile schema for the given organization Id.
func (s *ProfileSchemaService) GetProfileSchema(ctx context.Context, orgId string) (map[string]interface{}, error) {

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

	// Step 2: Fetch the effective schema attributes: owned, and shared attributes that are active
	schemaAttributes, err := GetEffectiveProfileSchemaAttributes(ctx, orgId)
	if err != nil {
		errMsg := fmt.Sprintf("Error retrieving profile schema attributes for organization: %s", orgId)
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

func (s *ProfileSchemaService) DeleteProfileSchema(ctx context.Context, orgId string) error {
	if err := psstr.DeleteProfileSchema(ctx, orgId); err != nil {
		return err
	}
	recomputeShares(ctx, orgId)
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
	orgId string, filters []string) ([]model.ProfileSchemaAttribute, error) {

	allAttrs, err := GetEffectiveProfileSchemaAttributes(ctx, orgId)
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

func (s *ProfileSchemaService) SyncProfileSchema(ctx context.Context, orgHandle string) error {

	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	// A sub org can read its identity attributes from another org, for example its root in IS.
	claims, err := identityClient.GetProfileSchema(identityAttributeSourceHandle(ctx, orgHandle))
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("failed to fetch profile schema from identity server for organization %s:", orgHandle)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.SYNC_PROFILE_SCHEMA.Code,
			Message:     errors2.SYNC_PROFILE_SCHEMA.Message,
			Description: errMsg,
		}, err)
	}

	if len(claims) > 0 {
		claims, err = keepIdentityAttributeIds(ctx, orgHandle, claims)
		if err != nil {
			return err
		}
		err := psstr.UpsertIdentityAttributes(ctx, orgHandle, claims)
		if err != nil {
			errMsg := fmt.Sprintf("failed to persist profile schema for organization %s:", orgHandle)
			logger.Debug(errMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.SYNC_PROFILE_SCHEMA.Code,
				Message:     errors2.SYNC_PROFILE_SCHEMA.Message,
				Description: errMsg,
			}, err)
		}
		logger.Info("Profile schema successfully updated for org: " + orgHandle)
		recomputeShares(ctx, orgHandle)
	}
	return nil
}

func (s *ProfileSchemaService) GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
	orgId, scope string, filters []string) (interface{}, error) {

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

	schemaAttributes, err := psstr.GetProfileSchemaAttributesByScopeAndFilter(ctx, orgId, scope, validatedFilters)
	if err != nil {
		return nil, err
	}
	withShared, err := withSharedAttributes(ctx, orgId, schemaAttributes, scope)
	if err != nil {
		return nil, err
	}
	schemaAttributes = schemaAttributes[:0]
	for _, attr := range withShared {
		if attr.Origin != shareModel.OriginShared || matchesAllFilters(attr, validatedFilters) {
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
