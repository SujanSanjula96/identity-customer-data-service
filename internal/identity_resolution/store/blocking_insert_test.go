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

package store

import (
	"errors"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func TestExecBlockingKeyInsert(t *testing.T) {
	duplicate := mssql.Error{Number: 2627}
	other := errors.New("boom")

	for name, tc := range map[string]struct {
		results       []error
		wantOther     bool
		wantDuplicate bool
		wantCalls     int
	}{
		"succeeds at once":             {results: []error{nil}, wantCalls: 1},
		"runs again after a duplicate": {results: []error{duplicate, nil}, wantCalls: 2},
		"returns other errors at once": {results: []error{other}, wantOther: true, wantCalls: 1},
		"gives up after the attempts": {
			results:       []error{duplicate, duplicate, duplicate, nil},
			wantDuplicate: true,
			wantCalls:     blockingKeyInsertAttempts,
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			err := execBlockingKeyInsert(func() error {
				result := tc.results[calls]
				calls++
				return result
			})
			if errors.Is(err, other) != tc.wantOther || database.IsDuplicateKey(err) != tc.wantDuplicate ||
				(err == nil) != (!tc.wantOther && !tc.wantDuplicate) {
				t.Errorf("error = %v, want other=%v duplicate=%v", err, tc.wantOther, tc.wantDuplicate)
			}
			if calls != tc.wantCalls {
				t.Errorf("ran %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}
