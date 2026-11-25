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

// Package service implements the business logic for the profile schema and its sharing policies.
package service

import (
	"context"
	"errors"
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

// ProfileSchemaServiceInterface defines the operations on the profile schema and its sharing policies.
type ProfileSchemaServiceInterface interface {
	GetProfileSchema(ctx context.Context, orgID string) (map[string]interface{}, error)
	DeleteProfileSchema(ctx context.Context, orgID string) error
	AddProfileSchemaAttributesForScope(ctx context.Context,
		attrs []model.ProfileSchemaAttribute, scope string, orgID string) ([]model.ProfileSchemaAttribute, error)
	GetProfileSchemaAttributesByScope(ctx context.Context, orgID string, scope string) (interface{}, error)
	GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
		orgID string, scope string, filters []string) (interface{}, error)
	DeleteProfileSchemaAttributesByScope(ctx context.Context, orgID string, scope string) error
	GetProfileSchemaAttributeByID(
		ctx context.Context,
		orgID string,
		attributeID string,
	) (model.ProfileSchemaAttribute, error)
	GetProfileSchemaAttributeByName(ctx context.Context,
		attributeName string, orgID string) (*model.ProfileSchemaAttribute, error)
	UpdateProfileSchemaAttributeByID(ctx context.Context,
		orgID string, attributeID string, update model.ProfileSchemaAttributeUpdate, scope string) error
	DeleteProfileSchemaAttributeByID(ctx context.Context, orgID string, attributeID string) error
	SyncProfileSchema(ctx context.Context, orgID string) error
	CreateSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeID string,
		req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error)
	ListSharingPolicies(ctx context.Context, initiator orgModel.Organization, scope,
		attributeID string) (*shareModel.PolicyList, error)
	GetSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeID,
		policyID string) (*shareModel.PolicyResponse, error)
	UpdateSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeID,
		policyID string, req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error)
	DeleteSharingPolicy(ctx context.Context, initiator orgModel.Organization, scope, attributeID,
		policyID string) error
}

// ProfileSchemaService is the default implementation of the ProfileSchemaServiceInterface.
type ProfileSchemaService struct{}

// GetProfileSchemaService creates a new instance of UnificationRuleService.
func GetProfileSchemaService() ProfileSchemaServiceInterface {
	return &ProfileSchemaService{}
}

