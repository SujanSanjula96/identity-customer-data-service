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
	"time"

	"gopkg.in/yaml.v2"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// Test_ResolveShutdownGracePeriod covers the three answers the setting has.
func Test_ResolveShutdownGracePeriod(t *testing.T) {

	tests := []struct {
		name    string
		seconds int
		want    time.Duration
		wantErr bool
	}{
		{
			name:    "an omitted setting gives the application default",
			seconds: 0,
			want:    constants.DefaultShutdownGracePeriod,
		},
		{
			name:    "a positive setting gives that many seconds",
			seconds: 40,
			want:    40 * time.Second,
		},
		{
			name:    "one second is accepted",
			seconds: 1,
			want:    time.Second,
		},
		{
			name:    "a negative setting is refused",
			seconds: -1,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveShutdownGracePeriod(ShutdownConfig{GracePeriodSeconds: test.seconds})

			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %d seconds, got %s", test.seconds, got)
				}
				if got != 0 {
					t.Errorf("expected no duration with an error, got %s", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("got %s, want %s", got, test.want)
			}
		})
	}
}

// Test_ResolveShutdownGracePeriod_readsTheDefault checks the default itself, so
// that a change to the constant does not pass unnoticed.
func Test_ResolveShutdownGracePeriod_readsTheDefault(t *testing.T) {

	got, err := ResolveShutdownGracePeriod(ShutdownConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 25*time.Second {
		t.Errorf("the default shutdown grace period is %s, want 25s", got)
	}
}

// Test_ShutdownConfig_readsTheDeploymentKey checks the YAML tag against the key
// the shipped deployment.yaml uses. A wrong tag would leave the setting at zero
// and hide itself behind the default.
func Test_ShutdownConfig_readsTheDeploymentKey(t *testing.T) {

	const document = "shutdown:\n  grace_period_seconds: 40\n"

	var cfg Config
	if err := yaml.Unmarshal([]byte(document), &cfg); err != nil {
		t.Fatalf("failed to read the document: %v", err)
	}
	if cfg.Shutdown.GracePeriodSeconds != 40 {
		t.Fatalf("got %d seconds, want 40", cfg.Shutdown.GracePeriodSeconds)
	}

	grace, err := ResolveShutdownGracePeriod(cfg.Shutdown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grace != 40*time.Second {
		t.Errorf("got %s, want 40s", grace)
	}
}
