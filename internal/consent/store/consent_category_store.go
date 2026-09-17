/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package store

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	model "github.com/wso2/identity-customer-data-service/internal/consent/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"strings"
)

// AddConsentCategory inserts a new consent category into the database.
func AddConsentCategory(ctx context.Context, category model.ConsentCategory) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for inserting consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_CONSENT_CATEGORY.Code,
			Message:     errors2.ADD_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()
	dbType := dbClient.DBType()
	query := scripts.InsertConsentCategory
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for inserting consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_CONSENT_CATEGORY.Code,
			Message:     errors2.ADD_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	_, err = tx.Exec(query, category.CategoryName, category.CategoryIdentifier, category.OrgHandle, category.Purpose,
		scripts.EncodeStringArray(dbType, category.Destinations), category.IsMandatory)
	if err != nil {
		errRollback := tx.Rollback()
		if errRollback != nil {
			errorMsg := fmt.Sprintf("Failed to rollback inserting consent category: %s", category.CategoryIdentifier)
			logger.Debug(errorMsg, log.Error(errRollback))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.ADD_CONSENT_CATEGORY.Code,
				Message:     errors2.ADD_CONSENT_CATEGORY.Message,
				Description: errorMsg,
			}, errRollback)
		}
		errorMsg := fmt.Sprintf("Failed to execute query for inserting consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_CONSENT_CATEGORY.Code,
			Message:     errors2.ADD_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
	}

	attrQuery := scripts.InsertConsentCategoryAttribute
	for _, attr := range category.Attributes {
		_, err = tx.Exec(attrQuery, category.CategoryIdentifier, attr.Scope, attr.AttributeName, attr.AttributeId, attr.ApplicationIdentifier)
		if err != nil {
			_ = tx.Rollback()
			errorMsg := fmt.Sprintf("Failed to insert attribute %s for consent category: %s", attr.AttributeName, category.CategoryIdentifier)
			logger.Debug(errorMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.ADD_CONSENT_CATEGORY.Code,
				Message:     errors2.ADD_CONSENT_CATEGORY.Message,
				Description: errorMsg,
			}, err)
		}
	}

	logger.Info(fmt.Sprintf("Successfully inserted consent category: %s", category.CategoryIdentifier))
	return tx.Commit()
}

// GetAllConsentCategories retrieves all consent categories from the database.
func GetAllConsentCategories(ctx context.Context) ([]model.ConsentCategory, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get db client for fetching consent categories."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetAllConsentCategories
	results, err := dbClient.ExecuteQueryContext(ctx, query)
	if err != nil {
		errorMsg := "Failed to execute query for fetching consent categories."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: "Failed to fetch consent categories.",
		}, err)
	}

	categories := make([]model.ConsentCategory, 0, len(results))
	for _, row := range results {
		categories = append(categories, model.ConsentCategory{
			CategoryName:       row["category_name"].(string),
			CategoryIdentifier: row["category_identifier"].(string),
			OrgHandle:          row["org_handle"].(string),
			Purpose:            row["purpose"].(string),
			Destinations:       parseStringArray(row["destinations"]),
			IsMandatory:        parseBool(row["is_mandatory"]),
		})
	}
	if len(categories) == 0 {
		logger.Debug("No consent categories found")
		return nil, nil
	}

	// Resolve attributes: mandatory categories get attributes live from profile_schema;
	// regular categories read from consent_category_attributes.
	regularIds := make([]string, 0)
	for _, c := range categories {
		if !c.IsMandatory {
			regularIds = append(regularIds, c.CategoryIdentifier)
		}
	}
	attrsByCategory, err := getAttributesByCategoryIds(ctx, dbClient, regularIds)
	if err != nil {
		return nil, err
	}
	for i := range categories {
		if categories[i].IsMandatory {
			attrs, err := resolveMandatoryAttributes(ctx, dbClient, categories[i].OrgHandle)
			if err != nil {
				return nil, err
			}
			categories[i].Attributes = attrs
		} else {
			categories[i].Attributes = attrsByCategory[categories[i].CategoryIdentifier]
		}
	}

	logger.Info(fmt.Sprintf("Successfully fetched %d consent categories", len(categories)))
	return categories, nil
}