// AddProfileSchemaAttributesForScope adds profile schema attributes to the specific scope.
func (s *ProfileSchemaService) AddProfileSchemaAttributesForScope(
	ctx context.Context,
	schemaAttributes []model.ProfileSchemaAttribute,
	scope string,
	orgID string,
) ([]model.ProfileSchemaAttribute, error) {
	if err := suborg.RejectWrite(ctx, orgID); err != nil {
		return nil, err
	}
	validAttrs := make([]model.ProfileSchemaAttribute, 0, len(schemaAttributes))
	for _, attr := range schemaAttributes {
		if err, isValid := s.validateSchemaAttribute(ctx, orgID, attr); isValid {
			// Ensure the scope is valid
			parts := strings.SplitN(attr.AttributeName, ".", 2)
			scopeOfAttr := parts[0]
			if scope != scopeOfAttr {
				errorMsg := fmt.Sprintf("Attribute '%s' does not match the scope '%s'", attr.AttributeName, scope)
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.InvalidAttribute.Code,
					Message:     errors2.InvalidAttribute.Message,
					Description: errorMsg,
				}, http.StatusBadRequest)
				return nil, clientError
			}

			if err := checkSharedNameConflict(ctx, orgID, attr.AttributeName, attr.ApplicationIdentifier); err != nil {
				return nil, err
			}

			existing, err := psstr.GetProfileSchemaAttributeByName(ctx, orgID, attr.AttributeName)
			if err != nil {
				return nil, err
			}
			if existing != nil {
				// For application_data attributes, check both attribute name AND application identifier
				if scope == constants.ApplicationData {
					if existing.ApplicationIdentifier == attr.ApplicationIdentifier {
						errorMsg := fmt.Sprintf("Attribute '%s' already exists for application '%s' in org '%s'",
							attr.AttributeName, attr.ApplicationIdentifier, orgID)
						clientError := errors2.NewClientError(errors2.ErrorMessage{
							Code:        errors2.SchemaAttributeAlreadyExists.Code,
							Message:     errors2.SchemaAttributeAlreadyExists.Message,
							Description: errorMsg,
						}, http.StatusConflict)
						return nil, clientError
					}
				} else {
					// For non-application attributes, duplicate names are not allowed
					errorMsg := fmt.Sprintf("Attribute '%s' already exists for org '%s'", attr.AttributeName, orgID)
					clientError := errors2.NewClientError(errors2.ErrorMessage{
						Code:        errors2.SchemaAttributeAlreadyExists.Code,
						Message:     errors2.SchemaAttributeAlreadyExists.Message,
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

	if err := psstr.AddProfileSchemaAttributesForScope(ctx, validAttrs, scope, orgID); err != nil {
		return nil, err
	}
	return validAttrs, nil
}

func (s *ProfileSchemaService) validateSchemaAttribute(ctx context.Context, orgID string,
	attr model.ProfileSchemaAttribute) (error, bool) {
	parts := strings.Split(attr.AttributeName, ".")
	logger := log.GetLogger()
	if len(parts) < 2 {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.InvalidAttribute.Code,
			Message:     errors2.InvalidAttribute.Message,
			Description: "Attribute name must be in the format <Scope>.<Name>",
		}, http.StatusBadRequest)
		return clientError, false
	}

	if len(parts) > 5 {
		logger.Warn("Attribute exceeds supported nesting. Maximum allowed depth is 4.")
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.InvalidAttribute.Code,
			Message:     errors2.InvalidAttribute.Message,
			Description: "Attribute exceeds the maximum depth of 4.",
		}, http.StatusBadRequest)
		return clientError, false
	}

	scope := parts[0]
	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.InvalidAttribute.Code,
			Message: errors2.InvalidAttribute.Message,
			Description: fmt.Sprintf("Invalid scope: %s. Must be one of identity_attributes, traits, "+
				"application_data", scope),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if scope == constants.ApplicationData {
		if attr.ApplicationIdentifier == "" {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.InvalidAttribute.Code,
				Message:     errors2.InvalidAttribute.Message,
				Description: "Application identifier is required for application_data scope",
			}, http.StatusBadRequest)
			return clientError, false
		}

		err, validApplicationIdentifier := validateApplicationIdentifierFn(ctx, attr.ApplicationIdentifier, orgID)
		if err != nil {
			return err, false
		}
		if !validApplicationIdentifier {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.InvalidAppIdentifier.Code,
				Message:     errors2.InvalidAppIdentifier.Message,
				Description: fmt.Sprintf("Invalid application identifier: %s", attr.ApplicationIdentifier),
			}, http.StatusBadRequest)
			return clientError, false
		}
	}

	if !constants.AllowedValueTypes[attr.ValueType] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.InvalidAttribute.Code,
			Message: errors2.InvalidAttribute.Message,
			Description: fmt.Sprintf(
				"Invalid value_type: %s. Must be one of %v",
				attr.ValueType,
				keysOf(constants.AllowedValueTypes),
			),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if len(attr.SubAttributes) > 0 {
		if attr.ValueType != constants.ComplexDataType {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.InvalidAttribute.Code,
				Message:     errors2.InvalidAttribute.Message,
				Description: fmt.Sprintf("SubAttributes are meant for for value_type: %s", constants.ComplexDataType),
			}, http.StatusBadRequest)
			return clientError, false
		}
	}
	if attr.ValueType == constants.ComplexDataType {
		if len(attr.SubAttributes) == 0 {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.ProfileSchemaAddBadRequest.Code,
				Message:     errors2.ProfileSchemaAddBadRequest.Message,
				Description: fmt.Sprintf("SubAttributes are required for value_type: %s", constants.ComplexDataType),
			}, http.StatusBadRequest)
			return clientError, false
		}

		parentDepth := len(strings.Split(attr.AttributeName, "."))
		for _, subAttr := range attr.SubAttributes {
			subDepth := len(strings.Split(subAttr.AttributeName, "."))

			if !strings.HasPrefix(subAttr.AttributeName, attr.AttributeName+".") {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:    errors2.InvalidAttribute.Code,
					Message: errors2.InvalidAttribute.Message,
					Description: fmt.Sprintf(
						"Invalid sub-attribute name: %s. It must start with parent attribute name '%s' "+
							"followed by a dot and sub-key.",
						subAttr.AttributeName,
						attr.AttributeName,
					),
				}, http.StatusBadRequest)
				return clientError, false
			}

			if subDepth != parentDepth+1 {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:    errors2.InvalidAttribute.Code,
					Message: errors2.InvalidAttribute.Message,
					Description: fmt.Sprintf(
						"Invalid sub-attribute '%s'. It must be exactly one level deeper than '%s'.",
						subAttr.AttributeName,
						attr.AttributeName,
					),
				}, http.StatusBadRequest)
				return clientError, false
			}

			subAttribute, err := s.GetProfileSchemaAttributeByID(ctx, orgID, subAttr.AttributeID)
			if err != nil {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.InvalidAttribute.Code,
					Message:     errors2.InvalidAttribute.Message,
					Description: fmt.Sprintf("Sub-attribute with Id '%s' does not exist.", subAttr.AttributeID),
				}, http.StatusBadRequest)
				return clientError, false
			}
			if subAttribute.AttributeName != subAttr.AttributeName {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:    errors2.InvalidAttribute.Code,
					Message: errors2.InvalidAttribute.Message,
					Description: fmt.Sprintf(
						"Sub-attribute name '%s' does not match the expected name.",
						subAttr.AttributeName,
					),
				}, http.StatusBadRequest)
				return clientError, false
			}
			if subAttribute.ApplicationIdentifier != attr.ApplicationIdentifier {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:    errors2.InvalidAttribute.Code,
					Message: errors2.InvalidAttribute.Message,
					Description: fmt.Sprintf(
						"Sub-attribute '%s' must have the same application identifier as the parent attribute '%s'.",
						subAttr.AttributeName,
						attr.AttributeName,
					),
				}, http.StatusBadRequest)
				return clientError, false
			}
		}
	}

	if !constants.AllowedMutabilityValues[attr.Mutability] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.InvalidAttribute.Code,
			Message: errors2.InvalidAttribute.Message,
			Description: fmt.Sprintf(
				"Invalid mutability: '%s'. Must be one of %v",
				attr.Mutability,
				keysOf(constants.AllowedMutabilityValues),
			),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if !constants.AllowedMergeStrategies[attr.MergeStrategy] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.InvalidAttribute.Code,
			Message: errors2.InvalidAttribute.Message,
			Description: fmt.Sprintf(
				"Invalid merge strategy: '%s'. Must be one of %v",
				attr.MergeStrategy,
				keysOf(constants.AllowedMergeStrategies),
			),
		}, http.StatusBadRequest)
		return clientError, false
	}

	if attr.DisplayName != "" {
		if len(attr.DisplayName) > constants.MaxAttributeDisplayNameLength {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:    errors2.InvalidAttribute.Code,
				Message: errors2.InvalidAttribute.Message,
				Description: fmt.Sprintf(
					"Display name exceeded %d characters",
					constants.MaxAttributeDisplayNameLength,
				),
			}, http.StatusBadRequest)
			return clientError, false
		}
		if constants.DisplayNameRegex.MatchString(attr.DisplayName) {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:    errors2.InvalidAttribute.Code,
				Message: errors2.InvalidAttribute.Message,
				Description: "Display name contains disallowed characters. It can only contain alphanumerics, " +
					"periods (.), dashes (-), underscores (_), plus signs (+), and spaces",
			}, http.StatusBadRequest)
			return clientError, false
		}
	}

	return nil, true
}

