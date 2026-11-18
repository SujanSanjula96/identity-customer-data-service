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

package model

import "encoding/json"

// ApplicationData represents contextual data for an application
type ApplicationData struct {
	AppID           string                 `json:"application_id" bson:"application_id"`
	AppSpecificData map[string]interface{} `json:"app_specific_data,omitempty" bson:"app_specific_data,omitempty"`
}

// MarshalJSON writes the application-specific data as top-level fields next to application_id.
func (a ApplicationData) MarshalJSON() ([]byte, error) {
	base := map[string]interface{}{
		"application_id": a.AppID,
	}
	for k, v := range a.AppSpecificData {
		base[k] = v
	}
	return json.Marshal(base)
}
