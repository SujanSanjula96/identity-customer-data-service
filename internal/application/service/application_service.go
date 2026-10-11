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
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
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
// In a sub org, the app can be an app of the sub org or an app of a parent org, which the sub org can get
// through a share. CDS looks for it in the org first, and then in each parent org up to the root. A shared
// app of the sub org (a fragment) is not valid: the key of its data is the ID of the main app (R-019). CDS
// registers the app with the org that owns it.
func (as *ApplicationService) ResolveAndRegisterApplication(ctx context.Context,
	appIdentifier, orgHandle string) (bool, error) {

	logger := log.GetLogger()
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)

	targets, err := orgChain(ctx, orgHandle)
	if err != nil {
		return false, err
	}
	for _, target := range targets {
		app, exists, err := identityClient.GetApplicationIn(appIdentifier, target)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		if app.IsFragment() {
			logger.Debug(fmt.Sprintf("Application '%s' is a shared app in organization '%s'. Use the ID of "+
				"the main application.", appIdentifier, target.Handle))
			return false, nil
		}
		// clientId is empty for SAML-only apps; the row is still persisted (with a NULL clientId) so the table
		// holds an entry for every application.
		if err := store.UpsertApplication(ctx, model.Application{
			AppID:     appIdentifier,
			OrgHandle: target.Handle,
			ClientID:  app.ClientId,
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	logger.Debug(fmt.Sprintf("Application '%s' does not exist in the identity server for organization '%s' "+
		"or its parent organizations", appIdentifier, orgHandle))
	return false, nil
}

// ResolveAppIdentifierByClientID resolves an OAuth clientId to the app ID via the local store. A missing mapping
// returns an empty string with a nil error; a store failure is returned so callers can distinguish the two.
// In a sub org, the token of a shared app carries the clientId of the main app, and CDS keys the data by
// the main app (R-016). So a clientId that the sub org does not have is looked up in each parent org up to
// the root (R-019).
func (as *ApplicationService) ResolveAppIdentifierByClientID(ctx context.Context,
	orgHandle, clientID string) (string, error) {

	targets, err := orgChain(ctx, orgHandle)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		appID, err := store.GetAppIdentifierByClientID(ctx, target.Handle, clientID)
		if err != nil || appID != "" {
			return appID, err
		}
	}
	return "", nil
}

// orgChain returns the org and its parent orgs up to the root, nearest first. An org that CDS does not
// know as a B2B org is its own chain.
func orgChain(ctx context.Context, orgHandle string) ([]client.OrgTarget, error) {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil || org.IsRoot() {
		return []client.OrgTarget{{Handle: orgHandle}}, nil
	}
	root, err := orgStore.GetOrganizationById(ctx, org.RootOrgId)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return []client.OrgTarget{{Handle: orgHandle}}, nil
	}
	var chain []client.OrgTarget
	for current := org; current != nil && !current.IsRoot(); {
		chain = append(chain, client.OrgTarget{Handle: current.OrgHandle, OrgId: current.OrgId,
			RootHandle: root.OrgHandle})
		if current, err = orgStore.GetOrganizationById(ctx, current.ParentOrgId); err != nil {
			return nil, err
		}
	}
	return append(chain, client.OrgTarget{Handle: root.OrgHandle}), nil
}
