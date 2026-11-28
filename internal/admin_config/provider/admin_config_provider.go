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

// Package provider supplies the admin configuration service.
package provider

import "github.com/wso2/identity-customer-data-service/internal/admin_config/service"

// AdminConfigProviderInterface defines the interface for the consent category provider.
type AdminConfigProviderInterface interface {
	GetAdminConfigService() service.AdminConfigServiceInterface
}

// AdminConfigProvider is the default implementation of the AdminConfigProviderInterface.
type AdminConfigProvider struct{}

// NewAdminConfigProvider creates a new instance of AdminConfigProvider.
func NewAdminConfigProvider() AdminConfigProviderInterface {
	return &AdminConfigProvider{}
}

// GetAdminConfigService returns the consent category service instance.
func (cp *AdminConfigProvider) GetAdminConfigService() service.AdminConfigServiceInterface {
	return service.GetAdminConfigService()
}
