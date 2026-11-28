/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

// Package store persists consent categories and the profile attributes they cover.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/wso2/identity-customer-data-service/internal/consent/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// AddConsentCategory inserts a new consent category into the database.
func AddConsentCategory(ctx context.Context, orgID string, category model.ConsentCategory) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get db client for inserting consent category: %s",
			category.CategoryIdentifier,
		)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()
	dbType := dbClient.DBType()
	query := scripts.InsertConsentCategory
	destinations, err := scripts.EncodeStringArray(dbType, category.Destinations)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to encode destinations for consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to begin transaction for inserting consent category: %s",
			category.CategoryIdentifier,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	_, err = tx.ExecContext(ctx, query, category.CategoryName, category.CategoryIdentifier, orgID, category.Purpose,
		destinations, category.IsMandatory)
	if err != nil {
		errRollback := tx.Rollback()
		if errRollback != nil {
			errorMsg := fmt.Sprintf("Failed to rollback inserting consent category: %s", category.CategoryIdentifier)
			logger.Debug(errorMsg, log.Error(errRollback))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.AddConsentCategory.Code,
				Message:     errors2.AddConsentCategory.Message,
				Description: errorMsg,
			}, errRollback)
		}
		errorMsg := fmt.Sprintf(
			"Failed to execute query for inserting consent category: %s",
			category.CategoryIdentifier,
		)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}

	attrQuery := scripts.InsertConsentCategoryAttribute
	for _, attr := range category.Attributes {
		_, err = tx.ExecContext(
			ctx,
			attrQuery,
			category.CategoryIdentifier,
			attr.Scope,
			attr.AttributeName,
			attr.AttributeID,
			attr.ApplicationIdentifier,
		)
		if err != nil {
			_ = tx.Rollback()
			errorMsg := fmt.Sprintf(
				"Failed to insert attribute %s for consent category: %s",
				attr.AttributeName,
				category.CategoryIdentifier,
			)
			logger.Debug(errorMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.AddConsentCategory.Code,
				Message:     errors2.AddConsentCategory.Message,
				Description: errorMsg,
			}, err)
		}
	}

	logger.Info(fmt.Sprintf("Successfully inserted consent category: %s", category.CategoryIdentifier))
	return tx.Commit()
}

// GetAllConsentCategories retrieves all consent categories of the given org from the database.
func GetAllConsentCategories(ctx context.Context, orgID string) ([]model.ConsentCategory, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get db client for fetching consent categories."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetAllConsentCategories
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID)
	if err != nil {
		errorMsg := "Failed to execute query for fetching consent categories."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: "Failed to fetch consent categories.",
		}, err)
	}

	categories := make([]model.ConsentCategory, 0, len(results))
	for _, row := range results {
		categories = append(categories, model.ConsentCategory{
			CategoryName:       row["category_name"].(string),
			CategoryIdentifier: row["category_identifier"].(string),
			OrgID:              rows.String(row, "org_id"),
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
	regularIDs := make([]string, 0)
	for _, c := range categories {
		if !c.IsMandatory {
			regularIDs = append(regularIDs, c.CategoryIdentifier)
		}
	}
	attrsByCategory, err := getAttributesByCategoryIDs(ctx, dbClient, regularIDs)
	if err != nil {
		return nil, err
	}
	for i := range categories {
		if categories[i].IsMandatory {
			attrs, err := resolveMandatoryAttributes(ctx, dbClient, orgID)
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
func GetConsentCategoryByID(ctx context.Context, id string, orgID string) (*model.ConsentCategory, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching consent category: %s", id)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetConsentCategoryByID
	results, err := dbClient.ExecuteQueryContext(ctx, query, id, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute query for fetching consent category: %s", id)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
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
		OrgID:              rows.String(row, "org_id"),
		Purpose:            row["purpose"].(string),
		Destinations:       parseStringArray(row["destinations"]),
		IsMandatory:        parseBool(row["is_mandatory"]),
	}

	if category.IsMandatory {
		attrs, err := resolveMandatoryAttributes(ctx, dbClient, orgID)
		if err != nil {
			return nil, err
		}
		category.Attributes = attrs
	} else {
		attrsByCategory, err := getAttributesByCategoryIDs(ctx, dbClient, []string{id})
		if err != nil {
			return nil, err
		}
		category.Attributes = attrsByCategory[id]
	}

	return &category, nil
}

// GetConsentCategoryByName retrieves a consent category by name within an org.
func GetConsentCategoryByName(ctx context.Context, name string, orgID string) (*model.ConsentCategory, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching consent category: %s", name)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetConsentCategoryByName
	results, err := dbClient.ExecuteQueryContext(ctx, query, name, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute query for fetching consent category: %s", name)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
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
		OrgID:              rows.String(row, "org_id"),
		Purpose:            row["purpose"].(string),
		Destinations:       parseStringArray(row["destinations"]),
		IsMandatory:        parseBool(row["is_mandatory"]),
	}
	return &category, nil
}

// UpdateConsentCategory updates an existing consent category in the database.
func UpdateConsentCategory(ctx context.Context, orgID string, category model.ConsentCategory) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get db client for updating consent category: %s",
			category.CategoryIdentifier,
		)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating consent category: %s",
			category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	dbType := dbClient.DBType()
	query := scripts.UpdateConsentCategory
	destinations, err := scripts.EncodeStringArray(dbType, category.Destinations)
	if err != nil {
		_ = tx.Rollback()
		logger.Debug("Failed to encode destinations for consent category", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: "Failed to encode destinations for the consent category.",
		}, err)
	}
	_, err = tx.ExecContext(ctx, query, category.CategoryName, category.Purpose,
		destinations,
		category.CategoryIdentifier, orgID)
	if err != nil {
		_ = tx.Rollback()
		logger.Debug("Failed to update consent category", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: "Failed to update consent category.",
		}, err)
	}

	deleteAttrQuery := scripts.DeleteConsentCategoryAttributesByCategoryID
	_, err = tx.ExecContext(ctx, deleteAttrQuery, category.CategoryIdentifier)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to delete attributes for consent category: %s", category.CategoryIdentifier)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}

	insertAttrQuery := scripts.InsertConsentCategoryAttribute
	for _, attr := range category.Attributes {
		_, err = tx.ExecContext(
			ctx,
			insertAttrQuery,
			category.CategoryIdentifier,
			attr.Scope,
			attr.AttributeName,
			attr.AttributeID,
			attr.ApplicationIdentifier,
		)
		if err != nil {
			_ = tx.Rollback()
			errorMsg := fmt.Sprintf(
				"Failed to insert attribute %s for consent category: %s",
				attr.AttributeName,
				category.CategoryIdentifier,
			)
			logger.Debug(errorMsg, log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UpdateConsentCategory.Code,
				Message:     errors2.UpdateConsentCategory.Message,
				Description: errorMsg,
			}, err)
		}
	}

	return tx.Commit()
}