var validateApplicationIdentifierFn = defaultValidateApplicationIdentifier

// defaultValidateApplicationIdentifier validates the application identifier against the identity server.
func defaultValidateApplicationIdentifier(ctx context.Context, appIdentifier string, orgID string) (error, bool) {
	if config.GetCDSRuntime().Config.UsesAppIDIdentifier() {
		ok, err := appProvider.NewApplicationProvider().GetApplicationService().
			ResolveAndRegisterApplication(ctx, appIdentifier, orgID)
		if err != nil {
			return err, false
		}
		return nil, ok
	}

	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return err, false
	}
	identityClient := client.NewIdentityClient(config.GetCDSRuntime().Config)
	res, err := identityClient.FetchApplicationIdentifier(appIdentifier, orgHandle)
	if err != nil {
		return err, false
	}
	return nil, len(res.Applications) == 1
}

// GetProfileSchemaAttributeByID returns the schema attribute with the given ID, whether the organization owns
// it, it is shared with the organization, or the organization inherits it.
func (s *ProfileSchemaService) GetProfileSchemaAttributeByID(ctx context.Context,
	orgID string, attributeID string) (model.ProfileSchemaAttribute, error) {
	attr, err := psstr.GetProfileSchemaAttributeByID(ctx, orgID, attributeID)
	if err == nil {
		return attr, nil
	}
	if clientErr := new(errors2.ClientError); !errors.As(err, &clientErr) {
		return attr, err
	}
	shared, sharedErr := getVisibleSharedAttribute(ctx, orgID, attributeID)
	if sharedErr != nil {
		return attr, sharedErr
	}
	if shared != nil {
		return *shared, nil
	}
	inherited, inheritedErr := getInheritedAttribute(ctx, orgID, attributeID)
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
func getOwnedAttribute(ctx context.Context, orgID string, attributeID string) (model.ProfileSchemaAttribute, error) {
	attr, err := psstr.GetProfileSchemaAttributeByID(ctx, orgID, attributeID)
	if err == nil {
		return attr, nil
	}
	if clientErr := new(errors2.ClientError); errors.As(err, &clientErr) {
		if shared, sharedErr := getVisibleSharedAttribute(ctx, orgID, attributeID); sharedErr == nil && shared != nil {
			return attr, sharingService.ReadOnlyError()
		}
		if inherited, inheritedErr := getInheritedAttribute(ctx, orgID, attributeID); inheritedErr == nil &&
			inherited != nil {
			return attr, inheritedReadOnlyError()
		}
	}
	return attr, err
}

// getOwnedShareableAttribute returns the attribute when the organization owns it and can share it.
func getOwnedShareableAttribute(ctx context.Context, orgID string, scope,
	attributeID string) (model.ProfileSchemaAttribute, error) {
	attr, err := getOwnedAttribute(ctx, orgID, attributeID)
	if err != nil {
		return attr, err
	}
	if !strings.HasPrefix(attr.AttributeName, scope+".") {
		return attr, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.AttributeNotFound.Code,
			Message:     errors2.AttributeNotFound.Message,
			Description: fmt.Sprintf("Attribute '%s' is not in the scope '%s'.", attributeID, scope),
		}, http.StatusNotFound)
	}
	return attr, isShareableAttribute(ctx, orgID, attr)
}

// GetProfileSchemaAttributeByName returns the organization's own schema attribute with the given name.
func (s *ProfileSchemaService) GetProfileSchemaAttributeByName(ctx context.Context,
	attributeName string, orgID string) (*model.ProfileSchemaAttribute, error) {
	return psstr.GetProfileSchemaAttributeByName(ctx, orgID, attributeName)
}

