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

type ProfileStatus struct {
	ReferenceProfileId string `json:"reference_profile_id,omitempty" bson:"reference_profile_id,omitempty"`
	IsReferenceProfile bool   `json:"is_reference_profile,omitempty" bson:"is_reference_profile,omitempty"`
	// Is the profile waiting for admin approval for unification
	IsWaitingOnAdmin bool `json:"is_waiting_on_admin,omitempty" bson:"is_waiting_on_admin,omitempty"`
	// Is the profile waiting for user approval for unification
	IsWaitingOnUser bool        `json:"is_waiting_on_user,omitempty" bson:"is_waiting_on_user,omitempty"`
	DeleteProfile   bool        `json:"delete_profile,omitempty" bson:"delete_profile,omitempty"`
	ListProfile     bool        `json:"list_profile,omitempty" bson:"list_profile,omitempty"`
	ReferenceReason string      `json:"reference_reason,omitempty" bson:"reference_reason,omitempty"`
	References      []Reference `json:"references,omitempty" bson:"references,omitempty"`
}

type Reference struct {
	ProfileId string `json:"profile_id,omitempty" bson:"profile_id,omitempty"`
	Reason    string `json:"reason,omitempty" bson:"rule_name,omitempty"`
}

type Profile struct {
	ProfileId          string                 `json:"profile_id" bson:"profile_id"`
	UserId             string                 `json:"user_id" bson:"user_id"`
	OrgId              string                 `json:"org_id" bson:"org_id"`
	CreatedAt          time.Time              `json:"created_at" bson:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at" bson:"updated_at"`
	IdentityAttributes map[string]interface{} `json:"identity_attributes,omitempty" bson:"identity_attributes,omitempty"`
	Traits             map[string]interface{} `json:"traits,omitempty" bson:"traits,omitempty"`
	ApplicationData    []ApplicationData      `json:"application_data,omitempty" bson:"application_data,omitempty"`
	ProfileStatus      *ProfileStatus         `json:"profile_status,omitempty" bson:"profile_status,omitempty"`
}

type ProfileCookie struct {
	ProfileId string `json:"profile_id" bson:"profile_id"`
	CookieId  string `json:"cookie_id" bson:"cookie_id"`
	IsActive  bool   `json:"is_active" bson:"is_active"`
}

// ProfileConsent represents the consent information associated with a profile
type ProfileConsent struct {
	Consents []ConsentRecord `json:"consents" bson:"consents"`
}

// ConsentRecord represents an individual consent record for a profile
type ConsentRecord struct {
	CategoryIdentifier string    `json:"category_identifier" bson:"category_identifier"` // References the consent category
	IsConsented        bool      `json:"is_consented" bson:"is_consented"`               // Whether the user has given consent
	ConsentedAt        time.Time `json:"consented_at" bson:"consented_at"`               // Timestamp when consent was given/updated
}

// ProfileConsentResponse is the response model for profile consents API
type ProfileConsentResponse struct {
	Consents []ConsentRecord `json:"consents"`
}

type ProfileCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ProfileId string    `json:"profile_id"`
	Direction string    `json:"direction,omitempty"` // "next" or "prev"
}
