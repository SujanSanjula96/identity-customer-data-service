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

	"github.com/google/uuid"
	model "github.com/wso2/identity-customer-data-service/internal/consent/model"
	"github.com/wso2/identity-customer-data-service/internal/consent/store"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// ConsentCategoryServiceInterface defines the service interface.
type ConsentCategoryServiceInterface interface {
	GetAllConsentCategories(ctx context.Context) ([]model.ConsentCategory, error)
	GetConsentCategory(ctx context.Context, id string) (*model.ConsentCategory, error)
	AddConsentCategory(ctx context.Context, category model.ConsentCategory) (*model.ConsentCategory, error)
	UpdateConsentCategory(ctx context.Context, category model.ConsentCategory) error
	DeleteConsentCategory(ctx context.Context, id string) error
	SeedDefaultConsentCategory(ctx context.Context, orgHandle string) error
}

// ConsentCategoryService is the default implementation.
type ConsentCategoryService struct{}

// GetConsentCategoryService returns a new instance.
func GetConsentCategoryService() ConsentCategoryServiceInterface {
	return &ConsentCategoryService{}
}

// GetAllConsentCategories retrieves all categories.
func (cs *ConsentCategoryService) GetAllConsentCategories(ctx context.Context) ([]model.ConsentCategory, error) {

	consentCat, err := store.GetAllConsentCategories(ctx)

	if err != nil {
		return nil, err
	}
	if len(consentCat) == 0 {
		return []model.ConsentCategory{}, nil
	}
	return consentCat, nil

}

// GetConsentCategory retrieves a category by ID.
func (cs *ConsentCategoryService) GetConsentCategory(ctx context.Context, id string) (*model.ConsentCategory, error) {

	consentCat, err := store.GetConsentCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if consentCat == nil {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CONSENT_CAT_NOT_FOUND.Code,
			Message:     errors2.CONSENT_CAT_NOT_FOUND.Message,
			Description: fmt.Sprintf("Consent category not found for the provided categoryId: %s", id),
		}, http.StatusNotFound)
	}
	return consentCat, nil
}

// AddConsentCategory adds a new category.
func (cs *ConsentCategoryService) AddConsentCategory(ctx context.Context,
	category model.ConsentCategory) (*model.ConsentCategory, error) {

	err, isValid := cs.validateConsentCat(category)

	if !isValid || err != nil {
		return nil, err
	}

	existingCat, err := store.GetConsentCategoryByName(ctx, category.CategoryName, category.OrgHandle)

	if err != nil {
		return nil, err
	}

	if existingCat != nil && existingCat.CategoryName == category.CategoryName {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CONSENT_CAT_ALREADY_EXISTS.Code,
			Message:     errors2.CONSENT_CAT_ALREADY_EXISTS.Message,
			Description: fmt.Sprintf("Category with the same name :%s already exists.", category.CategoryName),
		}, http.StatusConflict)
	}

	// category_identifier is always server-generated; ignore any caller-supplied value.
	category.CategoryIdentifier = uuid.New().String()

	resolved, err := resolveAttributeScopes(ctx, category.OrgHandle, category.Attributes)
	if err != nil {
		return nil, err
	}
	category.Attributes = resolved

	err = store.AddConsentCategory(ctx, category)
	if err != nil {
		return nil, err
	}
	return &category, nil
}

func (cs *ConsentCategoryService) validateConsentCat(category model.ConsentCategory) (error, bool) {

	if category.CategoryName == "" || category.Purpose == "" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CONSENT_CAT_VALIDATION.Code,
			Message:     errors2.CONSENT_CAT_VALIDATION.Message,
			Description: "category_name and purpose are required.",
		}, http.StatusBadRequest), false
	}

	if !constants.AllowedConsentPurposes[category.Purpose] {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CONSENT_CAT_VALIDATION.Code,
			Message:     errors2.CONSENT_CAT_VALIDATION.Message,
			Description: "Invalid purpose. Allowed values are profiling, personalization, destination.",
		}, http.StatusBadRequest), false
	}

	for _, attr := range category.Attributes {
		if attr.AttributeName == "" {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.CONSENT_CAT_VALIDATION.Code,
				Message:     errors2.CONSENT_CAT_VALIDATION.Message,
				Description: "attribute_name is required for each attribute.",
			}, http.StatusBadRequest), false
		}
	}
	return nil, true
}

