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

// Package store persists the profile schema attributes.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func valueTuples(rows [][]interface{}) (string, []interface{}) {
	tuples := make([]string, len(rows))
	var args []interface{}
	for i, row := range rows {
		placeholders := make([]string, len(row))
		for j := range row {
			placeholders[j] = "$" + strconv.Itoa(len(args)+j+1)
		}
		tuples[i] = "(" + strings.Join(placeholders, ", ") + ")"
		args = append(args, row...)
	}
	return strings.Join(tuples, ", "), args
}

// AddProfileSchemaAttributesForScope adds multiple profile schema attributes.
func AddProfileSchemaAttributesForScope(ctx context.Context,
	attrs []model.ProfileSchemaAttribute, scope, orgID string) error {
	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := "Error occurred while initializing DB client for adding profile schema attributes"
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.AddProfileSchema.Code,
			Message:     errors.AddProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	if len(attrs) == 0 {
		return nil // nothing to insert
	}

	rows := make([][]interface{}, 0, len(attrs))

	for _, attr := range attrs {
		subAttrsJSON, err := json.Marshal(attr.SubAttributes)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to marshal sub attributes for attribute %s", attr.AttributeID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.AddProfileSchema.Code,
				Message:     errors.AddProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
		canonicalJSON, err := json.Marshal(attr.CanonicalValues)

		if err != nil {
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.AddProfileSchema.Code,
				Message:     errors.AddProfileSchema.Message,
				Description: fmt.Sprintf("Failed to marshal canonical values for attribute %s", attr.AttributeID),
			}, err)
		}

		rows = append(rows, []interface{}{orgID, attr.AttributeID, attr.AttributeName, attr.ValueType,
			attr.MergeStrategy, attr.ApplicationIdentifier, attr.Mutability, attr.MultiValued, string(subAttrsJSON),
			string(canonicalJSON), scope, attr.DisplayName})
	}

	batchSize := min(len(rows), database.MaxRowsPerStatement(dbClient.DBType(), 12))
	for start := 0; start < len(rows); start += batchSize {
		tuples, valueArgs := valueTuples(rows[start:min(start+batchSize, len(rows))])
		query := scripts.InsertProfileSchemaAttributesForScope.Append(tuples)
		_, err = dbClient.ExecuteQueryContext(ctx, query, valueArgs...)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to insert profile schema attributes for org: %s", attrs[0].OrgID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.AddProfileSchema.Code,
				Message:     errors.AddProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
	}
	logger.Info(fmt.Sprintf("Successfully added %d profile schema attributes for organization: %s",
		len(attrs), attrs[0].OrgID))
	return nil
}

// GetProfileSchemaAttributeByID retrieves a profile schema attribute by its ID for a given organization.
func GetProfileSchemaAttributeByID(ctx context.Context,
	orgID, attributeID string) (model.ProfileSchemaAttribute, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for org: %s and attribute: %s",
			orgID, attributeID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return model.ProfileSchemaAttribute{}, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileSchemaAttributeByID

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID, attributeID)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for the org:%s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return model.ProfileSchemaAttribute{}, serverError
	}
	if len(results) == 0 {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.AttributeNotFound.Code,
			Message:     errors.AttributeNotFound.Message,
			Description: "Profile schema attribute not found for org: " + orgID + " and attribute: " + attributeID,
		}, http.StatusNotFound)
		return model.ProfileSchemaAttribute{}, clientError
	}
	row := results[0]
	return mapRowToProfileAttribute(row), nil
}

// GetProfileSchemaAttributesByScope retrieves all profile schema attributes for a given organization and scope.
func GetProfileSchemaAttributesByScope(ctx context.Context,
	orgID, scope string) ([]model.ProfileSchemaAttribute, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Error initializing DB client for org: %s and scope: %s", orgID, scope)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileSchemaAttributeByScope

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID, scope)
	if err != nil {
		errorMsg := fmt.Sprintf("Error fetching profile schema attributes for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}

	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile schema attributes found for org: %s and scope: %s", orgID, scope))
		return []model.ProfileSchemaAttribute{}, nil
	}

	attributes := make([]model.ProfileSchemaAttribute, 0, len(results))
	for _, row := range results {
		attributes = append(attributes, mapRowToProfileAttribute(row))
	}

	return attributes, nil
}

