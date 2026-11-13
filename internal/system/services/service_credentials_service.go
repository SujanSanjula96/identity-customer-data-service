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

// Package services registers the routes of the CDS APIs.
package services

import (
	"net/http"

	"github.com/wso2/identity-customer-data-service/internal/service_credentials/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// ServiceCredentialsService registers the service credentials sync endpoint.
type ServiceCredentialsService struct {
	handler *handler.ServiceCredentialsHandler
	mux     *http.ServeMux
}

// NewServiceCredentialsService registers the service credentials sync endpoint on the mux.
func NewServiceCredentialsService(mux *http.ServeMux) *ServiceCredentialsService {
	s := &ServiceCredentialsService{
		handler: handler.NewServiceCredentialsHandler(),
		mux:     mux,
	}

	const base = constants.ApiBasePath + "/v1"
	s.mux.HandleFunc("POST "+base+"/service-credentials/sync", s.handler.SyncServiceCredentials)

	return s
}
