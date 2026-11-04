/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
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
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/application/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// UpsertApplication persists the application information, refreshing the clientId on re-validation.
func UpsertApplication(ctx context.Context, app model.Application) error {

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for persisting application: %s", app.AppID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPSERT_APPLICATION_FAILED.Code,
			Message:     errors2.UPSERT_APPLICATION_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	// SAML-only apps have no clientId; store NULL so they don't collide on the (org_handle, client_id) unique key.
	var clientID interface{}
	if app.ClientID != "" {
		clientID = app.ClientID
	}

	query := scripts.UpsertApplication
	_, err = dbClient.ExecuteQueryContext(ctx, query, app.AppID, app.OrgHandle, clientID)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while persisting application: %s", app.AppID)
		logger.Debug(errorMsg, log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPSERT_APPLICATION_FAILED.Code,
			Message:     errors2.UPSERT_APPLICATION_FAILED.Message,
			Description: errorMsg,
		}, err)
	}

	logger.Info(fmt.Sprintf("Application information persisted for app_id: %s", app.AppID))
	return nil
}

// GetAppIdentifierByClientID resolves an OAuth clientId to the app ID.
func GetAppIdentifierByClientID(ctx context.Context, orgHandle, clientID string) (string, error) {

	logger := log.GetLogger()
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for resolving clientId for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APPLICATION_FAILED.Code,
			Message:     errors2.GET_APPLICATION_FAILED.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	query := scripts.GetAppIdentifierByClientID
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, clientID)
	if err != nil {
		errorMsg := fmt.Sprintf("Error occurred while resolving clientId for organization: %s", orgHandle)
		logger.Debug(errorMsg, log.Error(err))
		return "", errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APPLICATION_FAILED.Code,
			Message:     errors2.GET_APPLICATION_FAILED.Message,
			Description: errorMsg,
		}, err)
	}

	if len(results) == 0 {
		return "", nil
	}
	appID, _ := results[0]["app_id"].(string)
	return appID, nil
}
