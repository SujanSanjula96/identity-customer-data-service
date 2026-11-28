/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
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

// Package identitysource resolves the organization that holds the identity attributes of an
// organization. A sub organization uses the identity attributes of its source organization.
package identitysource

import (
	"context"

	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
)

// Of returns the ID of the organization that holds the identity attributes of the given organization.
func Of(ctx context.Context, orgID string) string {
	stored, err := orgStore.GetOrganizationByID(ctx, orgID)
	if err != nil || stored == nil || stored.IsPrimary() {
		return orgID
	}
	primary, err := orgStore.GetOrganizationByID(ctx, stored.PrimaryOrgID)
	if err != nil || primary == nil {
		return orgID
	}
	if idp.GetAdapter().IdentityAttributeSourceHandle(stored.OrgHandle, primary.OrgHandle) != primary.OrgHandle {
		return orgID
	}
	return primary.OrgID
}

// IsInherited reports whether the organization inherits its identity attributes, and returns the ID
// of the source organization.
func IsInherited(ctx context.Context, orgID string) (string, bool) {
	source := Of(ctx, orgID)
	return source, source != orgID
}
