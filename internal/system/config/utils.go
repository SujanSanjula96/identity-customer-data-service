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

import (
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

// LoadConfig loads and sets AppConfig (global variable)
func LoadConfig(cdsHome, filePath string) (*Config, error) {
	file, err := os.ReadFile(filepath.Clean(path.Join(cdsHome, filePath)))
	if err != nil {
		return nil, err
	}

	expanded := os.ExpandEnv(string(file))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// OverrideCDSRuntime holds the runtime configuration for the application
func OverrideCDSRuntime(conf Config) {
	runtimeConfig = &CDSRuntime{
		Config: conf,
	}
}
