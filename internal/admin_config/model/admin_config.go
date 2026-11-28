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

// Package model defines the admin configuration of an organization, as stored and in the API.
package model

// AdminConfig is the configuration of an organization as stored.
type AdminConfig struct {
	OrgID                 string   `json:"org_id" bson:"org_id"`
	CDSEnabled            bool     `json:"cds_enabled" bson:"cds_enabled"`
	InitialSchemaSyncDone bool     `json:"initial_schema_sync_done" bson:"initial_schema_sync_done"`
	SystemApplications    []string `json:"system_applications" bson:"system_applications"`
	AutoMergeEnabled      bool     `json:"auto_merge_enabled" bson:"auto_merge_enabled"`
	AutoMergeThreshold    float64  `json:"auto_merge_threshold" bson:"auto_merge_threshold"`
	ManualReviewThreshold float64  `json:"manual_review_threshold" bson:"manual_review_threshold"`
	// DeterministicMatchDecisive: a pair agreeing on any deterministic rule merges without
	// the other rules being consulted. Off, the other rules may object and send it to review.
	DeterministicMatchDecisive bool `json:"deterministic_match_decisive" bson:"deterministic_match_decisive"`
	// CORSAllowedOrigins are the browser origins of the organisation's own apps, allowed to
	// call its endpoints in addition to the deployment-wide auth.cors_allowed_origins list.
	CORSAllowedOrigins []string `json:"cors_allowed_origins" bson:"cors_allowed_origins"`
	// The cleanup settings of temporary profiles are 0 when the organization has not set them.
	TemporaryProfileRetentionDays       int `json:"temporary_profile_retention_days"`
	TemporaryProfileCleanupIntervalDays int `json:"temporary_profile_cleanup_interval_days"`
}

// AdminConfigAPI is the configuration of an organization in the API responses.
type AdminConfigAPI struct {
	CDSEnabled                 bool     `json:"cds_enabled" bson:"cds_enabled"`
	SystemApplications         []string `json:"system_applications,omitempty" bson:"system_applications,omitempty"`
	AutoMergeEnabled           bool     `json:"auto_merge_enabled" bson:"auto_merge_enabled"`
	AutoMergeThreshold         float64  `json:"auto_merge_threshold" bson:"auto_merge_threshold"`
	ManualReviewThreshold      float64  `json:"manual_review_threshold" bson:"manual_review_threshold"`
	DeterministicMatchDecisive bool     `json:"deterministic_match_decisive" bson:"deterministic_match_decisive"`
	CORSAllowedOrigins         []string `json:"cors_allowed_origins,omitempty" bson:"cors_allowed_origins,omitempty"`
	// The cleanup settings of temporary profiles are the effective ones, the defaults when none is set.
	TemporaryProfileRetentionDays       int `json:"temporary_profile_retention_days"`
	TemporaryProfileCleanupIntervalDays int `json:"temporary_profile_cleanup_interval_days"`
}

// AdminConfigUpdateAPI is the body of a configuration update. A nil field leaves the setting unchanged.
type AdminConfigUpdateAPI struct {
	CDSEnabled                 *bool    `json:"cds_enabled" bson:"cds_enabled"`
	SystemApplications         []string `json:"system_applications,omitempty"`
	AutoMergeEnabled           *bool    `json:"auto_merge_enabled,omitempty"`
	AutoMergeThreshold         *float64 `json:"auto_merge_threshold,omitempty"`
	ManualReviewThreshold      *float64 `json:"manual_review_threshold,omitempty"`
	DeterministicMatchDecisive *bool    `json:"deterministic_match_decisive,omitempty"`
	CORSAllowedOrigins         []string `json:"cors_allowed_origins,omitempty"`
	// The cleanup settings of temporary profiles must be at least 1 when given.
	TemporaryProfileRetentionDays       *int `json:"temporary_profile_retention_days,omitempty"`
	TemporaryProfileCleanupIntervalDays *int `json:"temporary_profile_cleanup_interval_days,omitempty"`
}

// AdminConfigSyncAPI is the body that the identity provider sends to POST /config/sync. CDSEnabled is required.
type AdminConfigSyncAPI struct {
	CDSEnabled *bool `json:"cds_enabled"`
}

// TemporaryProfileCleanupSettings are the cleanup settings an organization has stored. A field is 0 when
// the organization has not set it.
type TemporaryProfileCleanupSettings struct {
	RetentionDays int
	IntervalDays  int
}
