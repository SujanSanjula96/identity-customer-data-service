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
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Secrets are kept out of deployment.yaml and reach the runtime as environment
// variables, which is what the ${...} placeholders in the shipped configuration
// expand from.
//
// repository/conf/secrets.env is where an operator can keep them, one
// KEY=VALUE per line, readable by the account that runs CDD and nobody else.
// The pack ships it with every value empty, for the operator to fill in.
// `cdd start` reads it, and a variable already set in the environment wins,
// so a deployment with a secret manager supplies them from there instead and
// needs no file at all.

// loadSecrets reads the KEY=VALUE pairs. A missing file yields an empty map:
// an installation whose secrets come from elsewhere is not broken.
func loadSecrets(cdsHome string) (map[string]string, error) {
	file, err := os.Open(path(cdsHome, secretsFile))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("could not read %s: %w", secretsFile, err)
	}
	defer func() { _ = file.Close() }()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", secretsFile, err)
	}
	return values, nil
}

// environment turns the stored secrets into KEY=VALUE strings for a child
// process, leaving any variable already set in the environment alone so that a
// deployment can override one without editing the file.
func environment(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for key, value := range values {
		if _, set := os.LookupEnv(key); set {
			continue
		}
		env = append(env, key+"="+value)
	}
	sort.Strings(env)
	return env
}
