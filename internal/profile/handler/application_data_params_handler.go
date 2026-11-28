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

package handler

import (
	"net/http"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/profile/service"
)

func parseApplicationDataParams(r *http.Request) service.ApplicationDataFilterParams {
	params := service.ApplicationDataFilterParams{
		IncludeAppData:  false,
		RequestedAppIDs: []string{},
	}

	appDataParam := r.URL.Query().Get("includeApplicationData")
	if strings.ToLower(appDataParam) == "true" {
		params.IncludeAppData = true
	}

	appIDParam := r.URL.Query().Get("application_identifier")
	if appIDParam != "" {
		if appIDParam == "*" {
			params.RequestedAppIDs = []string{"*"}
		} else {
			ids := strings.Split(appIDParam, ",")
			for _, id := range ids {
				trimmed := strings.TrimSpace(id)
				if trimmed != "" {
					params.RequestedAppIDs = append(params.RequestedAppIDs, trimmed)
				}
			}
		}
	}

	return params
}
