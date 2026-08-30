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

import "time"
import "github.com/wso2/identity-customer-data-service/internal/system/pagination"

type ProfileResponse struct {
	ProfileId          string                            `json:"profile_id" bson:"profile_id"`
	UserId             string                            `json:"user_id,omitempty" bson:"user_id,omitempty"`
	AnonymousProfileTracker string                         `json:"anonymous_profile_tracker,omitempty" bson:"anonymous_profile_tracker,omitempty"`
	Meta               Meta                              `json:"meta" bson:"meta"`
	IdentityAttributes map[string]interface{}            `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]interface{}            `json:"traits,omitempty" bson:"traits,omitempty"`
	ApplicationData    map[string]map[string]interface{} `json:"application_data,omitempty" bson:"application_data,omitempty"`
	MergedTo           *Reference                        `json:"merged_to,omitempty" bson:"merged_to,omitempty"`
	MergedFrom         []Reference                       `json:"merged_from,omitempty" bson:"merged_from,omitempty"`
}

type ProfileListResponse struct {
	ProfileId          string                            `json:"profile_id" bson:"profile_id"`
	UserId             string                            `json:"user_id,omitempty" bson:"user_id,omitempty"`
	Meta               Meta                              `json:"meta" bson:"meta"`
	IdentityAttributes map[string]interface{}            `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]interface{}            `json:"traits,omitempty" bson:"traits,omitempty"`
	ApplicationData    map[string]map[string]interface{} `json:"application_data,omitempty" bson:"application_data,omitempty"`
	MergedFrom         []Reference                       `json:"merged_from,omitempty" bson:"merged_from,omitempty"`
}

type Meta struct {
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
	Location  string    `json:"location" bson:"location"`
}

type ProfileRequest struct {
	UserId             string                 `json:"user_id" bson:"user_id"`
	IdentityAttributes map[string]interface{} `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]interface{} `json:"traits,omitempty" bson:"traits,omitempty"`
	ApplicationData    map[string]interface{} `json:"application_data"`
}

type ProfileLinkRequest struct {
	UserId string `json:"user_id" bson:"user_id"`
}

type ProfileLinkResponse struct {
	ProfileId string `json:"profile_id" bson:"profile_id"`
	UserId    string `json:"user_id" bson:"user_id"`
}

type ProfileSync struct {
	UserId        string                 `json:"userId" bson:"userId"`
	ProfileCookie string                 `json:"profileCookie,omitempty" bson:"profileCookie,omitempty"`
	ProfileId     string                 `json:"profileId,omitempty" bson:"profileId,omitempty"`
	Event         string                 `json:"event" bson:"event"`
	Claims        map[string]interface{} `json:"claims,omitempty" bson:"claims,omitempty"`
	OrgHandle     string                 `json:"orgHandle,omitempty" bson:"orgHandle,omitempty"`
}

type ProfileListAPIResponse struct {
	Pagination pagination.Pagination `json:"pagination"`
	Items      []ProfileListResponse `json:"profiles"`
}
