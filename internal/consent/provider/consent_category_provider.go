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

// Package provider supplies the consent category service.
package provider

import (
	"github.com/wso2/identity-customer-data-service/internal/consent/service"
)

// ConsentCategoryProviderInterface defines the interface for the consent category provider.
type ConsentCategoryProviderInterface interface {
	GetConsentCategoryService() service.ConsentCategoryServiceInterface
}

// ConsentCategoryProvider is the default implementation of the ConsentCategoryProviderInterface.
type ConsentCategoryProvider struct{}

// NewConsentCategoryProvider creates a new instance of ConsentCategoryProvider.
func NewConsentCategoryProvider() ConsentCategoryProviderInterface {
	return &ConsentCategoryProvider{}
}

// GetConsentCategoryService returns the consent category service instance.
func (cp *ConsentCategoryProvider) GetConsentCategoryService() service.ConsentCategoryServiceInterface {
	return service.GetConsentCategoryService()
}