// DeleteConsentCategory deletes the consent category of the organization.
func DeleteConsentCategory(ctx context.Context, categoryID string, orgID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for deleting consent category: %s", categoryID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for deleting consent category: %s", categoryID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	query := scripts.DeleteConsentCategory
	_, err = tx.ExecContext(ctx, query, categoryID, orgID)
	if err != nil {
		_ = tx.Rollback()
		errMsg := fmt.Sprintf("Failed to execute query for deleting consent category: %s", categoryID)
		logger.Debug(errMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateConsentCategory.Code,
			Message:     errors2.UpdateConsentCategory.Message,
			Description: errMsg,
		}, err)
	}
	return tx.Commit()
}

// SeedDefaultIdentityDataCategory creates the mandatory "Identity Data" consent category for the org.
// Attributes are not stored in consent_category_attributes — they are resolved dynamically from
// profile_schema at query time so they stay in sync with schema changes automatically.
func SeedDefaultIdentityDataCategory(ctx context.Context, orgID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for seeding identity data category for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	dbType := dbClient.DBType()
	upsertQuery := scripts.UpsertDefaultIdentityDataCategory
	destinations, err := scripts.EncodeStringArray(dbType, []string{})
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to encode destinations for the identity data category of org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}
	_, err = dbClient.ExecuteQueryContext(
		ctx,
		upsertQuery,
		constants.DefaultIdentityDataCategoryName,
		uuid.New().String(),
		orgID,
		constants.DefaultIdentityDataCategoryPurpose,
		destinations,
	)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to upsert identity data category for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddConsentCategory.Code,
			Message:     errors2.AddConsentCategory.Message,
			Description: errorMsg,
		}, err)
	}

	logger.Info(fmt.Sprintf("Successfully seeded identity data consent category for org: %s", orgID))
	return nil
}

// resolveMandatoryAttributes fetches the attributes for a mandatory consent category live from
// profile_schema (identity_attributes scope). This mirrors what the consent filter does at
// query time, so the GET response always reflects the current schema state.
func resolveMandatoryAttributes(ctx context.Context, dbClient client.DBClientInterface, orgID string) (
	[]model.ConsentAttribute, error) {
	query := scripts.GetProfileSchemaAttributeByScope
	rows, err := dbClient.ExecuteQueryContext(ctx, query, orgID, constants.IdentityAttributes)
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

// GetMandatoryConsentCategoryIDs returns the identifiers of all mandatory consent categories for an org.
func GetMandatoryConsentCategoryIDs(ctx context.Context, orgID string) ([]string, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching mandatory category ids for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetMandatoryConsentCategoryIDsByOrg
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch mandatory category ids for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}

	ids := make([]string, 0, len(results))
	for _, row := range results {
		ids = append(ids, row["category_identifier"].(string))
	}
	return ids, nil
}

// GetConsentCategoryIDs returns the IDs of all consent categories of the org.
func GetConsentCategoryIDs(ctx context.Context, orgID string) ([]string, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client for fetching category ids for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetConsentCategoryIDsByOrg
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch category ids for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}

	ids := make([]string, 0, len(results))
	for _, row := range results {
		ids = append(ids, row["category_identifier"].(string))
	}
	return ids, nil
}