// GetProfileSchemaAttributeByName retrieves a profile schema attribute by its name for a given organization.
func GetProfileSchemaAttributeByName(ctx context.Context,
	orgID, attributeName string) (*model.ProfileSchemaAttribute, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()

	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for fetching schema attribute: %s", attributeName)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileSchemaAttributeByName

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID, attributeName)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed in fetching schema attribute '%s' for organization '%s'", attributeName,
			orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile schema found for attribute: %s in org: %s", attributeName, orgID))
		return nil, nil
	}

	row := results[0]
	var subAttrs []model.SubAttribute
	if raw, ok := row["sub_attributes"].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &subAttrs); err != nil {
			errorMsg := fmt.Sprintf("Failed to unmarshal sub_attributes for attribute '%s' in org '%s'",
				attributeName, orgID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors.NewServerError(errors.ErrorMessage{
				Code:        errors.GetProfileSchema.Code,
				Message:     errors.GetProfileSchema.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
	}

	var canonicalValues []model.CanonicalValue
	if raw, ok := row["canonical_values"].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &canonicalValues); err != nil {
			errorMsg := fmt.Sprintf("Failed to unmarshal canonical_values for attribute '%s' in organization '%s'",
				attributeName, orgID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors.NewServerError(errors.ErrorMessage{
				Code:        errors.GetProfileSchema.Code,
				Message:     errors.GetProfileSchema.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
	}

	attr := &model.ProfileSchemaAttribute{
		OrgID:                 orgID,
		AttributeName:         row["attribute_name"].(string),
		DisplayName:           row["display_name"].(string),
		AttributeID:           row["attribute_id"].(string),
		ValueType:             row["value_type"].(string),
		MergeStrategy:         row["merge_strategy"].(string),
		Mutability:            row["mutability"].(string),
		ApplicationIdentifier: row["application_identifier"].(string),
		MultiValued:           row["multi_valued"].(bool),
		SubAttributes:         subAttrs,
		CanonicalValues:       canonicalValues,
	}

	logger.Info(fmt.Sprintf("Successfully fetched profile schema attribute '%s' for organizaton '%s'",
		attributeName, orgID))
	return attr, nil
}

// GetProfileSchemaAttributesForOrg retrieves all profile schema attributes for a given organization.
func GetProfileSchemaAttributesForOrg(ctx context.Context, orgID string) ([]model.ProfileSchemaAttribute, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileSchemaByOrg

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	schema := make([]model.ProfileSchemaAttribute, 0)
	for _, row := range results {
		attr := mapRowToProfileAttribute(row)
		schema = append(schema, attr)
	}
	return schema, nil
}

// GetProfileSchemaAttributesByIDs returns the attributes with the IDs. OwnerOrgID of each
// attribute is set.
func GetProfileSchemaAttributesByIDs(ctx context.Context,
	attributeIDs []string) ([]model.ProfileSchemaAttribute, error) {
	if len(attributeIDs) == 0 {
		return nil, nil
	}
	serverError := func(description string, err error) error {
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: description,
		}, err)
	}
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, serverError("Failed to get a database client to read attributes by ID.", err)
	}
	defer func() { _ = dbClient.Close() }()

	placeholders := make([]string, len(attributeIDs))
	args := make([]interface{}, len(attributeIDs))
	for i, id := range attributeIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	results, err := dbClient.ExecuteQueryContext(ctx,
		scripts.GetProfileSchemaAttributesByIDs.Format(strings.Join(placeholders, ", ")), args...)
	if err != nil {
		return nil, serverError("Failed to read attributes by ID.", err)
	}
	attrs := make([]model.ProfileSchemaAttribute, 0, len(results))
	for _, row := range results {
		attr := mapRowToProfileAttribute(row)
		attr.OwnerOrgID = rows.String(row, "org_id")
		attrs = append(attrs, attr)
	}
	return attrs, nil
}

// DeleteProfileSchemaAttributeByID deletes a specific profile schema attribute by its ID for a given organization.
func DeleteProfileSchemaAttributeByID(ctx context.Context, orgID, attributeID string) error {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Error occurred while deleting profile schema for org: %s and attribute: %s",
			orgID,
			attributeID,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DeleteProfileSchema.Code,
			Message:     errors.DeleteProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.DeleteProfileSchemaAttributeByID
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgID, attributeID)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting profile schema attribute with id: %s", attributeID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DeleteProfileSchema.Code,
			Message:     errors.DeleteProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	logger.Info(fmt.Sprintf("Profile schema attribute: %s deleted: ", attributeID))
	return nil
}

