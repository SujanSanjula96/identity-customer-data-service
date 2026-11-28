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

// Package cli implements cdd, the command installed packs ship.
//
// Extracting the pack is the installation, and configuring it is editing
// repository/conf/deployment.yaml. The command does not reach into the
// Identity Server: the Identity Server carries its own side of the
// integration, so nothing here writes to an Identity Server installation —
// which an image or a read-only mount would not allow anyway.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// command is one subcommand of cdd.
type command struct {
	name string
	// summary is the one-line description shown by `cdd help`.
	summary string
	// run receives the arguments that follow the subcommand name.
	run func(args []string) error
}

// errUsage is returned by a command whose arguments did not parse. Main prints
// the command's own usage rather than a stack of flag errors.
var errUsage = errors.New("invalid arguments")

func commands() []command {
	return []command{
		{name: "start", summary: "Run CDD in the foreground; Ctrl+C stops it", run: runStart},
	}
}

// Main runs one subcommand and returns the process exit code.
func Main(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(os.Stdout)
		return 0
	}

	name := args[0]
	for _, cmd := range commands() {
		if cmd.name != name {
			continue
		}
		err := cmd.run(args[1:])
		if err == nil {
			return 0
		}
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintf(os.Stderr, "\n%s %v\n", red("Error:"), err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "%s unknown command %q\n\n", red("Error:"), name)
	usage(os.Stderr)
	return 2
}

func usage(out *os.File) {
	var b strings.Builder
	b.WriteString("Customer Data Directory\n\nUsage:\n  cdd <command> [options]\n\nCommands:\n")
	for _, cmd := range commands() {
		fmt.Fprintf(&b, "  %-9s %s\n", cmd.name, cmd.summary)
	}
	b.WriteString("\nRun `cdd <command> --help` for the options of one command.\n")
	_, _ = fmt.Fprint(out, b.String())
}
