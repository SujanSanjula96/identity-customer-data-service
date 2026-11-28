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
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths inside an installed pack. The executable and start.sh sit in
// <CDD_HOME>/bin, with repository/ and updates/ beside bin/.
const (
	confDir     = "repository/conf"
	configFile  = "repository/conf/deployment.yaml"
	secretsFile = "repository/conf/secrets.env"
)

// home resolves CDD_HOME.
//
// The executable's own location is the primary source, because a pack is
// relocatable and an operator who extracted it anywhere should not have to
// export anything to use it. CDD_HOME overrides that for deployments which run
// the binary from outside the pack.
func home() (string, error) {
	if env := strings.TrimSpace(os.Getenv("CDD_HOME")); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return "", fmt.Errorf("CDD_HOME %q is not a usable path: %w", env, err)
		}
		return abs, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not locate the cdd executable, so CDD_HOME is unknown: %w", err)
	}
	// Resolve symlinks so that a cdd on PATH, linked into the pack, still
	// resolves to the pack rather than to the link's directory.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	dir := packRoot(exe)
	if _, err := os.Stat(filepath.Join(dir, configFile)); err != nil {
		cwd, wdErr := os.Getwd()
		if wdErr == nil {
			if _, err := os.Stat(filepath.Join(cwd, configFile)); err == nil {
				return cwd, nil
			}
		}
		return "", fmt.Errorf("%s does not look like a CDD installation: %s is missing.\n"+
			"Run cdd from the bin directory of an extracted pack, or set CDD_HOME", dir, configFile)
	}
	return dir, nil
}

// packRoot returns the pack an executable belongs to: <CDD_HOME>/bin/cdd -> <CDD_HOME>.
func packRoot(exe string) string {
	return filepath.Dir(filepath.Dir(exe))
}

// path joins one pack-relative path onto CDD_HOME.
func path(cdsHome, rel string) string { return filepath.Join(cdsHome, filepath.FromSlash(rel)) }

// ---------------------------------------------------------------------------
// Terminal output
//
// Lifecycle commands are read by an operator watching a long-running sequence,
// so each step says what it is doing before it does it. Color is dropped when
// the output is not a terminal, so logs and CI output stay plain.
// ---------------------------------------------------------------------------

var useColour = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}()

func color(code, s string) string {
	if !useColour {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func red(s string) string    { return color("31", s) }
func yellow(s string) string { return color("33", s) }
func bold(s string) string   { return color("1", s) }

func stepf(format string, a ...any) { fmt.Printf("\n%s\n", bold(fmt.Sprintf(format, a...))) }
func infof(format string, a ...any) { fmt.Printf("    %s\n", fmt.Sprintf(format, a...)) }
func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "\n%s %s\n", yellow("!"), fmt.Sprintf(format, a...))
}
