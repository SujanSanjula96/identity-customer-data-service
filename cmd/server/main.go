/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
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

// Command server runs the CDS runtime in the foreground.
//
// It is the entry point used by the container image, where the process is the
// deployment and the orchestrator owns the lifecycle. Installed packs ship
// bin/cdd instead, which runs this same runtime.
package main

import (
	"flag"
	"os"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/server"
)

func main() {
	server.Run(resolveCDSHome())
}

// resolveCDSHome determines the CDS home directory from the -cdsHome flag, the
// CDS_HOME environment variable, or the working directory, in that order.
func resolveCDSHome() string {
	cdsHomeFlag := flag.String("cdsHome", "", "Path to the CDD home directory")

	if !flag.Parsed() {
		flag.Parse()
	}

	if *cdsHomeFlag != "" {
		log.GetLogger().Info("Using the CDD home from the command line argument.", log.String("cds_home", *cdsHomeFlag))
		return *cdsHomeFlag
	}

	if envHome := os.Getenv("CDS_HOME"); envHome != "" {
		log.GetLogger().Info("Using the CDD home from the CDS_HOME environment variable.",
			log.String("cds_home", envHome))
		return envHome
	}

	dir, err := os.Getwd()
	if err != nil {
		log.GetLogger().Error("Failed to get the current working directory.", log.Error(err))
		os.Exit(1)
	}
	return dir
}
