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

package handler

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

func withFuzzyMatching(t *testing.T, enabled bool) {
	t.Helper()

	conf := config.Config{}
	conf.IdentityResolution.FuzzyMatchingEnabled = enabled
	config.OverrideCDSRuntime(conf)
}

// TestFuzzyMatchingDefaultsToOff pins the upgrade behaviour. A deployment.yaml written
// before this key existed must keep unifying profiles exactly as it did, so an upgrade
// cannot change which profiles get merged until an operator asks for it.
func TestFuzzyMatchingDefaultsToOff(t *testing.T) {
	config.OverrideCDSRuntime(config.Config{})

	if fuzzyMatchingAllowed() {
		t.Error("an absent fuzzy_matching_enabled must read as disabled")
	}
}

func TestFuzzyMatchingHonoursAnExplicitSetting(t *testing.T) {
	for _, want := range []bool{true, false} {
		withFuzzyMatching(t, want)

		if got := fuzzyMatchingAllowed(); got != want {
			t.Errorf("fuzzy_matching_enabled=%v read as %v", want, got)
		}
	}
}

// TestFuzzyIsRefusedNotDowngraded is the point of refusing rather than quietly storing the
// rule as exact: a caller that believes it configured a rule to tolerate typos, and is
// overruled without being told, would misread every merge that rule takes part in.
func TestFuzzyIsRefusedNotDowngraded(t *testing.T) {
	withFuzzyMatching(t, false)

	if err := refuseFuzzy(); err == nil {
		t.Fatal("expected a refusal for fuzzy while the server has it switched off")
	}
}
