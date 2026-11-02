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

// Package identitysource resolves the organization that holds the identity attributes of an
// organization. A sub organization uses the identity attributes of its source organization.
package identitysource

import (
	"context"

	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
)

// HandleOf returns the handle of the organization that holds the identity attributes of the given
// organization.
func HandleOf(ctx context.Context, orgHandle string) string {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil || org.IsPrimary() {
		return orgHandle
	}
	primary, err := orgStore.GetOrganizationById(ctx, org.PrimaryOrgId)
	if err != nil || primary == nil {
		return orgHandle
	}
	return idp.GetAdapter().IdentityAttributeSourceHandle(orgHandle, primary.OrgHandle)
}

// IsInherited reports whether the organization inherits its identity attributes, and returns the
// source organization.
func IsInherited(ctx context.Context, orgHandle string) (string, bool) {

	source := HandleOf(ctx, orgHandle)
	return source, source != orgHandle
}
