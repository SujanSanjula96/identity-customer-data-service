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

// Package identitysource finds the org that stores the identity attributes of an org. A sub org
// inherits the identity attributes of its source org on read and stores none (R-017). Every reader
// of identity attributes uses this package, so that the source org is decided in one place.
package identitysource

import (
	"context"

	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
)

// HandleOf returns the handle of the org that stores the identity attributes of the org. It is
// the org itself for a root, and for an org that CDS does not know as a B2B org. When the org tree
// cannot be read, it returns the org itself, so a reader finds no attributes and fails closed.
func HandleOf(ctx context.Context, orgHandle string) string {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil || org.IsRoot() {
		return orgHandle
	}
	root, err := orgStore.GetOrganizationById(ctx, org.RootOrgId)
	if err != nil || root == nil {
		return orgHandle
	}
	return idp.GetAdapter().IdentityAttributeSourceHandle(orgHandle, root.OrgHandle)
}

// IsInherited reports whether the org inherits its identity attributes, and returns the source org.
func IsInherited(ctx context.Context, orgHandle string) (string, bool) {

	source := HandleOf(ctx, orgHandle)
	return source, source != orgHandle
}
