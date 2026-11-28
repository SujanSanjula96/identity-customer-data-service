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

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The secrets file is the operator's: comments, blank lines and quoted values
// are all things a person writes by hand.
func TestLoadSecrets(t *testing.T) {
	cdsHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cdsHome, confDir), 0o750); err != nil {
		t.Fatalf("could not stage the configuration directory: %v", err)
	}
	content := "# set by the operator\n\nAUTH_SERVER_ADMIN_PASSWORD=plain\nDB_PASSWORD=\"quoted\"\nnot a pair\n"
	if err := os.WriteFile(filepath.Join(cdsHome, secretsFile), []byte(content), 0o600); err != nil {
		t.Fatalf("could not write the secrets file: %v", err)
	}

	secrets, err := loadSecrets(cdsHome)
	if err != nil {
		t.Fatalf("loadSecrets: %v", err)
	}
	if len(secrets) != 2 || secrets["AUTH_SERVER_ADMIN_PASSWORD"] != "plain" || secrets["DB_PASSWORD"] != "quoted" {
		t.Errorf("loadSecrets = %v", secrets)
	}

	// A variable already in the environment wins over the file.
	t.Setenv("DB_PASSWORD", "from-the-environment")
	env := environment(secrets)
	if len(env) != 1 || env[0] != "AUTH_SERVER_ADMIN_PASSWORD=plain" {
		t.Errorf("environment = %v, want only the variable the environment does not already set", env)
	}
}

func TestLoadSecretsWithoutAFile(t *testing.T) {
	// An installation whose secrets come from a secret manager has no file,
	// and that is not a failure.
	secrets, err := loadSecrets(t.TempDir())
	if err != nil {
		t.Fatalf("loadSecrets: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("expected no secrets, got %d", len(secrets))
	}
}