// GetConsentFilterAttributes returns what filtering profiles by consent needs from an
// organization, once for any number of profiles: the mandatory category identifiers, and
// the attributes of each mandatory and requested category. Attributes are returned for a
// requested category whether or not any profile consented to it; GetProfileConsentsByProfileIDs
// supplies which categories each profile consented to.
func GetConsentFilterAttributes(ctx context.Context, orgID string, categoryIDs []string) (
	map[string]bool, map[string][]model.ConsentAttribute, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get db client for fetching consent filter attributes"
		logger.Debug(errorMsg, log.Error(err))
		return nil, nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	mandatoryQuery := scripts.GetMandatoryConsentCategoryIDsByOrg
	mandatoryResults, err := dbClient.ExecuteQueryContext(ctx, mandatoryQuery, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch mandatory category ids for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	mandatorySet := make(map[string]bool, len(mandatoryResults))
	for _, row := range mandatoryResults {
		mandatorySet[row["category_identifier"].(string)] = true
	}

	result := make(map[string][]model.ConsentAttribute)

	// Mandatory categories resolve their attributes live from profile_schema instead of
	// consent_category_attributes. This means adding or removing an identity attribute via
	// schema sync automatically updates what the mandatory "Identity Data" category covers —
	// no reseeding or migration needed.
	if len(mandatorySet) > 0 {
		attrs, err := resolveMandatoryAttributes(ctx, dbClient, orgID)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to fetch identity attributes from schema for org: %s", orgID)
			logger.Debug(errorMsg, log.Error(err))
			return nil, nil, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.FetchConsentCategories.Code,
				Message:     errors2.FetchConsentCategories.Message,
				Description: errorMsg,
			}, err)
		}
		for id := range mandatorySet {
			result[id] = attrs
		}
	}

	seen := make(map[string]bool, len(categoryIDs))
	regularIDs := make([]string, 0, len(categoryIDs))
	for _, id := range categoryIDs {
		if seen[id] || mandatorySet[id] {
			continue
		}
		seen[id] = true
		regularIDs = append(regularIDs, id)
	}
	if len(regularIDs) > 0 {
		regularAttrs, err := getAttributesByCategoryIDs(ctx, dbClient, regularIDs)
		if err != nil {
			return nil, nil, err
		}
		for id, attrs := range regularAttrs {
			result[id] = attrs
		}
	}

	return mandatorySet, result, nil
}

// GetProfileConsentsByProfileIDs returns, for each of the given profiles, the categories it
// has consented to (consent_status = true), in one query. A profile with no consent records
// is absent from the result.
func GetProfileConsentsByProfileIDs(ctx context.Context, profileIDs []string) (map[string]map[string]bool, error) {
	result := make(map[string]map[string]bool)
	if len(profileIDs) == 0 {
		return result, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get db client for fetching profile consents"
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	ids := make([]interface{}, len(profileIDs))
	placeholders := make([]string, len(profileIDs))
	for i, id := range profileIDs {
		ids[i] = id
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	inQuery := scripts.GetProfileConsentsByProfileIDs.Format(strings.Join(placeholders, ", "))

	rows, err := dbClient.ExecuteQueryContext(ctx, inQuery, ids...)
	if err != nil {
		errorMsg := "Failed to fetch consents for profiles"
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}

	for _, row := range rows {
		if !parseBool(row["consent_status"]) {
			continue
		}
		profileID := row["profile_id"].(string)
		if result[profileID] == nil {
			result[profileID] = make(map[string]bool)
		}
		result[profileID][row["category_id"].(string)] = true
	}
	return result, nil
}

// getAttributesByCategoryIDs is an internal helper that fetches attributes for a list of category IDs
// using the provided db client (avoids opening a second connection).
func getAttributesByCategoryIDs(ctx context.Context, dbClient client.DBClientInterface, categoryIDs []string) (
	map[string][]model.ConsentAttribute, error) {
	logger := log.GetLogger()

	result := make(map[string][]model.ConsentAttribute)
	if len(categoryIDs) == 0 {
		return result, nil
	}

	ids := make([]interface{}, len(categoryIDs))
	placeholders := make([]string, len(categoryIDs))
	for i, id := range categoryIDs {
		ids[i] = id
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	inQuery := scripts.GetConsentCategoryAttributesByCategoryIDs.Format(strings.Join(placeholders, ", "))

	rows, err := dbClient.ExecuteQueryContext(ctx, inQuery, ids...)
	if err != nil {
		errorMsg := "Failed to fetch consent category attributes"
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FetchConsentCategories.Code,
			Message:     errors2.FetchConsentCategories.Message,
			Description: errorMsg,
		}, err)
	}

	for _, row := range rows {
		catID := row["category_id"].(string)
		attr := model.ConsentAttribute{
			Scope:                 row["scope"].(string),
			AttributeName:         row["attribute_name"].(string),
			AttributeID:           fmt.Sprint(row["attribute_id"]),
			ApplicationIdentifier: row["application_identifier"].(string),
		}
		result[catID] = append(result[catID], attr)
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