// GetConsentCategoryByID retrieves a consent category by its ID.
func GetConsentCategoryByID(ctx context.Context, id string) (*model.ConsentCategory, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching consent category: %s", id)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetConsentCategoryById
	results, err := dbClient.ExecuteQueryContext(ctx, query, id)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute query for fetching consent category: %s", id)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}

	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("Consent category not found for id: %s", id))
		return nil, nil
	}
	row := results[0]
	category := model.ConsentCategory{
		CategoryName:       row["category_name"].(string),
		CategoryIdentifier: row["category_identifier"].(string),
		OrgHandle:          row["org_handle"].(string),
		Purpose:            row["purpose"].(string),
		Destinations:       parseStringArray(row["destinations"]),
		IsMandatory:        parseBool(row["is_mandatory"]),
	}

	if category.IsMandatory {
		attrs, err := resolveMandatoryAttributes(ctx, dbClient, category.OrgHandle)
		if err != nil {
			return nil, err
		}
		category.Attributes = attrs
	} else {
		attrsByCategory, err := getAttributesByCategoryIds(ctx, dbClient, []string{id})
		if err != nil {
			return nil, err
		}
		category.Attributes = attrsByCategory[id]
	}

	return &category, nil
}

// GetConsentCategoryByName retrieves a consent category by name within an org.
func GetConsentCategoryByName(ctx context.Context, name string, orgHandle string) (*model.ConsentCategory, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching consent category: %s", name)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetConsentCategoryByName
	results, err := dbClient.ExecuteQueryContext(ctx, query, name, orgHandle)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute query for fetching consent category: %s", name)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}

	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("Consent category not found for name: %s", name))
		return nil, nil
	}
	row := results[0]
	category := model.ConsentCategory{
		CategoryName:       row["category_name"].(string),
		CategoryIdentifier: row["category_identifier"].(string),
		OrgHandle:          row["org_handle"].(string),
		Purpose:            row["purpose"].(string),
		Destinations:       parseStringArray(row["destinations"]),
		IsMandatory:        parseBool(row["is_mandatory"]),
	}
	return &category, nil
}

// UpdateConsentCategory updates an existing consent category in the database.
func UpdateConsentCategory(ctx context.Context, category model.ConsentCategory) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for updating consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating consent category: %s",
			category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	dbType := dbClient.DBType()
	query := scripts.UpdateConsentCategory
	_, err = tx.Exec(query, category.CategoryName, category.Purpose,
		scripts.EncodeStringArray(dbType, category.Destinations), category.CategoryIdentifier)
	if err != nil {
		_ = tx.Rollback()
		logger.Debug("Failed to update consent category", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: "Failed to update consent category.",
		}, err)
	}

	deleteAttrQuery := scripts.DeleteConsentCategoryAttributesByCategoryId
	_, err = tx.Exec(deleteAttrQuery, category.CategoryIdentifier)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to delete attributes for consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
	}

	insertAttrQuery := scripts.InsertConsentCategoryAttribute
	for _, attr := range category.Attributes {
		_, err = tx.Exec(insertAttrQuery, category.CategoryIdentifier, attr.Scope, attr.AttributeName, attr.AttributeId, attr.ApplicationIdentifier)
		if err != nil {
			_ = tx.Rollback()
			errorMsg := fmt.Sprintf("Failed to insert attribute %s for consent category: %s", attr.AttributeName, category.CategoryIdentifier)
			logger.Debug(errorMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
				Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
				Description: errorMsg,
			}, err)
		}
	}

	return tx.Commit()
}