// resolveAttributeScopes looks up each attribute in the profile schema by name and populates
// Scope (converted to API scope) and AttributeId. Returns an error if any attribute_name is
// not found in the org's schema. For applicationData scope, app_id must also be provided.
func resolveAttributeScopes(ctx context.Context,
	orgHandle string, attrs []model.ConsentAttribute) ([]model.ConsentAttribute, error) {
	svc := schemaService.GetProfileSchemaService()
	resolved := make([]model.ConsentAttribute, 0, len(attrs))
	for _, attr := range attrs {
		schemaAttr, err := svc.GetProfileSchemaAttributeByName(ctx, attr.AttributeName, orgHandle)
		if err != nil || schemaAttr == nil {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.CONSENT_CAT_VALIDATION.Code,
				Message:     errors2.CONSENT_CAT_VALIDATION.Message,
				Description: fmt.Sprintf("attribute_name '%s' not found in org schema.", attr.AttributeName),
			}, http.StatusBadRequest)
		}
		// Infer scope directly from the attribute_name prefix — more reliable than reading
		// the scope column from profile_schema (avoids any query/mapping inconsistency).
		attr.Scope = inferScope(attr.AttributeName)
		attr.AttributeId = schemaAttr.AttributeId
		if attr.Scope == constants.ApplicationData {
			if attr.ApplicationIdentifier == "" {
				return nil, errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.CONSENT_CAT_VALIDATION.Code,
					Message:     errors2.CONSENT_CAT_VALIDATION.Message,
					Description: fmt.Sprintf("application_identifier is required for applicationData attribute '%s'.", attr.AttributeName),
				}, http.StatusBadRequest)
			}
			if schemaAttr.ApplicationIdentifier != attr.ApplicationIdentifier {
				return nil, errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.CONSENT_CAT_VALIDATION.Code,
					Message:     errors2.CONSENT_CAT_VALIDATION.Message,
					Description: fmt.Sprintf("application_identifier '%s' does not match the application_identifier '%s' registered in the schema for attribute '%s'.", attr.ApplicationIdentifier, schemaAttr.ApplicationIdentifier, attr.AttributeName),
				}, http.StatusBadRequest)
			}
		}
		resolved = append(resolved, attr)
	}
	return resolved, nil
}

// inferScope derives the API-facing scope from the attribute_name prefix.
// "traits.*" → "traits", "identity_attributes.*" → "identityAttributes", "application_data.*" → "applicationData"
func inferScope(attributeName string) string {
	switch {
	case strings.HasPrefix(attributeName, constants.Traits+"."):
		return constants.Traits
	case strings.HasPrefix(attributeName, constants.IdentityAttributes+"."):
		return constants.IdentityAttributes
	case strings.HasPrefix(attributeName, constants.ApplicationData+"."):
		return constants.ApplicationData
	default:
		return attributeName
	}
}

// UpdateConsentCategory updates an existing category.
func (cs *ConsentCategoryService) UpdateConsentCategory(ctx context.Context, category model.ConsentCategory) error {

	if category.CategoryIdentifier == "" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: "Consent category ID is required for update.",
		}, http.StatusBadRequest)
	}

	if err, ok := cs.validateConsentCat(category); !ok {
		return err
	}

	if err := guardMandatoryCategory(ctx, category.CategoryIdentifier); err != nil {
		return err
	}

	resolved, err := resolveAttributeScopes(ctx, category.OrgHandle, category.Attributes)
	if err != nil {
		return err
	}
	category.Attributes = resolved

	return store.UpdateConsentCategory(ctx, category)
}

// DeleteConsentCategory deletes an existing category.
func (cs *ConsentCategoryService) DeleteConsentCategory(ctx context.Context, categoryId string) error {
	if categoryId == "" {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: "Consent category Id is required for update.",
		}, http.StatusBadRequest)
	}
	if err := guardMandatoryCategory(ctx, categoryId); err != nil {
		return err
	}
	return store.DeleteConsentCategory(ctx, categoryId)
}

// guardMandatoryCategory rejects mutations on any category flagged is_mandatory in the DB.
func guardMandatoryCategory(ctx context.Context, categoryId string) error {
	cat, err := store.GetConsentCategoryByID(ctx, categoryId)
	if err != nil {
		return err
	}
	if cat != nil && cat.IsMandatory {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CONSENT_CAT_MANDATORY.Code,
			Message:     errors2.CONSENT_CAT_MANDATORY.Message,
			Description: fmt.Sprintf("Consent category '%s' is mandatory and cannot be modified or deleted.", categoryId),
		}, http.StatusForbidden)
	}
	return nil
}

// SeedDefaultConsentCategory seeds the mandatory identity data consent category for the org.
func (cs *ConsentCategoryService) SeedDefaultConsentCategory(ctx context.Context, orgHandle string) error {
	return store.SeedDefaultIdentityDataCategory(ctx, orgHandle)
}
