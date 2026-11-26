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

package database

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
	mssql "github.com/microsoft/go-mssqldb"
)

func TestIsDuplicateKey(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		duplicate bool
	}{
		"nil":                          {nil, false},
		"other error":                  {errors.New("boom"), false},
		"mssql primary key violation":  {mssql.Error{Number: 2627}, true},
		"mssql unique index violation": {mssql.Error{Number: 2601}, true},
		"mssql deadlock victim":        {mssql.Error{Number: 1205}, false},
		"mssql foreign key violation":  {mssql.Error{Number: 547}, false},
		"wrapped mssql violation":      {fmt.Errorf("query CDS-IDR-05 failed: %w", mssql.Error{Number: 2627}), true},
		"postgres unique violation":    {&pq.Error{Code: "23505"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsDuplicateKey(tc.err); got != tc.duplicate {
				t.Errorf("IsDuplicateKey(%v) = %v, want %v", tc.err, got, tc.duplicate)
			}
		})
	}
}