// DeleteProfileSchemaAttributes deletes all profile schema attributes for a given organization and scope.
func DeleteProfileSchemaAttributes(ctx context.Context, orgID, scope string) error {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Error occurred while deleting profile schema for org: %s and attribute: %s",
			orgID,
			scope,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DeleteProfileSchema.Code,
			Message:     errors.DeleteProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.DeleteProfileSchemaAttributeForScope
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgID, scope)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting profile schema attribute with scope: %s", scope)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DeleteProfileSchema.Code,
			Message:     errors.DeleteProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	logger.Info(fmt.Sprintf("Profile schema attributes with scope:%s deleted for the organization:%s ", scope, orgID))
	return nil
}

// PatchProfileSchemaAttributesForScope updates the given attributes of the scope, matched by ID, in one
// transaction.
func PatchProfileSchemaAttributesForScope(ctx context.Context,
	orgID string, scope string, updates []model.ProfileSchemaAttribute) error {
	// An organization that CDS does not know gets an empty key, which matches no row.

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Error initializing DB client for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UpdateProfileSchema.Code,
			Message:     errors.UpdateProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to begin transaction for update of profile schema attributes for organization: %s",
			orgID,
		)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UpdateProfileSchema.Code,
			Message:     errors.UpdateProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}

	stmt := scripts.UpdateProfileSchemaAttributesForSchema

	for _, attr := range updates {
		subAttrsJSON, err := json.Marshal(attr.SubAttributes)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to marshal sub attributes for attribute %s", attr.AttributeID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.UpdateProfileSchema.Code,
				Message:     errors.UpdateProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
		canonicalJSON, err := json.Marshal(attr.CanonicalValues)

		if err != nil {
			errorMsg := fmt.Sprintf("Failed to marshal canonical values for attribute %s", attr.AttributeID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.UpdateProfileSchema.Code,
				Message:     errors.UpdateProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
		args := []interface{}{
			attr.AttributeName,
			attr.ValueType,
			attr.MergeStrategy,
			attr.Mutability,
			attr.ApplicationIdentifier,
			attr.MultiValued,
			string(canonicalJSON),
			string(subAttrsJSON),
			attr.DisplayName,
			orgID,
			attr.AttributeID,
			scope,
		}

		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			err := tx.Rollback()
			if err != nil {
				errorMsg := fmt.Sprintf(
					"Failed to rollback updating attribute %s for organization %s",
					attr.AttributeID,
					orgID,
				)
				logger.Debug(errorMsg, log.Error(err))
				return errors.NewServerError(errors.ErrorMessage{
					Code:        errors.UpdateProfileSchema.Code,
					Message:     errors.UpdateProfileSchema.Message,
					Description: errorMsg,
				}, err)
			}
			errorMsg := fmt.Sprintf("Failed to update attribute %s for organization %s", attr.AttributeID, orgID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.UpdateProfileSchema.Code,
				Message:     errors.UpdateProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
	}

	if err := tx.Commit(); err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to commit transaction for updating profile schema attributes for organization: %s",
			orgID,
		)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UpdateProfileSchema.Code,
			Message:     errors.UpdateProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}

	logger.Info(
		fmt.Sprintf(
			"Update completed for %d profile schema attributes of scope: %s of organization: %s )",
			len(updates),
			scope,
			orgID,
		),
	)
	return nil
}

