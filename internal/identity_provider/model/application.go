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

// Package model defines the responses of the identity provider APIs that CDS calls.
package model

// ApplicationsListResponse is a page of the application list of the identity server.
type ApplicationsListResponse struct {
	TotalResults int               `json:"totalResults"`
	StartIndex   int               `json:"startIndex"`
	Count        int               `json:"count"`
	Applications []ApplicationItem `json:"applications"`
}

// ApplicationItem is an application in the identity server.
type ApplicationItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ClientID string `json:"clientId"`
	Issuer   string `json:"issuer"`
}