func DeleteConsentCategory(ctx context.Context, categoryId string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for deleting consent category: %s", categoryId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for deleting consent category: %s", categoryId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	query := scripts.DeleteConsentCategory
	_, err = tx.Exec(query, categoryId)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to execute query for deleting consent category: %s", categoryId)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_CONSENT_CATEGORY.Code,
			Message:     errors2.UPDATE_CONSENT_CATEGORY.Message,
			Description: errMsg,
		}, err)
	}
	return tx.Commit()
}

// SeedDefaultIdentityDataCategory creates the mandatory "Identity Data" consent category for the org.
// Attributes are not stored in consent_category_attributes — they are resolved dynamically from
// profile_schema at query time so they stay in sync with schema changes automatically.
func SeedDefaultIdentityDataCategory(ctx context.Context, orgHandle string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for seeding identity data category for org: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_CONSENT_CATEGORY.Code,
			Message:     errors2.ADD_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	dbType := dbClient.DBType()
	upsertQuery := scripts.UpsertDefaultIdentityDataCategory
	_, err = dbClient.ExecuteQueryContext(ctx, upsertQuery, constants.DefaultIdentityDataCategoryName, uuid.New().String(), orgHandle,
		constants.DefaultIdentityDataCategoryPurpose, scripts.EncodeStringArray(dbType, []string{}))
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to upsert identity data category for org: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_CONSENT_CATEGORY.Code,
			Message:     errors2.ADD_CONSENT_CATEGORY.Message,
			Description: errorMsg,
		}, err)
	}

	logger.Info(fmt.Sprintf("Successfully seeded identity data consent category for org: %s", orgHandle))
	return nil
}

// resolveMandatoryAttributes fetches the attributes for a mandatory consent category live from
// profile_schema (identity_attributes scope). This mirrors what the consent filter does at
// query time, so the GET response always reflects the current schema state.
func resolveMandatoryAttributes(ctx context.Context, dbClient client.DBClientInterface, orgHandle string) (
	[]model.ConsentAttribute, error) {
	query := scripts.GetProfileSchemaAttributeByScope
	rows, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, constants.IdentityAttributes)
	if err != nil {
		return nil, err
	}
	attrs := make([]model.ConsentAttribute, 0, len(rows))
	for _, row := range rows {
		attrs = append(attrs, model.ConsentAttribute{
			Scope:         constants.ScopeIdentityAttributes,
			AttributeName: fmt.Sprint(row["attribute_name"]),
		})
	}
	return attrs, nil
}

// GetMandatoryConsentCategoryIds returns the identifiers of all mandatory consent categories for an org.
func GetMandatoryConsentCategoryIds(ctx context.Context, orgHandle string) ([]string, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching mandatory category ids for org: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetMandatoryConsentCategoryIdsByOrg
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch mandatory category ids for org: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}

	ids := make([]string, 0, len(results))
	for _, row := range results {
		ids = append(ids, row["category_identifier"].(string))
	}
	return ids, nil
}