// DeleteProfileSchema deletes all profile schema attributes for a given organization.
func DeleteProfileSchema(ctx context.Context, orgID string) error {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting all of profile schema attributes for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DeleteProfileSchema.Code,
			Message:     errors.DeleteProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.DeleteProfileSchemaForOrg
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgID)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting all of profile schema attributes for org: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DeleteProfileSchema.Code,
			Message:     errors.DeleteProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	logger.Info("Profile schema cleared for org: " + orgID)
	return nil
}

// mapRowToProfileAttribute converts database row to a ProfileSchemaAttribute model.
func mapRowToProfileAttribute(row map[string]interface{}) model.ProfileSchemaAttribute {
	var subAttrs []model.SubAttribute
	if raw, ok := row["sub_attributes"].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &subAttrs); err != nil {
			log.GetLogger().Debug("Failed to unmarshal sub_attributes", log.Error(err))
		}
	}

	var canonicalValues []model.CanonicalValue
	if raw, ok := row["canonical_values"].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &canonicalValues); err != nil {
			log.GetLogger().Debug("Failed to unmarshal canonical_values", log.Error(err))
		}
	}

	scope := ""
	if s, ok := row["scope"].(string); ok {
		scope = s
	}

	return model.ProfileSchemaAttribute{
		AttributeID:           fmt.Sprint(row["attribute_id"]),
		AttributeName:         row["attribute_name"].(string),
		Scope:                 scope,
		DisplayName:           row["display_name"].(string),
		ValueType:             row["value_type"].(string),
		MergeStrategy:         row["merge_strategy"].(string),
		Mutability:            row["mutability"].(string),
		ApplicationIdentifier: row["application_identifier"].(string),
		MultiValued:           row["multi_valued"].(bool),
		SubAttributes:         subAttrs,
		CanonicalValues:       canonicalValues,
	}
}

