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

package server

import (
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

func secretsConfig(username, password, key string) config.Config {
	var cfg config.Config
	cfg.AuthServer.AdminUsername = username
	cfg.AuthServer.AdminPassword = password
	cfg.IDP.ServiceCredentials.EncryptionKey = key
	return cfg
}

func TestSecretWarningsNoneWhenEverySecretIsSet(t *testing.T) {
	if warnings := secretWarnings(secretsConfig("admin", "secret", "a-key"), true); len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

// An empty password is what an unexpanded ${AUTH_SERVER_ADMIN_PASSWORD} leaves,
// and with it no synchronization call can authenticate.
func TestSecretWarningsNameAnEmptyAdminCredential(t *testing.T) {
	cases := map[string]config.Config{
		"empty password":         secretsConfig("admin", "", "a-key"),
		"blank password":         secretsConfig("admin", "  ", "a-key"),
		"empty username":         secretsConfig("", "secret", "a-key"),
		"both empty, no key use": secretsConfig("", "", ""),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			warnings := secretWarnings(cfg, false)
			if len(warnings) != 1 || !strings.Contains(warnings[0], "AUTH_SERVER_ADMIN_PASSWORD") {
				t.Fatalf("expected one warning naming AUTH_SERVER_ADMIN_PASSWORD, got %v", warnings)
			}
		})
	}
}

func TestSecretWarningsNameAMissingKeyOnlyWhereCredentialsAreSent(t *testing.T) {
	warnings := secretWarnings(secretsConfig("admin", "secret", ""), true)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "SERVICE_CREDENTIALS_ENCRYPTION_KEY") {
		t.Fatalf("expected one warning naming SERVICE_CREDENTIALS_ENCRYPTION_KEY, got %v", warnings)
	}

	// An identity provider that sends no credentials has no use for a key.
	if warnings := secretWarnings(secretsConfig("admin", "secret", ""), false); len(warnings) != 0 {
		t.Fatalf("expected no warning without service credentials, got %v", warnings)
	}
}
