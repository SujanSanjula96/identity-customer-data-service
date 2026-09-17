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
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// AddProfileSchemaAttributesForScope adds multiple profile schema attributes.
func AddProfileSchemaAttributesForScope(ctx context.Context,
	attrs []model.ProfileSchemaAttribute, scope, orgId string) error {

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := "Error occurred while initializing DB client for adding profile schema attributes"
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.ADD_PROFILE_SCHEMA.Code,
			Message:     errors.ADD_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	if len(attrs) == 0 {
		return nil // nothing to insert
	}

	valueStrings := make([]string, 0, len(attrs))
	valueArgs := make([]interface{}, 0, len(attrs)*12)

	for i, attr := range attrs {
		idx := i * 12
		subAttrsJSON, err := json.Marshal(attr.SubAttributes)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to marshal sub attributes for attribute %s", attr.AttributeId)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.ADD_PROFILE_SCHEMA.Code,
				Message:     errors.ADD_PROFILE_SCHEMA.Message,
				Description: errorMsg,
			}, err)
		}
		canonicalJSON, err := json.Marshal(attr.CanonicalValues)

		if err != nil {
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.ADD_PROFILE_SCHEMA.Code,
				Message:     errors.ADD_PROFILE_SCHEMA.Message,
				Description: fmt.Sprintf("Failed to marshal canonical values for attribute %s", attr.AttributeId),
			}, err)
		}

		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d,  $%d, $%d,  $%d, $%d, $%d, $%d) ",
			idx+1, idx+2, idx+3, idx+4, idx+5, idx+6, idx+7, idx+8, idx+9, idx+10, idx+11, idx+12))
		valueArgs = append(valueArgs, orgId, attr.AttributeId, attr.AttributeName, attr.ValueType,
			attr.MergeStrategy, attr.ApplicationIdentifier, attr.Mutability, attr.MultiValued, subAttrsJSON,
			canonicalJSON, scope, attr.DisplayName)

	}

	query := scripts.InsertProfileSchemaAttributesForScope.Append(strings.Join(valueStrings, ", "))
	_, err = dbClient.ExecuteQueryContext(ctx, query, valueArgs...)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile schema attributes for org: %s", attrs[0].OrgId)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.ADD_PROFILE_SCHEMA.Code,
			Message:     errors.ADD_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}
	logger.Info(fmt.Sprintf("Successfully added %d profile schema attributes for organization: %s",
		len(attrs), attrs[0].OrgId))
	return nil
}

// GetProfileSchemaAttributeById retrieves a profile schema attribute by its ID for a given organization.
func GetProfileSchemaAttributeById(ctx context.Context,
	orgId, attributeId string) (model.ProfileSchemaAttribute, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for org: %s and attribute: %s",
			orgId, attributeId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return model.ProfileSchemaAttribute{}, serverError
	}
	defer dbClient.Close()

	query := scripts.GetProfileSchemaAttributeById

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgId, attributeId)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for the org:%s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return model.ProfileSchemaAttribute{}, serverError
	}
	if len(results) == 0 {
		clientError := errors.NewClientError(errors.ErrorMessage{
			Code:        errors.ATTRIBUTE_NOT_FOUND.Code,
			Message:     errors.ATTRIBUTE_NOT_FOUND.Message,
			Description: "Profile schema attribute not found for org: " + orgId + " and attribute: " + attributeId,
		}, http.StatusNotFound)
		return model.ProfileSchemaAttribute{}, clientError
	}
	row := results[0]
	return mapRowToProfileAttribute(row), nil
}

