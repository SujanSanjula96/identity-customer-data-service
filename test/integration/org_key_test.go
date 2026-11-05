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
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
)

// orgSQL renders the org tokens of a statement that a test runs on suiteDB itself, for the
// org_id_migration phase of the suite.
func orgSQL(statement string) string {

	return model.RenderOrgTokens(statement, database.OrgPhaseFor(suiteDBType))
}

// writeOrgKey returns the org key that a statement of the suite's phase writes for the
// organization. It stores the organization when it is not stored yet, as a store does.
func writeOrgKey(t testing.TB, org string) string {

	t.Helper()
	key, err := orgkey.ForWrite(context.Background(), org)
	require.NoError(t, err)
	return key
}

// readOrgKey returns the org key that a statement of the suite's phase filters by.
func readOrgKey(t testing.TB, org string) string {

	t.Helper()
	key, _, err := orgkey.ForRead(context.Background(), org)
	require.NoError(t, err)
	return key
}
