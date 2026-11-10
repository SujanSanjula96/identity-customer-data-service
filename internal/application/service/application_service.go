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
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// ApplicationServiceInterface defines the interface for the application service.
type ApplicationServiceInterface interface {
	ResolveAndRegisterApplication(ctx context.Context, appIdentifier string, orgId string) (bool, error)
	ResolveAppIdentifierByClientID(ctx context.Context, orgId string, clientID string) (string, error)
}

// ApplicationService is the default implementation of the ApplicationServiceInterface.
type ApplicationService struct{}

// GetApplicationService creates a new instance of ApplicationService.
func GetApplicationService() ApplicationServiceInterface {

	return &ApplicationService{}
}

// ResolveAndRegisterApplication validates the application in the identity server and persists its clientId.
func (as *ApplicationService) ResolveAndRegisterApplication(ctx context.Context,
	appIdentifier string, orgId string) (bool, error) {

	logger := log.GetLogger()
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgId)
	if err != nil {
		return false, err
	}
	app, exists, err := identityClient.GetApplication(appIdentifier, orgHandle)
	if err != nil {
		return false, err
	}
	if !exists {
		logger.Debug(fmt.Sprintf("Application '%s' does not exist in the identity server for organization '%s'",
			appIdentifier, orgId))
		return false, nil
	}

	// clientId is empty for SAML-only apps; the row is still persisted (with a NULL clientId) so the table
	// holds an entry for every application.
	if err := store.UpsertApplication(ctx, orgId, model.Application{
		AppID:    appIdentifier,
		OrgId:    orgId,
		ClientID: app.ClientId,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// ResolveAppIdentifierByClientID resolves an OAuth clientId to the app ID via the local store. A missing mapping
// returns an empty string with a nil error; a store failure is returned so callers can distinguish the two.
// If a sub organization has no mapping for the client ID, the ancestor organizations are checked,
// nearest first.
func (as *ApplicationService) ResolveAppIdentifierByClientID(ctx context.Context,
	orgId string, clientID string) (string, error) {

	orgIds, err := orgChain(ctx, orgId)
	if err != nil {
		return "", err
	}
	for _, chainOrgId := range orgIds {
		appID, err := store.GetAppIdentifierByClientID(ctx, chainOrgId, clientID)
		if err != nil || appID != "" {
			return appID, err
		}
	}
	return "", nil
}

// orgChain returns the IDs of the organization and its ancestors, nearest first.
func orgChain(ctx context.Context, orgId string) ([]string, error) {

	org, err := orgStore.GetOrganizationById(ctx, orgId)
	if err != nil {
		return nil, err
	}
	if org == nil || org.IsPrimary() {
		return []string{orgId}, nil
	}
	chain, err := orgStore.GetOrganizationChain(ctx, org.OrgId)
	if err != nil {
		return nil, err
	}
	orgIds := make([]string, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		orgIds = append(orgIds, chain[i].OrgId)
	}
	return orgIds, nil
}