// GetProfileSchemaAttributesByScope retrieves all profile schema attributes for a given organization and scope.
func GetProfileSchemaAttributesByScope(ctx context.Context,
	orgId, scope string) ([]model.ProfileSchemaAttribute, error) {

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Error initializing DB client for org: %s and scope: %s", orgId, scope)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetProfileSchemaAttributeByScope

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgId, scope)
	if err != nil {
		errorMsg := fmt.Sprintf("Error fetching profile schema attributes for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}

	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile schema attributes found for org: %s and scope: %s", orgId, scope))
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
	orgId, attributeName string) (*model.ProfileSchemaAttribute, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()

	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for fetching schema attribute: %s", attributeName)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetProfileSchemaAttributeByName

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgId, attributeName)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed in fetching schema attribute '%s' for organization '%s'", attributeName,
			orgId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile schema found for attribute: %s in org: %s", attributeName, orgId))
		return nil, nil
	}

	row := results[0]
	var subAttrs []model.SubAttribute
	if raw, ok := row["sub_attributes"].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &subAttrs); err != nil {
			errorMsg := fmt.Sprintf("Failed to unmarshal sub_attributes for attribute '%s' in org '%s'",
				attributeName, orgId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors.NewServerError(errors.ErrorMessage{
				Code:        errors.GET_PROFILE_SCHEMA.Code,
				Message:     errors.GET_PROFILE_SCHEMA.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
	}

	var canonicalValues []model.CanonicalValue
	if raw, ok := row["canonical_values"].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &canonicalValues); err != nil {
			errorMsg := fmt.Sprintf("Failed to unmarshal canonical_values for attribute '%s' in organization '%s'",
				attributeName, orgId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors.NewServerError(errors.ErrorMessage{
				Code:        errors.GET_PROFILE_SCHEMA.Code,
				Message:     errors.GET_PROFILE_SCHEMA.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
	}

	attr := &model.ProfileSchemaAttribute{
		OrgId:                 orgId,
		AttributeName:         row["attribute_name"].(string),
		DisplayName:           row["display_name"].(string),
		AttributeId:           row["attribute_id"].(string),
		ValueType:             row["value_type"].(string),
		MergeStrategy:         row["merge_strategy"].(string),
		Mutability:            row["mutability"].(string),
		ApplicationIdentifier: row["application_identifier"].(string),
		MultiValued:           row["multi_valued"].(bool),
		SubAttributes:         subAttrs,
		CanonicalValues:       canonicalValues,
	}

	logger.Info(fmt.Sprintf("Successfully fetched profile schema attribute '%s' for organizaton '%s'",
		attributeName, orgId))
	return attr, nil
}

// GetProfileSchemaAttributesForOrg retrieves all profile schema attributes for a given organization.
func GetProfileSchemaAttributesForOrg(ctx context.Context, orgId string) ([]model.ProfileSchemaAttribute, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetProfileSchemaByOrg

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgId)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while fetching profile schema for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
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

// PatchProfileSchemaAttributeById updates a specific profile schema attribute for a given organization.
func PatchProfileSchemaAttributeById(ctx context.Context,
	orgId, attributeId string, updates map[string]interface{}) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while patching profile schema for org: %s and attribute: %s", orgId, attributeId)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
			Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	setClauses := []string{}
	args := []interface{}{}
	argIndex := 1

	for key, value := range updates {
		switch v := value.(type) {
		case []interface{}, map[string]interface{}:
			// Marshal slices/maps to JSON
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				errorMsg := fmt.Sprintf("Error marshalling value for key '%s' in profile schema update for org: %s", key, orgId)
				logger.Debug(errorMsg, log.Error(err))
				return errors.NewServerError(errors.ErrorMessage{
					Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
					Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
					Description: errorMsg,
				}, err)
			}
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", key, argIndex))
			args = append(args, string(jsonBytes))

		case nil:
			// Skip nil values to avoid null pointer issues
			continue

		default:
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", key, argIndex))
			args = append(args, v)
		}
		argIndex++
	}

	// Append WHERE clause parameters
	args = append(args, orgId, attributeId)

	query := scripts.UpdateProfileSchemaAttributeFields.Append(strings.Join(setClauses, ", ") +
		` WHERE org_handle = $` + strconv.Itoa(argIndex) + ` AND attribute_id = $` + strconv.Itoa(argIndex+1))

	_, err = dbClient.ExecuteQueryContext(ctx, query, args...)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while executing update for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
			Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}

	return nil
}

// DeleteProfileSchemaAttributeById deletes a specific profile schema attribute by its ID for a given organization.
func DeleteProfileSchemaAttributeById(ctx context.Context, orgId, attributeId string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting profile schema for org: %s and attribute: %s", orgId, attributeId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors.DELETE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.DeleteProfileSchemaAttributeById
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgId, attributeId)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting profile schema attribute with id: %s", attributeId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors.DELETE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	logger.Info(fmt.Sprintf("Profile schema attribute: %s deleted: ", attributeId))
	return nil
}