// GetProfileSchemaAttributesByScope retrieves profile schema attributes for a specific scope.
func (s *ProfileSchemaService) GetProfileSchemaAttributesByScope(ctx context.Context,
	orgID string, scope string) (interface{}, error) {
	schemaAttributes, err := psstr.GetProfileSchemaAttributesByScope(ctx, orgID, scope)
	if err != nil {
		return nil, err
	}
	schemaAttributes, err = withVisibleAttributes(ctx, orgID, schemaAttributes, scope)
	if err != nil {
		return nil, err
	}

	// todo: ensure to have the addition also in same format
	if scope == constants.ApplicationData {
		grouped := make(map[string][]model.ProfileSchemaAttribute)
		for _, attr := range schemaAttributes {
			appID := attr.ApplicationIdentifier
			if appID == "" {
				log.GetLogger().
					Warn(fmt.Sprintf("Missing application identifier for application data: %s", attr.AttributeName))
				continue
			}
			// Keep application_identifier in the attribute as required
			grouped[appID] = append(grouped[appID], attr)
		}
		return grouped, err
	}
	return schemaAttributes, nil
}

// UpdateProfileSchemaAttributeByID applies the update to a schema attribute the organization owns. The
// attribute must be in the scope. A field that the update does not hold keeps its stored value. Sub
// organizations cannot update attributes.
func (s *ProfileSchemaService) UpdateProfileSchemaAttributeByID(ctx context.Context,
	orgID string, attributeID string, update model.ProfileSchemaAttributeUpdate, scope string) error {
	if err := suborg.RejectWrite(ctx, orgID); err != nil {
		return err
	}
	if update == (model.ProfileSchemaAttributeUpdate{}) {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.InvalidAttribute.Code,
			Message:     errors2.InvalidAttribute.Message,
			Description: "No updates provided for the profile schema attribute",
		}, http.StatusBadRequest)
	}
	attribute, err := getOwnedAttribute(ctx, orgID, attributeID)
	if err != nil {
		return err
	}
	if attribute.AttributeID == "" || attribute.Scope != scope {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.InvalidAttribute.Code,
			Message:     errors2.InvalidAttribute.Message,
			Description: fmt.Sprintf("Attribute with Id '%s' does not exist in scope '%s'", attributeID, scope),
		}, http.StatusNotFound)
	}

	required := []struct {
		field string
		value *string
	}{
		{"attribute_name", update.AttributeName},
		{"value_type", update.ValueType},
		{"merge_strategy", update.MergeStrategy},
		{"mutability", update.Mutability},
	}
	for _, r := range required {
		if r.value == nil || *r.value == "" {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.InvalidAttribute.Code,
				Message:     errors2.InvalidAttribute.Message,
				Description: fmt.Sprintf("Required field '%s' is missing or empty", r.field),
			}, http.StatusBadRequest)
		}
	}
	if update.ApplicationIdentifier != nil && *update.ApplicationIdentifier != "" &&
		attribute.Scope != constants.ApplicationData {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.InvalidAttribute.Code,
			Message:     errors2.InvalidAttribute.Message,
			Description: "application_identifier can be updated only for application_data attributes",
		}, http.StatusBadRequest)
	}

	updated := attribute
	updated.AttributeName = *update.AttributeName
	updated.ValueType = *update.ValueType
	updated.MergeStrategy = *update.MergeStrategy
	updated.Mutability = *update.Mutability
	if update.DisplayName != nil {
		updated.DisplayName = *update.DisplayName
	}
	if update.MultiValued != nil {
		updated.MultiValued = *update.MultiValued
	}
	if update.CanonicalValues != nil {
		updated.CanonicalValues = *update.CanonicalValues
	}
	if update.SubAttributes != nil {
		updated.SubAttributes = *update.SubAttributes
	}
	if update.ApplicationIdentifier != nil && *update.ApplicationIdentifier != "" {
		updated.ApplicationIdentifier = *update.ApplicationIdentifier
	}

	if updated.AttributeName != attribute.AttributeName {
		err := checkSharedNameConflict(ctx, orgID, updated.AttributeName, attribute.ApplicationIdentifier)
		if err != nil {
			return err
		}
	}
	err, isValid := s.validateSchemaAttribute(ctx, orgID, updated)
	if !isValid {
		if err != nil {
			return err
		}
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.InvalidAttribute.Code,
			Message:     errors2.InvalidAttribute.Message,
			Description: "Invalid updates provided for the profile schema attribute",
		}, http.StatusBadRequest)
	}
	return psstr.PatchProfileSchemaAttributesForScope(ctx, orgID, attribute.Scope,
		[]model.ProfileSchemaAttribute{updated})
}

