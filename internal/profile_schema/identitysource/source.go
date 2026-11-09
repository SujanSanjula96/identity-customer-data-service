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

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
)

// Of returns the organization that holds the identity attributes of the given organization.
func Of(ctx context.Context, org orgModel.Ref) orgModel.Ref {

	stored, err := orgStore.GetOrganizationById(ctx, org.ID)
	if err != nil || stored == nil || stored.IsPrimary() {
		return org
	}
	primary, err := orgStore.GetOrganizationById(ctx, stored.PrimaryOrgId)
	if err != nil || primary == nil {
		return org
	}
	if idp.GetAdapter().IdentityAttributeSourceHandle(org.Handle, primary.OrgHandle) != primary.OrgHandle {
		return org
	}
	return orgModel.Ref{ID: primary.OrgId, Handle: primary.OrgHandle}
}

// IsInherited reports whether the organization inherits its identity attributes, and returns the
// source organization.
func IsInherited(ctx context.Context, org orgModel.Ref) (orgModel.Ref, bool) {

	source := Of(ctx, org)
	return source, source.ID != org.ID
}