// DeleteProfileSchemaAttributes deletes all profile schema attributes for a given organization and scope.
func DeleteProfileSchemaAttributes(ctx context.Context, orgId, scope string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting profile schema for org: %s and attribute: %s", orgId, scope)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors.DELETE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.DeleteProfileSchemaAttributeForScope
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgId, scope)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting profile schema attribute with scope: %s", scope)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors.DELETE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	logger.Info(fmt.Sprintf("Profile schema attributes with scope:%s deleted for the organization:%s ", scope, orgId))
	return nil
}

func PatchProfileSchemaAttributesForScope(ctx context.Context,
	orgId string, scope string, updates []model.ProfileSchemaAttribute) error {

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Error initializing DB client for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
			Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for update of profile schema attributes for organization: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
			Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}

	// Harmless after a successful Commit, which leaves the transaction done.
	// On every other exit it releases the connection at once, rather than at
	// the transaction deadline.
	defer func() { _ = tx.Rollback() }()

	stmt := scripts.UpdateProfileSchemaAttributesForSchema

	for _, attr := range updates {
		subAttrsJSON, err := json.Marshal(attr.SubAttributes)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to marshal sub attributes for attribute %s", attr.AttributeId)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
				Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
				Description: errorMsg,
			}, err)
		}
		canonicalJSON, err := json.Marshal(attr.CanonicalValues)

		if err != nil {
			errorMsg := fmt.Sprintf("Failed to marshal canonical values for attribute %s", attr.AttributeId)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
				Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
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
			canonicalJSON,
			subAttrsJSON,
			attr.DisplayName,
			orgId,
			attr.AttributeId,
			scope,
		}

		if _, err := tx.Exec(stmt, args...); err != nil {
			err := tx.Rollback()
			if err != nil {
				errorMsg := fmt.Sprintf("Failed to rollback updating attribute %s for organization %s", attr.AttributeId, orgId)
				logger.Debug(errorMsg, log.Error(err))
				return errors.NewServerError(errors.ErrorMessage{
					Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
					Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
					Description: errorMsg,
				}, err)
			}
			errorMsg := fmt.Sprintf("Failed to update attribute %s for organization %s", attr.AttributeId, orgId)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
				Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
				Description: errorMsg,
			}, err)
		}
	}

	if err := tx.Commit(); err != nil {
		errorMsg := fmt.Sprintf("Failed to commit transaction for updating profile schema attributes for organization: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
			Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}

	logger.Info(fmt.Sprintf("Update completed for %d profile schema attributes of scope: %s of organization: %s )", len(updates), scope, orgId))
	return nil
}

// DeleteProfileSchema deletes all profile schema attributes for a given organization.
func DeleteProfileSchema(ctx context.Context, orgId string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting all of profile schema attributes for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors.DELETE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.DeleteProfileSchemaForOrg
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgId)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while deleting all of profile schema attributes for org: %s", orgId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.DELETE_PROFILE_SCHEMA.Code,
			Message:     errors.DELETE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	logger.Info("Profile schema cleared for org: " + orgId)
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
		AttributeId:           fmt.Sprint(row["attribute_id"]),
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