// DeleteProfileSchemaAttributeByID deletes a profile schema attribute by its ID.
func (s *ProfileSchemaService) DeleteProfileSchemaAttributeByID(
	ctx context.Context,
	orgID string,
	attributeID string,
) error {
	if err := suborg.RejectWrite(ctx, orgID); err != nil {
		return err
	}
	attribute, err := getOwnedAttribute(ctx, orgID, attributeID)
	logger := log.GetLogger()
	if err != nil {
		if clientErr := new(errors2.ClientError); errors.As(err, &clientErr) &&
			clientErr.StatusCode == http.StatusForbidden {
			return err
		}
		// If the attribute does not exist, treat delete as a no-op (idempotent).
		if clientErr := new(errors2.ClientError); errors.As(err, &clientErr) {
			logger.Debug(
				fmt.Sprintf(
					"Attribute with Id '%s' does not exist in organization '%s', skipping delete",
					attributeID,
					orgID,
				),
			)
			return nil
		}
		errMsg := fmt.Sprintf(
			"Error retrieving profile schema attributes for attribute id:%s in organization:%s",
			attributeID,
			orgID,
		)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfileSchema.Code,
			Message:     errors2.DeleteProfileSchema.Message,
			Description: errMsg,
		}, err)
	}

	// If this attribute is referenced as a sub-attribute of a parent, block the deletion.
	// The caller must remove it from the parent attribute first.
	if strings.Count(attribute.AttributeName, ".") >= 2 {
		lastDot := strings.LastIndex(attribute.AttributeName, ".")
		parentName := attribute.AttributeName[:lastDot]
		parent, err := psstr.GetProfileSchemaAttributeByName(ctx, orgID, parentName)
		if err != nil {
			errMsg := fmt.Sprintf(
				"Error retrieving parent attribute '%s' while validating deletion of '%s'",
				parentName,
				attribute.AttributeName,
			)
			logger.Debug(errMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DeleteProfileSchema.Code,
				Message:     errors2.DeleteProfileSchema.Message,
				Description: errMsg,
			}, err)
		}
		if parent != nil {
			for _, sa := range parent.SubAttributes {
				if sa.AttributeID == attributeID {
					return errors2.NewClientError(errors2.ErrorMessage{
						Code:    errors2.DeleteProfileSchema.Code,
						Message: errors2.DeleteProfileSchema.Message,
						Description: fmt.Sprintf(
							"Cannot delete attribute '%s' as it is a sub-attribute of '%s'. "+
								"Remove it from the parent attribute first.",
							attribute.AttributeName,
							parentName,
						),
					}, http.StatusBadRequest)
				}
			}
		}
	}

	if err := psstr.DeleteProfileSchemaAttributeByID(ctx, orgID, attributeID); err != nil {
		return err
	}
	// Deleting the attribute deletes its sharing policies.
	return sharingService.DeletePoliciesOfResource(ctx, model.SharingResourceType, attributeID)
}

// DeleteProfileSchemaAttributesByScope deletes the organization's schema attributes in the scope. Sub
// organizations cannot delete them.
func (s *ProfileSchemaService) DeleteProfileSchemaAttributesByScope(
	ctx context.Context,
	orgID string,
	scope string,
) error {
	if err := suborg.RejectWrite(ctx, orgID); err != nil {
		return err
	}
	if err := psstr.DeleteProfileSchemaAttributes(ctx, orgID, scope); err != nil {
		return err
	}
	return nil
}

// GetProfileSchema retrieves the complete profile schema for the given organization ID.
func (s *ProfileSchemaService) GetProfileSchema(ctx context.Context, orgID string) (map[string]interface{}, error) {
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
	schemaAttributes, err := GetEffectiveProfileSchemaAttributes(ctx, orgID)
	if err != nil {
		errMsg := fmt.Sprintf("Error retrieving profile schema attributes for organization: %s", orgID)
		logger.Debug(errMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileSchema.Code,
			Message:     errors2.GetProfileSchema.Message,
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

// DeleteProfileSchema deletes the organization's profile schema. Sub organizations cannot delete it.
func (s *ProfileSchemaService) DeleteProfileSchema(ctx context.Context, orgID string) error {
	if err := suborg.RejectWrite(ctx, orgID); err != nil {
		return err
	}
	if err := psstr.DeleteProfileSchema(ctx, orgID); err != nil {
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

// GetProfileSchemaAttributesWithFilter returns the organization's effective schema attributes that match all
// filters, each in the form "field op value".
func GetProfileSchemaAttributesWithFilter(ctx context.Context,
	orgID string, filters []string) ([]model.ProfileSchemaAttribute, error) {
	allAttrs, err := GetEffectiveProfileSchemaAttributes(ctx, orgID)
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
			Code:        errors2.InvalidFilterFormat.Code,
			Message:     errors2.InvalidFilterFormat.Message,
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

// SyncProfileSchema fetches the organization's identity attributes from the identity server and stores them,
// keeping the IDs of attributes already stored. It fails for an organization that inherits its identity
// attributes.
func (s *ProfileSchemaService) SyncProfileSchema(ctx context.Context, orgID string) error {
	if source, inherits := identitysource.IsInherited(ctx, orgID); inherits {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.InheritedAttributeReadOnly.Code,
			Message: errors2.InheritedAttributeReadOnly.Message,
			Description: fmt.Sprintf("The organization '%s' inherits the identity attributes from '%s'. "+
				"Sync the identity attributes of '%s'.", orgID, source, source),
		}, http.StatusBadRequest)
	}
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return err
	}
	claims, err := identityClient.GetProfileSchema(orgHandle)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("failed to fetch profile schema from identity server for organization %s:", orgID)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.SyncProfileSchema.Code,
			Message:     errors2.SyncProfileSchema.Message,
			Description: errMsg,
		}, err)
	}

	if len(claims) > 0 {
		claims, err = keepIdentityAttributeIDs(ctx, orgID, claims)
		if err != nil {
			return err
		}
		err := psstr.UpsertIdentityAttributes(ctx, orgID, claims)
		if err != nil {
			errMsg := fmt.Sprintf("failed to persist profile schema for organization %s:", orgID)
			logger.Debug(errMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.SyncProfileSchema.Code,
				Message:     errors2.SyncProfileSchema.Message,
				Description: errMsg,
			}, err)
		}
		logger.Info("Profile schema successfully updated for org: " + orgID)
	}
	return nil
}

