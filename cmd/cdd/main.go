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

// Command cdd is the executable an installed CDD pack ships at its root.
//
// It runs the runtime in the foreground and verifies the installation. The
// container image runs cmd/server instead, where only the runtime is wanted.
package main

import (
	"os"

	"github.com/wso2/identity-customer-data-service/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
