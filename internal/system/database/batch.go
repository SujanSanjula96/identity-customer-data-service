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

package database

import "math"

const (
	mssqlMaxParameters = 2100
	mssqlMaxValueRows  = 1000
)

// MaxRowsPerStatement returns how many rows of paramsPerRow parameters one
// multi-row statement can hold on dbType. A caller takes the smaller of this
// and the batch size it prefers. The datasources other than SQL Server have no
// limit a store's batch can reach, so they get math.MaxInt.
func MaxRowsPerStatement(dbType string, paramsPerRow int) int {
	if dbType == TypeOracle {
		return 1
	}
	if dbType != TypeMSSQL {
		return math.MaxInt
	}
	if paramsPerRow < 1 {
		paramsPerRow = 1
	}
	return min(mssqlMaxValueRows, mssqlMaxParameters/paramsPerRow)
}