// GetProfileSchemaAttributesByScopeAndFilter validates the filters and returns the visible schema attributes
// that match them. Application data attributes are grouped by application identifier.
func (s *ProfileSchemaService) GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
	orgID string, scope string, filters []string) (interface{}, error) {
	validatedFilters := make([]string, 0, len(filters))
	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:    errors2.GetProfileSchema.Code,
				Message: errors2.GetProfileSchema.Message,
				Description: fmt.Sprintf(
					"Invalid filter for schema attributes: %#q. Filter must be in the format: field operator value",
					f,
				),
			}, http.StatusBadRequest)
		}

		field, operator, value := parts[0], parts[1], parts[2]

		// Validate operator
		switch operator {
		case "eq", "co", "sw":
		default:
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.GetProfileSchema.Code,
				Message:     errors2.GetProfileSchema.Message,
				Description: fmt.Sprintf("Unsupported operator:` %s`  while filtering schema attributes.", operator),
			}, http.StatusBadRequest)
		}

		// Validate field against allowlist
		if !constants.AllowedFilterFieldsForSchema[field] {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.GetProfileSchema.Code,
				Message:     errors2.GetProfileSchema.Message,
				Description: fmt.Sprintf("Unsupported filter field: %#q when filtering schema attributes.", field),
			}, http.StatusBadRequest)
		}

		validatedFilters = append(validatedFilters, fmt.Sprintf("%s %s %s", field, operator, value))
	}

	schemaAttributes, err := psstr.GetProfileSchemaAttributesByScopeAndFilter(ctx, orgID, scope, validatedFilters)
	if err != nil {
		return nil, err
	}
	visible, err := withVisibleAttributes(ctx, orgID, schemaAttributes, scope)
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
				log.GetLogger().
					Warn(fmt.Sprintf("Missing application identifier for application data: %s", attr.AttributeName))
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
	initiator orgModel.Organization, scope, attributeID string,
	req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error) {
	attr, err := getOwnedShareableAttribute(ctx, initiator.OrgID, scope, attributeID)
	if err != nil {
		return nil, err
	}
	return sharingService.CreatePolicy(ctx, model.SharingResourceType, attr.AttributeID, initiator, req)
}

// ListSharingPolicies returns the sharing policies of an attribute that the initiator owns.
func (s *ProfileSchemaService) ListSharingPolicies(ctx context.Context,
	initiator orgModel.Organization, scope, attributeID string) (*shareModel.PolicyList, error) {
	attr, err := getOwnedShareableAttribute(ctx, initiator.OrgID, scope, attributeID)
	if err != nil {
		return nil, err
	}
	return sharingService.ListPolicies(ctx, model.SharingResourceType, attr.AttributeID, initiator)
}

// GetSharingPolicy returns a sharing policy of an attribute that the initiator owns.
func (s *ProfileSchemaService) GetSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeID,
	policyID string) (*shareModel.PolicyResponse, error) {
	attr, err := getOwnedShareableAttribute(ctx, initiator.OrgID, scope, attributeID)
	if err != nil {
		return nil, err
	}
	return sharingService.GetPolicy(ctx, model.SharingResourceType, attr.AttributeID, policyID, initiator)
}

// UpdateSharingPolicy replaces a sharing policy of an attribute that the initiator owns.
func (s *ProfileSchemaService) UpdateSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeID, policyID string,
	req shareModel.PolicyRequest) (*shareModel.PolicyResponse, error) {
	attr, err := getOwnedShareableAttribute(ctx, initiator.OrgID, scope, attributeID)
	if err != nil {
		return nil, err
	}
	return sharingService.UpdatePolicy(ctx, model.SharingResourceType, attr.AttributeID, policyID, initiator, req)
}

// DeleteSharingPolicy removes a sharing policy of an attribute that the initiator owns.
func (s *ProfileSchemaService) DeleteSharingPolicy(ctx context.Context,
	initiator orgModel.Organization, scope, attributeID, policyID string) error {
	attr, err := getOwnedShareableAttribute(ctx, initiator.OrgID, scope, attributeID)
	if err != nil {
		return err
	}
	return sharingService.DeletePolicy(ctx, model.SharingResourceType, attr.AttributeID, policyID, initiator)
}

// The effective schema of an organization contains the attributes that it owns, the identity
// attributes that it inherits, and the shared attributes that are active in it.

// GetEffectiveProfileSchemaAttributes returns the effective schema of the organization.
func GetEffectiveProfileSchemaAttributes(ctx context.Context,
	orgID string) ([]model.ProfileSchemaAttribute, error) {
	owned, err := psstr.GetProfileSchemaAttributesForOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return withVisibleAttributes(ctx, orgID, owned, "")
}

