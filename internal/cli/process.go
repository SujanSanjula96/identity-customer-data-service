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

package cli

import (
	"fmt"
	"os"
	"strings"
)

// runStart runs the CDD runtime in the foreground until it is stopped with
// Ctrl+C or a signal.
//
// There is no background mode, and so no stop or status: running CDD as a
// service is the job of whatever already runs services — a unit file, a
// container runtime, a process supervisor — and each of those wants exactly
// this: one process to watch, its logs on stdout, and a signal to end it.
func runStart(args []string) error {
	flags := newFlagSet("start")
	debug := flags.Bool("debug", false, "log at DEBUG level")
	if err := parse(flags, args); err != nil {
		return err
	}

	cdsHome, err := home()
	if err != nil {
		return err
	}
	cfg, err := readRuntimeConfig(cdsHome)
	if err != nil {
		return err
	}

	// Refused here rather than left to the runtime, so the operator reads why
	// rather than a bind error from deep in the server's startup.
	if cfg.addressInUse() {
		return fmt.Errorf("something is already listening on %s, so CDD cannot start there.\n"+
			"Stop it, or change addr in %s", cfg.localBase(), configFile)
	}
	if cfg.syncAddressInUse() {
		return fmt.Errorf("something is already listening on %s, so CDD cannot serve the sync endpoints there.\n"+
			"Stop it, or change sync.addr in %s",
			cfg.syncAddress(), configFile)
	}

	if *debug {
		// Set before the secrets are applied, so the file cannot override a
		// level the operator asked for on the command line.
		if err := os.Setenv("LOG_LEVEL", "DEBUG"); err != nil {
			return fmt.Errorf("could not set LOG_LEVEL: %w", err)
		}
	}
	if err := applySecrets(cdsHome); err != nil {
		return err
	}

	// The pack's default certificate is Identity Server 7.3.0's own key pair, so
	// a fresh installation and a default Identity Server on one host trust each
	// other with no setup. Its private key is public, so it is said once, here.
	if usingDefaultCertificate(cfg.serverCertificate(cdsHome)) {
		warnf("CDD is using the default certificate the pack ships with. Its private key is in every " +
			"Identity Server 7.3.0 download: replace it before this deployment carries real traffic. " +
			"See repository/resources/security/README.md.")
	}
	if trustsDefaultCertificate(cfg.clientTrustStore(cdsHome)) {
		warnf("CDD accepts the default certificate the pack ships with as a client certificate on its sync " +
			"port. Its private key is in every Identity Server 7.3.0 download: replace sync.client_trust_store " +
			"with the certificate the Identity Server presents. See repository/resources/security/README.md.")
	}

	stepf("Starting CDD at %s", cfg.localBase())
	infof("Press Ctrl+C to stop.")
	fmt.Println()
	runtimeRun(cdsHome)
	return nil
}

// runtimeDefaults are values the packed configuration expands from the
// environment but that an installed pack has no other source for.
//
// Only what stops the runtime starting is defaulted here: an unset log level
// is not an empty log level, it is a parse failure that ends the process
// before it writes a line saying so. Anything an operator is expected to
// decide — the datasource, the certificate — is left to them and reported
// when it is missing.
var runtimeDefaults = map[string]string{
	"LOG_LEVEL": "INFO",
}

// applySecrets puts the operator's secrets file into the environment, which is
// where the ${...} placeholders in deployment.yaml are expanded from. A
// variable already set in the environment wins, so a deployment supplying its
// secrets from elsewhere is not overridden by the file.
func applySecrets(cdsHome string) error {
	secrets, err := loadSecrets(cdsHome)
	if err != nil {
		return err
	}
	for _, entry := range environment(secrets) {
		key, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("could not set %s: %w", key, err)
		}
	}

	for key, value := range runtimeDefaults {
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("could not set %s: %w", key, err)
		}
	}
	return nil
}
