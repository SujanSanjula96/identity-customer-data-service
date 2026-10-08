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
	"sync"
	"time"

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
// In a sub org, the token of a shared app carries the clientId of the main app, and CDS keys the data by
// the main app (R-016). So a clientId that the sub org does not have is looked up in the root.
func (as *ApplicationService) ResolveAppIdentifierByClientID(ctx context.Context,
	orgHandle, clientID string) (string, error) {

	appID, err := store.GetAppIdentifierByClientID(ctx, orgHandle, clientID)
	if err != nil || appID != "" {
		return appID, err
	}
	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil || org.IsRoot() {
		return "", err
	}
	root, err := orgStore.GetOrganizationById(ctx, org.RootOrgId)
	if err != nil || root == nil {
		return "", err
	}
	return store.GetAppIdentifierByClientID(ctx, root.OrgHandle, clientID)
}

// appReachTTL is how long CDS keeps the orgs where IS shares an app.
const appReachTTL = 2 * time.Minute

type appReachEntry struct {
	orgIds    map[string]bool
	fetchedAt time.Time
}

var (
	appReachMu    sync.Mutex
	appReachCache = map[string]appReachEntry{}
)

// AppReach returns the IDs of the orgs where IS shares the app. The app belongs to the root of the
// handle, and appIdentifier is its identifier in the root: the clientId or issuer, or the app ID in
// app_id mode. CDS reads GET /applications/{id}/shared-apps in the root and keeps the result for
// appReachTTL (R-016). When IS fails, the last result is used if CDS has one.
func AppReach(ctx context.Context, rootHandle, appIdentifier string) (map[string]bool, error) {

	key := rootHandle + "|" + appIdentifier
	appReachMu.Lock()
	entry, cached := appReachCache[key]
	appReachMu.Unlock()
	if cached && time.Since(entry.fetchedAt) < appReachTTL {
		return entry.orgIds, nil
	}
	orgIds, err := fetchAppReach(rootHandle, appIdentifier)
	if err != nil {
		if cached {
			log.GetLogger().Warn(fmt.Sprintf("Could not refresh the orgs of app '%s' in '%s'. Using the last "+
				"result.", appIdentifier, rootHandle), log.Error(err))
			return entry.orgIds, nil
		}
		return nil, err
	}
	appReachMu.Lock()
	appReachCache[key] = appReachEntry{orgIds: orgIds, fetchedAt: time.Now()}
	appReachMu.Unlock()
	return orgIds, nil
}

func fetchAppReach(rootHandle, appIdentifier string) (map[string]bool, error) {

	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)
	appID := appIdentifier
	if !cfg.UsesAppIDIdentifier() {
		apps, err := identityClient.FetchApplicationIdentifier(appIdentifier, rootHandle)
		if err != nil {
			return nil, err
		}
		if len(apps.Applications) != 1 {
			return map[string]bool{}, nil
		}
		appID = apps.Applications[0].ID
	}
	shared, exists, err := identityClient.GetSharedApplications(appID, rootHandle)
	if err != nil {
		return nil, err
	}
	orgIds := map[string]bool{}
	if !exists {
		return orgIds, nil
	}
	for _, app := range shared.SharedApplications {
		orgIds[app.OrganizationId] = true
	}
	return orgIds, nil
}
