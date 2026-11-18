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

package service

// ApplicationDataFilterParams controls which application data is returned with a profile.
type ApplicationDataFilterParams struct {
	IncludeAppData  bool     // Whether to include application data
	RequestedAppIDs []string // Specific app IDs to include, or "*" for all
}

// FilterApplicationData returns the application data the caller may see. It returns nothing unless
// IncludeAppData is set. A privileged caller gets the requested app IDs, or all data when none or "*"
// is requested; any other caller gets only its own application's data.
func FilterApplicationData(
	appData map[string]map[string]interface{},
	callerAppID string,
	isSystemApp bool,
	params ApplicationDataFilterParams,
) map[string]map[string]interface{} {
	if !params.IncludeAppData {
		return make(map[string]map[string]interface{})
	}

	if len(appData) == 0 {
		return make(map[string]map[string]interface{})
	}

	if isSystemApp {
		return filterForSystemApp(appData, params.RequestedAppIDs)
	}

	return filterForRegularApp(appData, callerAppID)
}

func filterForSystemApp(
	appData map[string]map[string]interface{},
	requestedAppIDs []string,
) map[string]map[string]interface{} {
	if len(requestedAppIDs) == 0 {
		return appData
	}

	for _, id := range requestedAppIDs {
		if id == "*" {
			return appData
		}
	}

	filtered := make(map[string]map[string]interface{})
	for _, appID := range requestedAppIDs {
		if data, exists := appData[appID]; exists {
			filtered[appID] = data
		}
	}

	return filtered
}

func filterForRegularApp(
	appData map[string]map[string]interface{},
	callerAppID string,
) map[string]map[string]interface{} {
	filtered := make(map[string]map[string]interface{})

	if callerAppID != "" {
		if data, exists := appData[callerAppID]; exists {
			filtered[callerAppID] = data
		}
	}

	return filtered
}