// UpsertIdentityAttributes replaces the organization's identity attributes with the given ones in one
// transaction: it updates existing rows in place, so their IDs and dependent rows stay, inserts new ones and
// deletes the attributes that are no longer present.
func UpsertIdentityAttributes(ctx context.Context, orgID string, attrs []model.ProfileSchemaAttribute) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error initializing DB client for organization: %s ", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UpdateProfileSchema.Code,
			Message:     errors.UpdateProfileSchema.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.SyncProfileSchema.Code,
			Message:     errors.SyncProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Step 1: Upsert incoming attributes in-place so that existing attribute_id rows
	// are updated rather than deleted and re-created. This preserves FK references
	// in unification_rules (ON DELETE CASCADE) and any other dependent tables.
	var rows [][]interface{}
	incomingIDs := make([]string, 0, len(attrs))

	for _, attr := range attrs {
		canonicalJSON, _ := json.Marshal(attr.CanonicalValues)
		subAttrJSON, _ := json.Marshal(attr.SubAttributes)
		attrKey := extractClaimKeyFromURI(attr.AttributeName)
		attr.AttributeName = attrKey
		incomingIDs = append(incomingIDs, attr.AttributeID)

		rows = append(rows, []interface{}{
			orgID,
			attr.AttributeID,
			attr.AttributeName,
			attr.ValueType,
			attr.MergeStrategy,
			attr.Mutability,
			attr.ApplicationIdentifier,
			attr.MultiValued,
			string(canonicalJSON),
			string(subAttrJSON),
			attr.SCIMDialect,
			constants.IdentityAttributes,
			attr.DisplayName,
		})
	}

	batchSize := min(max(len(rows), 1), database.MaxRowsPerStatement(tx.DBType(), 13))
	for start := 0; start < len(rows); start += batchSize {
		tuples, valueArgs := valueTuples(rows[start:min(start+batchSize, len(rows))])
		upsertQuery := scripts.UpsertIdentityClaimsForProfileSchema.Format(tuples)
		if _, err = tx.ExecContext(ctx, upsertQuery, valueArgs...); err != nil {
			errorMsg := fmt.Sprintf(
				"Failed to upsert identity attributes of profile schema for organization: %s", orgID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.SyncProfileSchema.Code,
				Message:     errors.SyncProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
	}

	// Step 2: Delete only attributes that are no longer present in the identity server.
	// Using a targeted NOT IN delete instead of a blanket delete preserves rows (and
	// their dependent FK entries) for attributes that still exist.
	if len(incomingIDs) > 0 {
		notInPlaceholders := make([]string, len(incomingIDs))
		deleteArgs := make([]interface{}, 0, len(incomingIDs)+1)
		deleteArgs = append(deleteArgs, orgID)
		for i, id := range incomingIDs {
			notInPlaceholders[i] = fmt.Sprintf("$%d", i+2)
			deleteArgs = append(deleteArgs, id)
		}
		staleDeleteQuery := scripts.DeleteStaleIdentityClaimsForProfileSchema.Format(
			strings.Join(notInPlaceholders, ","))
		if _, err = tx.ExecContext(ctx, staleDeleteQuery, deleteArgs...); err != nil {
			errorMsg := fmt.Sprintf(
				"Failed to remove stale identity attributes of profile schema for organization: %s",
				orgID,
			)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.SyncProfileSchema.Code,
				Message:     errors.SyncProfileSchema.Message,
				Description: errorMsg,
			}, err)
		}
	}

	if err = tx.Commit(); err != nil {
		errorMsg := fmt.Sprintf("Failed to commit transaction for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.SyncProfileSchema.Code,
			Message:     errors.SyncProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	return nil
}

func extractClaimKeyFromURI(uri string) string {
	parts := strings.Split(strings.TrimRight(uri, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// GetProfileSchemaAttributesByScopeAndFilter returns the organization's schema attributes that match all
// filters. Filters in the form "field op value" with op eq, co or sw are applied and others are skipped. The
// field is put into the SQL as is, so callers must validate it. The scope is used only in error messages.
func GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
	orgID, scope string, filters []string) ([]model.ProfileSchemaAttribute, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Error occurred while initializing DB client for filtering profile schema attributes "+
				"for org: %s and scope: %s",
			orgID,
			scope,
		)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	// The dialect is needed here for the LIKE operator, not to select a
	// statement.
	dbType := dbClient.DBType()
	baseSQL := scripts.FilterProfileSchemaAttributes.GetQuery(dbType)
	likeOperator, err := scripts.LikeOperator(dbType)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to prepare the profile schema filter query for org: %s and scope: %s",
			orgID, scope)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}
	conditions := []string{}
	args := []interface{}{orgID}
	argID := 2

	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			continue
		}
		field, operator, value := parts[0], parts[1], parts[2]

		var clause string
		switch operator {
		case "eq":
			clause = fmt.Sprintf("%s = $%d", field, argID)
			args = append(args, value)
		case "co":
			clause = fmt.Sprintf("%s %s $%d", field, likeOperator, argID)
			args = append(args, "%"+value+"%")
		case "sw":
			clause = fmt.Sprintf("%s %s $%d", field, likeOperator, argID)
			args = append(args, value+"%")
		default:
			continue
		}
		conditions = append(conditions, clause)
		argID++
	}

	if len(conditions) > 0 {
		baseSQL += " AND " + strings.Join(conditions, " AND ")
	}

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.FilterProfileSchemaAttributes.WithSQL(baseSQL), args...)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute profile schema filter query for org: %s and scope: %s", orgID, scope)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GetProfileSchema.Code,
			Message:     errors.GetProfileSchema.Message,
			Description: errorMsg,
		}, err)
	}

	attributes := make([]model.ProfileSchemaAttribute, 0)
	for _, row := range results {
		attr := mapRowToProfileAttribute(row)
		attributes = append(attributes, attr)
	}

	return attributes, nil
}
