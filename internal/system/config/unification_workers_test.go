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

package config

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func TestResolveUnificationWorkers(t *testing.T) {
	cases := []struct {
		name    string
		workers int
		want    int
		wantErr bool
	}{
		{name: "an absent key takes the default", workers: 0, want: constants.DefaultUnificationWorkers},
		{name: "a set value is kept", workers: 1, want: 1},
		{name: "a larger value is kept", workers: 20, want: 20},
		{name: "a negative value is refused", workers: -1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveUnificationWorkers(IdentityResolutionConfig{UnificationWorkers: tc.workers})
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