// withVisibleAttributes adds the inherited and the active shared attributes to the owned
// attributes. A non-empty scope limits the result to that scope.
func withVisibleAttributes(ctx context.Context, orgID string, owned []model.ProfileSchemaAttribute,
	scope string) ([]model.ProfileSchemaAttribute, error) {
	stored, err := orgService.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return owned, nil
	}
	source, inherits := identitysource.IsInherited(ctx, orgID)
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
func getVisibleSharedAttribute(ctx context.Context, orgID string,
	attributeID string) (*model.ProfileSchemaAttribute, error) {
	shared, err := activeSharedAttributesOf(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, attr := range shared {
		if attr.AttributeID == attributeID {
			attr.Origin = shareModel.OriginShared
			return &attr, nil
		}
	}
	return nil, nil
}

// checkSharedNameConflict rejects a local attribute name that an active shared attribute uses.
// Application data attribute names are scoped to their application.
func checkSharedNameConflict(ctx context.Context, orgID string, attributeName, applicationIdentifier string) error {
	shared, err := activeSharedAttributesOf(ctx, orgID)
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
func inheritedAttributes(ctx context.Context, sourceID string) ([]model.ProfileSchemaAttribute, error) {
	attrs, err := psstr.GetProfileSchemaAttributesByScope(ctx, sourceID, constants.IdentityAttributes)
	if err != nil {
		return nil, err
	}
	for i := range attrs {
		attrs[i].Origin = model.OriginInherited
	}
	return attrs, nil
}

// getInheritedAttribute returns the inherited identity attribute with the ID, or nil.
func getInheritedAttribute(ctx context.Context, orgID string,
	attributeID string) (*model.ProfileSchemaAttribute, error) {
	source, inherits := identitysource.IsInherited(ctx, orgID)
	if !inherits {
		return nil, nil
	}
	attr, err := psstr.GetProfileSchemaAttributeByID(ctx, source, attributeID)
	if err != nil {
		if clientErr := new(errors2.ClientError); errors.As(err, &clientErr) {
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
		Code:        errors2.InheritedAttributeReadOnly.Code,
		Message:     errors2.InheritedAttributeReadOnly.Message,
		Description: errors2.InheritedAttributeReadOnly.Description,
	}, http.StatusForbidden)
}

// keepIdentityAttributeIDs keeps the stored ID of each synced identity attribute, so that the
// unification rules that use it stay valid.
func keepIdentityAttributeIDs(ctx context.Context, orgID string, incoming []model.ProfileSchemaAttribute) (
	[]model.ProfileSchemaAttribute, error) {
	existing, err := psstr.GetProfileSchemaAttributesByScope(ctx, orgID, constants.IdentityAttributes)
	if err != nil {
		return nil, err
	}
	idByName := map[string]string{}
	for _, attr := range existing {
		idByName[attr.AttributeName] = attr.AttributeID
	}
	finalIDByName := map[string]string{}
	for i := range incoming {
		incoming[i].OrgID = orgID
		if id, ok := idByName[incoming[i].AttributeName]; ok {
			incoming[i].AttributeID = id
		}
		finalIDByName[incoming[i].AttributeName] = incoming[i].AttributeID
	}
	for i := range incoming {
		for j := range incoming[i].SubAttributes {
			if id, ok := finalIDByName[incoming[i].SubAttributes[j].AttributeName]; ok {
				incoming[i].SubAttributes[j].AttributeID = id
			}
		}
	}
	return incoming, nil
}

// isShareableAttribute checks that the organization can share the attribute. Only a primary
// organization can share an application data attribute. A complex attribute is shared as one unit:
// its sub-attributes follow the sharing policies of the parent and cannot be shared alone.
func isShareableAttribute(ctx context.Context, orgID string, attr model.ProfileSchemaAttribute) error {
	reject := func(description string) error {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ShareBadRequest.Code,
			Message:     errors2.ShareBadRequest.Message,
			Description: description,
		}, http.StatusBadRequest)
	}
	scope := strings.SplitN(attr.AttributeName, ".", 2)[0]
	switch {
	case scope == constants.IdentityAttributes:
		return reject("Identity attributes are not shared. The identity provider controls them for each organization.")
	case scope == constants.ApplicationData && attr.ApplicationIdentifier == "":
		return reject("The application data attribute has no application identifier.")
	case scope == constants.ApplicationData && !isPrimaryOrg(ctx, orgID):
		return reject("Only the primary organization can share application data attributes.")
	case strings.Count(attr.AttributeName, ".") > 1:
		return reject("A sub-attribute cannot be shared alone. Share its parent attribute. The " +
			"sub-attributes follow the sharing policies of the parent.")
	}
	return nil
}

// isPrimaryOrg reports whether the organization is a primary organization or is not in an
// organization tree.
func isPrimaryOrg(ctx context.Context, orgID string) bool {
	stored, err := orgService.GetOrganizationByID(ctx, orgID)
	return err == nil && (stored == nil || stored.IsPrimary())
}

