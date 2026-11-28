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
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func TestResolveUnificationWorkers(t *testing.T) {
	cases := []struct {
		name       string
		workers    int
		datasource string
		want       int
		wantErr    bool
	}{
		{name: "an absent key takes the default", workers: 0, datasource: "postgres",
			want: constants.DefaultUnificationWorkers},
		{name: "an absent key takes one worker on SQLite", workers: 0, datasource: "sqlite",
			want: constants.DefaultSQLiteUnificationWorkers},
		{name: "an absent datasource is SQLite", workers: 0, datasource: "",
			want: constants.DefaultSQLiteUnificationWorkers},
		{name: "a set value is kept", workers: 1, datasource: "postgres", want: 1},
		{name: "a larger value is kept", workers: 20, datasource: "postgres", want: 20},
		{name: "a set value is kept on SQLite", workers: 3, datasource: "sqlite", want: 3},
		{name: "a negative value is refused", workers: -1, datasource: "postgres", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveUnificationWorkers(IdentityResolutionConfig{UnificationWorkers: tc.workers},
				tc.datasource)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error for %d workers, got %d", tc.workers, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d workers, want %d", got, tc.want)
			}
		})
	}
}
