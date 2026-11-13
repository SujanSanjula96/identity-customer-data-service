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

package servicecreds

import (
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

const usableKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="

// A deployment without a key stores nothing and seals nothing, so it must not be
// asked for a key it has no use for, whatever its identity provider.
func TestValidateConfigAcceptsNoKey(t *testing.T) {
	for _, supported := range []bool{true, false} {
		if err := ValidateConfig(withKey("", "other"), supported); err != nil {
			t.Fatalf("ValidateConfig with supported %t: %v", supported, err)
		}
	}
}

func TestValidateConfigRefusesAnUnusableKey(t *testing.T) {
	cases := map[string]string{
		"not base64":   "not-base64-$$$",
		"wrong length": "c2hvcnQ=",
	}

	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateConfig(withKey(key, "wso2is"), true)
			if err == nil {
				t.Fatalf("expected a %s key to stop the server", name)
			}
			if !strings.Contains(err.Error(), "encryption_key") {
				t.Fatalf("error does not name the setting to fix: %v", err)
			}
		})
	}
}

func TestValidateConfigAcceptsAUsableKey(t *testing.T) {
	if err := ValidateConfig(withKey(usableKey, "wso2is"), true); err != nil {
		t.Fatalf("ValidateConfig: %v", err)
	}
}

// Nothing sends credentials to a deployment whose identity provider adapter does
// not verify them, so a key there is a mistake to report at start.
func TestValidateConfigRefusesAKeyTheIdentityProviderHasNoUseFor(t *testing.T) {
	err := ValidateConfig(withKey(usableKey, "other"), false)
	if err == nil {
		t.Fatal("expected a key for an identity provider without service credentials to stop the server")
	}
	if !strings.Contains(err.Error(), "encryption_key") || !strings.Contains(err.Error(), "other") {
		t.Fatalf("error does not name the setting and the identity provider: %v", err)
	}
}

func TestEnabledOnceAKeyIsSet(t *testing.T) {
	_ = config.InitializeCDSRuntime(t.TempDir(), &config.Config{})
	saved := config.GetCDSRuntime().Config
	t.Cleanup(func() { config.GetCDSRuntime().Config = saved })

	for _, key := range []string{usableKey, ""} {
		config.GetCDSRuntime().Config = withKey(key, "wso2is")
		if got, want := Enabled(), key != ""; got != want {
			t.Errorf("Enabled() with key %t = %t, want %t", key != "", got, want)
		}
	}
}

func withKey(key, idpType string) config.Config {
	return config.Config{
		IdP: config.IdPConfig{
			Type:               idpType,
			ServiceCredentials: config.ServiceCredentialsConfig{EncryptionKey: key},
		},
	}
}