func UpsertIdentityAttributes(ctx context.Context, orgID string, attrs []model.ProfileSchemaAttribute) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error initializing DB client for organization: %s ", orgID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors.NewServerError(errors.ErrorMessage{
			Code:        errors.UPDATE_PROFILE_SCHEMA.Code,
			Message:     errors.UPDATE_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.SYNC_PROFILE_SCHEMA.Code,
			Message:     errors.SYNC_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}

	// Harmless after a successful Commit, which leaves the transaction done.
	// On every other exit it releases the connection at once, rather than at
	// the transaction deadline.
	defer func() { _ = tx.Rollback() }()

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Step 1: Upsert incoming attributes in-place so that existing attribute_id rows
	// are updated rather than deleted and re-created. This preserves FK references
	// in unification_rules (ON DELETE CASCADE) and any other dependent tables.
	var valueStrings []string
	var valueArgs []interface{}
	argIndex := 1
	incomingIDs := make([]string, 0, len(attrs))

	for _, attr := range attrs {
		canonicalJSON, _ := json.Marshal(attr.CanonicalValues)
		subAttrJSON, _ := json.Marshal(attr.SubAttributes)
		attrKey := extractClaimKeyFromURI(attr.AttributeName)
		attr.AttributeName = attrKey
		incomingIDs = append(incomingIDs, attr.AttributeId)

		valueStrings = append(valueStrings, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			argIndex, argIndex+1, argIndex+2, argIndex+3, argIndex+4, argIndex+5, argIndex+6,
			argIndex+7, argIndex+8, argIndex+9, argIndex+10, argIndex+11, argIndex+12))
		valueArgs = append(valueArgs,
			orgID,
			attr.AttributeId,
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
		)
		argIndex += 13
	}

	upsertQuery := scripts.UpsertIdentityClaimsForProfileSchema.Format(strings.Join(valueStrings, ","))
	if _, err = tx.Exec(upsertQuery, valueArgs...); err != nil {
		errorMsg := fmt.Sprintf("Failed to upsert identity attributes of profile schema for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.SYNC_PROFILE_SCHEMA.Code,
			Message:     errors.SYNC_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
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
		if _, err = tx.Exec(staleDeleteQuery, deleteArgs...); err != nil {
			errorMsg := fmt.Sprintf("Failed to remove stale identity attributes of profile schema for organization: %s", orgID)
			logger.Debug(errorMsg, log.Error(err))
			return errors.NewServerError(errors.ErrorMessage{
				Code:        errors.SYNC_PROFILE_SCHEMA.Code,
				Message:     errors.SYNC_PROFILE_SCHEMA.Message,
				Description: errorMsg,
			}, err)
		}
	}

	if err = tx.Commit(); err != nil {
		errorMsg := fmt.Sprintf("Failed to commit transaction for organization: %s", orgID)
		logger.Debug(errorMsg, log.Error(err))
		return errors.NewServerError(errors.ErrorMessage{
			Code:        errors.SYNC_PROFILE_SCHEMA.Code,
			Message:     errors.SYNC_PROFILE_SCHEMA.Message,
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

func GetProfileSchemaAttributesByScopeAndFilter(ctx context.Context,
	orgId, scope string, filters []string) ([]model.ProfileSchemaAttribute, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while initializing DB client for filtering profile schema attributes for org: %s and scope: %s", orgId, scope)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	// The dialect is needed here for the LIKE operator, not to select a
	// statement.
	dbType := dbClient.DBType()
	baseSQL := scripts.FilterProfileSchemaAttributes.GetQuery(dbType)
	likeOperator := scripts.LikeOperator(dbType)
	conditions := []string{}
	args := []interface{}{orgId}
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
		errorMsg := fmt.Sprintf("Failed to execute profile schema filter query for org: %s and scope: %s", orgId, scope)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors.NewServerError(errors.ErrorMessage{
			Code:        errors.GET_PROFILE_SCHEMA.Code,
			Message:     errors.GET_PROFILE_SCHEMA.Message,
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
