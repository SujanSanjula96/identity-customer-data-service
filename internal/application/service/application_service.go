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

package service

import (
	"context"
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/application/model"
	"github.com/wso2/identity-customer-data-service/internal/application/store"
	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// ApplicationServiceInterface defines the interface for the application service.
type ApplicationServiceInterface interface {
	ResolveAndRegisterApplication(ctx context.Context, appIdentifier, orgHandle string) (bool, error)
	ResolveAppIdentifierByClientID(ctx context.Context, orgHandle, clientID string) (string, error)
}

// ApplicationService is the default implementation of the ApplicationServiceInterface.
type ApplicationService struct{}

// GetApplicationService creates a new instance of ApplicationService.
func GetApplicationService() ApplicationServiceInterface {

	return &ApplicationService{}
}

// ResolveAndRegisterApplication validates the application in the identity server and persists its clientId.
func (as *ApplicationService) ResolveAndRegisterApplication(ctx context.Context,
	appIdentifier, orgHandle string) (bool, error) {

	logger := log.GetLogger()
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	app, exists, err := identityClient.GetApplication(appIdentifier, orgHandle)
	if err != nil {
		return false, err
	}
	if !exists {
		logger.Debug(fmt.Sprintf("Application '%s' does not exist in the identity server for organization '%s'",
			appIdentifier, orgHandle))
		return false, nil
	}

	// clientId is empty for SAML-only apps; the row is still persisted (with a NULL clientId) so the table
	// holds an entry for every application.
	if err := store.UpsertApplication(ctx, model.Application{
		AppID:     appIdentifier,
		OrgHandle: orgHandle,
		ClientID:  app.ClientId,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// ResolveAppIdentifierByClientID resolves an OAuth clientId to the app ID via the local store. A missing mapping
// returns an empty string with a nil error; a store failure is returned so callers can distinguish the two.
func (as *ApplicationService) ResolveAppIdentifierByClientID(ctx context.Context,
	orgHandle, clientID string) (string, error) {

	return store.GetAppIdentifierByClientID(ctx, orgHandle, clientID)
}
