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

package config

import (
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// ResolveUnificationWorkers returns how many queued profiles one instance unifies at the same
// time. Zero, which is also what an omitted setting gives, returns the default of the datasource
// dataSourceType. A negative value is refused.
//
// The default is one on SQLite, which takes one writer at a time, so that further workers would
// only wait for each other, and constants.DefaultUnificationWorkers on a server database.
func ResolveUnificationWorkers(cfg IdentityResolutionConfig, dataSourceType string) (int, error) {
	if cfg.UnificationWorkers < 0 {
		return 0, fmt.Errorf("identity_resolution.unification_workers must not be negative, got %d",
			cfg.UnificationWorkers)
	}
	if cfg.UnificationWorkers == 0 {
		if database.ResolveType(dataSourceType) == database.TypeSQLite {
			return constants.DefaultSQLiteUnificationWorkers, nil
		}
		return constants.DefaultUnificationWorkers, nil
	}
	return cfg.UnificationWorkers, nil
}
