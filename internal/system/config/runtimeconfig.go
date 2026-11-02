/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

package config

import "sync"

// CDSRuntime holds the runtime configuration for the CDS server.
type CDSRuntime struct {
	CDSHome string `yaml:"cds_home"`
	Config  Config `yaml:"config"`
}

var (
	runtimeConfig *CDSRuntime
	once          sync.Once
)

// InitializeCDSRuntime initializes the CDSRuntime configuration.
func InitializeCDSRuntime(home string, config *Config) error {

	once.Do(func() {
		runtimeConfig = &CDSRuntime{
			CDSHome: home,
			Config:  *config,
		}
	})

	return nil
}

// GetCDSRuntime returns the CDSRuntime configuration.
func GetCDSRuntime() *CDSRuntime {

	if runtimeConfig == nil {
		panic("CDSRuntime is not initialized")
	}
	return runtimeConfig
}

// OrganizationManagementEnabled reports whether organization management is enabled. It is false
// before the runtime configuration is initialized.
func OrganizationManagementEnabled() bool {

	return runtimeConfig != nil && runtimeConfig.Config.OrganizationManagement.Enabled
}
