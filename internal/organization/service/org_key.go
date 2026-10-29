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
	"errors"
	"net/http"

	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
)

// RegisterOrgKeyResolver installs ResolveOrganization as the org key resolver of the stores, and
// the identity provider lookup of the inbuilt database migration. It applies whether organization
// management is enabled or not: every deployment keys its data by org ID.
func RegisterOrgKeyResolver() {

	orgkey.SetResolver(ResolveOrganization)
	provider.SetPrimaryOrganizationLookup(lookupPrimaryOrganization)
}

// ResolveOrganization returns the stored organization of the handle. When store is true and the
// handle has no row, it reads the primary organization from the identity provider and stores it,
// as GetOrStoreOrganization does. found is false when the identity provider does not know the
// handle as a primary organization.
func ResolveOrganization(ctx context.Context, handle string, storeIt bool) (orgkey.Ref, bool, error) {

	org, err := store.LookupOrganizationByHandle(ctx, handle)
	if err != nil {
		return orgkey.Ref{}, false, err
	}
	if org != nil {
		return orgkey.Ref{ID: org.OrgId, Handle: org.OrgHandle}, true, nil
	}
	if !storeIt {
		return orgkey.Ref{}, false, nil
	}

	primary, err := EnsurePrimary(ctx, handle)
	if isNotFound(err) {
		return orgkey.Ref{}, false, nil
	}
	if err != nil {
		return orgkey.Ref{}, false, err
	}
	return orgkey.Ref{ID: primary.OrgId, Handle: primary.OrgHandle}, true, nil
}

// lookupPrimaryOrganization reads the primary organization of the handle from the identity
// provider, without storing it.
func lookupPrimaryOrganization(ctx context.Context, handle string) (provider.PrimaryOrganization, bool, error) {

	info, err := idp.GetAdapter().GetPrimaryOrganization(ctx, handle)
	if isNotFound(err) {
		return provider.PrimaryOrganization{}, false, nil
	}
	if err != nil {
		return provider.PrimaryOrganization{}, false, err
	}
	return provider.PrimaryOrganization{Id: info.Id, Name: info.Name}, true, nil
}

func isNotFound(err error) bool {

	var clientError *errors2.ClientError
	return errors.As(err, &clientError) && clientError.StatusCode == http.StatusNotFound
}