// GetConsentedCategoryAttributesByProfileId returns the allowed attribute sets for each
// consented category. It only returns attributes for categories the profile has actively consented to.
// Mandatory categories are always included regardless of profile consent records.
func GetConsentedCategoryAttributesByProfileId(ctx context.Context,
	profileId string, orgHandle string, categoryIds []string) (map[string][]model.ConsentAttribute, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get db client for fetching consented category attributes"
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	// Fetch which categories the profile has consented to (consent_status = true)
	consentQuery := scripts.GetProfileConsentsByProfileId
	consentResults, err := dbClient.ExecuteQueryContext(ctx, consentQuery, profileId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch consents for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}

	consentedSet := make(map[string]bool)
	for _, row := range consentResults {
		if status, ok := row["consent_status"].(bool); ok && status {
			consentedSet[row["category_id"].(string)] = true
		}
	}

	// Fetch mandatory category IDs for the org — always included regardless of profile consent records.
	mandatoryQuery := scripts.GetMandatoryConsentCategoryIdsByOrg
	mandatoryResults, err := dbClient.ExecuteQueryContext(ctx, mandatoryQuery, orgHandle)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch mandatory category ids for org: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}
	mandatorySet := make(map[string]bool)
	for _, row := range mandatoryResults {
		mandatorySet[row["category_identifier"].(string)] = true
	}

	// Split requested categoryIds into mandatory and regular.
	// Mandatory categories are always included; regular ones only if profile has consented.
	seen := make(map[string]bool)
	mandatoryIds := make([]string, 0)
	regularIds := make([]string, 0)
	for _, id := range categoryIds {
		if seen[id] {
			continue
		}
		seen[id] = true
		if mandatorySet[id] {
			mandatoryIds = append(mandatoryIds, id)
		} else if consentedSet[id] {
			regularIds = append(regularIds, id)
		}
	}

	result := make(map[string][]model.ConsentAttribute)

	// For mandatory categories: resolve attributes live from profile_schema instead of
	// reading from consent_category_attributes. This means adding or removing an identity
	// attribute via schema sync automatically updates what the mandatory "Identity Data"
	// category covers — no reseeding or migration needed.
	if len(mandatoryIds) > 0 {
		schemaQuery := scripts.GetProfileSchemaAttributeByScope
		schemaResults, err := dbClient.ExecuteQueryContext(ctx, schemaQuery, orgHandle, constants.IdentityAttributes)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to fetch identity attributes from schema for org: %s", orgHandle)
			logger.Debug(errorMsg, log.Error(err))
			return nil, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
				Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
				Description: errorMsg,
			}, err)
		}
		attrs := make([]model.ConsentAttribute, 0, len(schemaResults))
		for _, row := range schemaResults {
			attrs = append(attrs, model.ConsentAttribute{
				Scope:         constants.ScopeIdentityAttributes,
				AttributeName: fmt.Sprint(row["attribute_name"]),
			})
		}
		for _, id := range mandatoryIds {
			result[id] = attrs
		}
	}

	// For regular categories: fetch from consent_category_attributes as usual.
	if len(regularIds) > 0 {
		regularAttrs, err := getAttributesByCategoryIds(ctx, dbClient, regularIds)
		if err != nil {
			return nil, err
		}
		for id, attrs := range regularAttrs {
			result[id] = attrs
		}
	}

	return result, nil
}

// getAttributesByCategoryIds is an internal helper that fetches attributes for a list of category IDs
// using the provided db client (avoids opening a second connection).
func getAttributesByCategoryIds(ctx context.Context, dbClient client.DBClientInterface, categoryIds []string) (
	map[string][]model.ConsentAttribute, error) {
	logger := log.GetLogger()

	result := make(map[string][]model.ConsentAttribute)
	if len(categoryIds) == 0 {
		return result, nil
	}

	ids := make([]interface{}, len(categoryIds))
	placeholders := make([]string, len(categoryIds))
	for i, id := range categoryIds {
		ids[i] = id
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	inQuery := scripts.GetConsentCategoryAttributesByCategoryIds.Format(strings.Join(placeholders, ", "))

	rows, err := dbClient.ExecuteQueryContext(ctx, inQuery, ids...)
	if err != nil {
		errorMsg := "Failed to fetch consent category attributes"
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FETCH_CONSENT_CATEGORIES.Code,
			Message:     errors2.FETCH_CONSENT_CATEGORIES.Message,
			Description: errorMsg,
		}, err)
	}

	for _, row := range rows {
		catId := row["category_id"].(string)
		attr := model.ConsentAttribute{
			Scope:                 row["scope"].(string),
			AttributeName:         row["attribute_name"].(string),
			AttributeId:           fmt.Sprint(row["attribute_id"]),
			ApplicationIdentifier: row["application_identifier"].(string),
		}
		result[catId] = append(result[catId], attr)
	}
	return result, nil
}

func parseBool(raw interface{}) bool {
	if raw == nil {
		return false
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	return false
}

// parseStringArray reads the destinations column, which holds a PostgreSQL
// TEXT[] on PostgreSQL and a JSON array on the inbuilt database.
func parseStringArray(raw interface{}) []string {
	return scripts.DecodeStringArray(raw)
}
