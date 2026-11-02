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
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func withOverrideAllowed(t *testing.T, allowed bool) {
	t.Helper()

	conf := config.Config{}
	conf.IdentityResolution.AllowEvidenceStrengthOverride = allowed
	config.OverrideCDSRuntime(conf)
}

// TestEvidenceStrengthStoresNothingWhenNotSupplied covers the ordinary path: a rule that
// says nothing about strengths stores nothing, so the value is derived from its attribute
// type on every read rather than frozen at creation time.
func TestEvidenceStrengthStoresNothingWhenNotSupplied(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		withOverrideAllowed(t, allowed)

		strength, err := resolveEvidenceStrength("", "match_strength")
		if err != nil {
			t.Fatalf("override allowed=%v: unexpected error: %v", allowed, err)
		}
		if strength != "" {
			t.Errorf("override allowed=%v: stored %q, want nothing stored", allowed, strength)
		}
	}
}

// TestStrengthIsNotExposedWhileOverrideDisabled is the read half of the same setting. A
// server that refuses the field on the way in must not describe it on the way out, or a
// client reads a value it has no way to change and which is really a function of
// attribute_type.
func TestStrengthIsNotExposedWhileOverrideDisabled(t *testing.T) {
	withOverrideAllowed(t, false)

	// Even a rule carrying a stored override — set while the flag was on, or seeded
	// directly — stays out of the response while the setting is off.
	if got := exposedStrength(constants.EvidenceStrengthLow); got != "" {
		t.Errorf("exposedStrength = %q, want nothing exposed", got)
	}
}

// TestOnlyAStoredStrengthIsExposed covers the enabled server. An inherited strength is
// reported as absent rather than as its derived value, so a caller can tell a deliberate
// choice from an inherited one and can send the response back without freezing the rule
// against a later change to the derivation table.
func TestOnlyAStoredStrengthIsExposed(t *testing.T) {
	withOverrideAllowed(t, true)

	if got := exposedStrength(constants.EvidenceStrengthLow); got != constants.EvidenceStrengthLow {
		t.Errorf("exposedStrength(override) = %q, want %q", got, constants.EvidenceStrengthLow)
	}
	if got := exposedStrength(""); got != "" {
		t.Errorf("exposedStrength(inherited) = %q, want nothing exposed", got)
	}
}

// TestEvidenceStrengthRefusedWhenOverrideDisabled is the point of the setting: a supplied
// strength is rejected rather than quietly replaced by the default, so a caller cannot
// believe it configured something the engine is not using.
func TestEvidenceStrengthRefusedWhenOverrideDisabled(t *testing.T) {
	withOverrideAllowed(t, false)

	_, err := resolveEvidenceStrength(constants.EvidenceStrengthLow, "match_strength")
	if err == nil {
		t.Fatal("expected a supplied strength to be refused while overrides are disabled")
	}
}

func TestEvidenceStrengthAcceptedWhenOverrideEnabled(t *testing.T) {
	withOverrideAllowed(t, true)

	strength, err := resolveEvidenceStrength(constants.EvidenceStrengthLow, "match_strength")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strength != constants.EvidenceStrengthLow {
		t.Errorf("match_strength = %q, want %q", strength, constants.EvidenceStrengthLow)
	}
}

func TestEvidenceStrengthRejectsUnknownValue(t *testing.T) {
	withOverrideAllowed(t, true)

	if _, err := resolveEvidenceStrength("VERY_HIGH", "match_strength"); err == nil {
		t.Error("expected an unknown strength to be rejected")
	}
}
