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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
)

// resolveOrgId returns the org ID of the organization, as a request handler resolves it. It stores the
// organization when it is not stored yet.
func resolveOrgId(t testing.TB, org string) string {

	t.Helper()
	ref, err := orgService.ResolveOrganization(context.Background(), org)
	require.NoError(t, err)
	return ref.ID
}