// GetProfileSchemaAttributesByIDs returns the attributes with the IDs, in any organization.
// OwnerOrgID of each attribute is set.
func GetProfileSchemaAttributesByIDs(ctx context.Context,
	attributeIDs []string) ([]model.ProfileSchemaAttribute, error) {
	return psstr.GetProfileSchemaAttributesByIDs(ctx, attributeIDs)
}

// activeSharedAttributesOf returns the shared attributes that are active in the organization.
func activeSharedAttributesOf(ctx context.Context, orgID string) ([]model.ProfileSchemaAttribute, error) {
	stored, err := orgService.GetOrganizationByID(ctx, orgID)
	if err != nil || stored == nil {
		return nil, err
	}
	return activeSharedAttributes(ctx, *stored)
}

// activeSharedAttributes returns the shared attributes that are active in the organization, the
// nearest owner first. OwnerOrgID of each attribute is set.
func activeSharedAttributes(ctx context.Context, org orgModel.Organization) ([]model.ProfileSchemaAttribute, error) {
	shared, err := sharingService.GetSharedResources(ctx, model.SharingResourceType, org)
	if err != nil || len(shared) == 0 {
		return nil, err
	}
	ids := make([]string, 0, len(shared))
	for _, r := range shared {
		ids = append(ids, r.ResourceID)
	}
	attrs, err := withSubAttributes(ctx, ids)
	if err != nil {
		return nil, err
	}
	owned, err := psstr.GetProfileSchemaAttributesForOrg(ctx, org.OrgID)
	if err != nil {
		return nil, err
	}
	return selectActiveSharedAttributes(shared, attrs, owned), nil
}

// withSubAttributes returns the attributes with the IDs and the sub-attributes of each complex
// attribute among them, at all levels. The sub-attributes of a shared complex attribute follow
// the sharing policies of the parent.
func withSubAttributes(ctx context.Context, ids []string) ([]model.ProfileSchemaAttribute, error) {
	var result []model.ProfileSchemaAttribute
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for len(ids) > 0 {
		attrs, err := psstr.GetProfileSchemaAttributesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		result = append(result, attrs...)
		ids = nil
		for _, attr := range attrs {
			for _, sub := range attr.SubAttributes {
				if !seen[sub.AttributeID] {
					seen[sub.AttributeID] = true
					ids = append(ids, sub.AttributeID)
				}
			}
		}
	}
	return result, nil
}

// selectActiveSharedAttributes returns the shared attributes that are active, in the order of the
// shared resources. A shared complex attribute comes with its sub-attributes, and the parent and
// its sub-attributes are active or not active together. A shared attribute is not active when an
// owned attribute or the shared attribute of a nearer owner has the same key as it or as one of
// its sub-attributes. attrs contains the shared attributes and their sub-attributes.
func selectActiveSharedAttributes(shared []shareModel.SharedResource, attrs,
	owned []model.ProfileSchemaAttribute) []model.ProfileSchemaAttribute {
	byID := make(map[string]model.ProfileSchemaAttribute, len(attrs))
	for _, attr := range attrs {
		byID[attr.AttributeID] = attr
	}
	usedKeys := make(map[string]bool, len(owned)+len(attrs))
	for _, attr := range owned {
		usedKeys[attributeKey(attr)] = true
	}
	var result []model.ProfileSchemaAttribute
	for _, r := range shared {
		attr, ok := byID[r.ResourceID]
		if !ok || attr.OwnerOrgID != r.OwnerOrgID {
			continue
		}
		group := sharedAttributeGroup(attr, byID)
		if anyKeyUsed(group, usedKeys) {
			continue
		}
		for _, member := range group {
			usedKeys[attributeKey(member)] = true
		}
		result = append(result, group...)
	}
	return result
}

// sharedAttributeGroup returns the shared attribute and its sub-attributes at all levels, parent
// first. A sub-attribute is in the group only when the owner of the parent owns it.
func sharedAttributeGroup(attr model.ProfileSchemaAttribute,
	byID map[string]model.ProfileSchemaAttribute) []model.ProfileSchemaAttribute {
	group := []model.ProfileSchemaAttribute{attr}
	inGroup := map[string]bool{attr.AttributeID: true}
	for i := 0; i < len(group); i++ {
		for _, ref := range group[i].SubAttributes {
			sub, ok := byID[ref.AttributeID]
			if !ok || inGroup[sub.AttributeID] || sub.OwnerOrgID != attr.OwnerOrgID {
				continue
			}
			inGroup[sub.AttributeID] = true
			group = append(group, sub)
		}
	}
	return group
}

// anyKeyUsed reports whether the key of an attribute of the group is in the used keys.
func anyKeyUsed(group []model.ProfileSchemaAttribute, usedKeys map[string]bool) bool {
	for _, attr := range group {
		if usedKeys[attributeKey(attr)] {
			return true
		}
	}
	return false
}

// attributeKey returns the key of an attribute for name conflicts. Application data attribute
// names are scoped to their application.
func attributeKey(attr model.ProfileSchemaAttribute) string {
	if !strings.HasPrefix(attr.AttributeName, constants.ApplicationData+".") || attr.ApplicationIdentifier == "" {
		return attr.AttributeName
	}
	return attr.ApplicationIdentifier + "\x00" + attr.AttributeName
}
