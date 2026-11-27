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

import (
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/pagination"
)

// ProfileResponse is the API representation of a single profile.
type ProfileResponse struct {
	ProfileID        string `json:"profile_id" bson:"profile_id"`
	UserID           string `json:"user_id,omitempty" bson:"user_id,omitempty"`
	AnonymousTracker string `json:"anonymous_profile_tracker,omitempty" bson:"anonymous_profile_tracker,omitempty"`
	Meta             Meta   `json:"meta" bson:"meta"`

	IdentityAttributes map[string]any `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]any `json:"traits,omitempty" bson:"traits,omitempty"`

	ApplicationData map[string]map[string]any `json:"application_data,omitempty" bson:"application_data,omitempty"`

	MergedTo   *Reference  `json:"merged_to,omitempty" bson:"merged_to,omitempty"`
	MergedFrom []Reference `json:"merged_from,omitempty" bson:"merged_from,omitempty"`
}

// ProfileListResponse is the API representation of a profile in a list, with a match score for fuzzy searches.
type ProfileListResponse struct {
	ProfileID string `json:"profile_id" bson:"profile_id"`
	UserID    string `json:"user_id,omitempty" bson:"user_id,omitempty"`
	Meta      Meta   `json:"meta" bson:"meta"`

	IdentityAttributes map[string]any `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]any `json:"traits,omitempty" bson:"traits,omitempty"`

	ApplicationData map[string]map[string]any `json:"application_data,omitempty" bson:"application_data,omitempty"`

	MergedFrom     []Reference        `json:"merged_from,omitempty" bson:"merged_from,omitempty"`
	MatchScore     *float64           `json:"match_score,omitempty" bson:"match_score,omitempty"`
	ScoreBreakdown map[string]float64 `json:"score_breakdown,omitempty" bson:"score_breakdown,omitempty"`
}

// Meta holds a profile's creation and update times and its resource location.
type Meta struct {
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
	Location  string    `json:"location" bson:"location"`
}

// ProfileCreateRequest is the request body of POST /profiles.
type ProfileCreateRequest struct {
	IdentityAttributes map[string]any `json:"identity_attributes,omitempty"`
	Traits             map[string]any `json:"traits,omitempty"`
	ApplicationData    map[string]any `json:"application_data"`
}

// ToProfileRequest returns the service input for this request, with no user ID.
func (r ProfileCreateRequest) ToProfileRequest() ProfileRequest {
	return ProfileRequest{
		IdentityAttributes: r.IdentityAttributes,
		Traits:             r.Traits,
		ApplicationData:    r.ApplicationData,
	}
}

// ProfilePatchRequest is the request body of PATCH /profiles/{profileId}. Each field is deep merged into the
// stored value. It has no user ID: only a link or the identity server sync sets it.
type ProfilePatchRequest struct {
	IdentityAttributes map[string]any `json:"identity_attributes,omitempty"`
	Traits             map[string]any `json:"traits,omitempty"`
	ApplicationData    map[string]any `json:"application_data,omitempty"`
}

// ProfileRequest is the service input for creating or updating a profile. Only internal callers, such as the
// identity server sync and authentication events, set UserID.
type ProfileRequest struct {
	UserID             string         `json:"user_id" bson:"user_id"`
	IdentityAttributes map[string]any `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]any `json:"traits,omitempty" bson:"traits,omitempty"`
	ApplicationData    map[string]any `json:"application_data"`
}

// ProfileLinkRequest is the request body for linking a profile to a user.
type ProfileLinkRequest struct {
	ProfileID string `json:"profile_id" bson:"profile_id"`
	UserID    string `json:"user_id" bson:"user_id"`
}

// ProfileLinkResponse is the response body for a profile link request.
type ProfileLinkResponse struct {
	ProfileID string `json:"profile_id" bson:"profile_id"`
	UserID    string `json:"user_id" bson:"user_id"`
}

// ProfileSync is a user event, such as a user add, update, delete, login or logout, sent by the identity
// server to keep profiles in sync.
type ProfileSync struct {
	UserID        string                 `json:"userId" bson:"userId"`
	ProfileCookie string                 `json:"profileCookie,omitempty" bson:"profileCookie,omitempty"`
	ProfileID     string                 `json:"profileId,omitempty" bson:"profileId,omitempty"`
	Event         string                 `json:"event" bson:"event"`
	Claims        map[string]interface{} `json:"claims,omitempty" bson:"claims,omitempty"`
	OrgHandle     string                 `json:"orgHandle,omitempty" bson:"orgHandle,omitempty"`
}

// ProfileListAPIResponse is the paginated response body for listing profiles.
type ProfileListAPIResponse struct {
	Pagination pagination.Pagination `json:"pagination"`
	Items      []ProfileListResponse `json:"profiles"`
}

// FuzzyMatchResult holds a profile along with its fuzzy match score.
type FuzzyMatchResult struct {
	Profile        ProfileResponse    `json:"profile"`
	MatchScore     float64            `json:"match_score"`
	ScoreBreakdown map[string]float64 `json:"score_breakdown"`
}
