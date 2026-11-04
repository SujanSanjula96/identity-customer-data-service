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

type AdminConfig struct {
	OrgHandle             string   `json:"org_handle" bson:"org_handle"`
	CDSEnabled            bool     `json:"cds_enabled" bson:"cds_enabled"`
	InitialSchemaSyncDone bool     `json:"initial_schema_sync_done" bson:"initial_schema_sync_done"`
	SystemApplications    []string `json:"system_applications" bson:"system_applications"`
	AutoMergeEnabled      bool     `json:"auto_merge_enabled" bson:"auto_merge_enabled"`
	AutoMergeThreshold    float64  `json:"auto_merge_threshold" bson:"auto_merge_threshold"`
	ManualReviewThreshold float64  `json:"manual_review_threshold" bson:"manual_review_threshold"`
	// DeterministicMatchDecisive: a pair agreeing on any deterministic rule merges without
	// the other rules being consulted. Off, the other rules may object and send it to review.
	DeterministicMatchDecisive bool `json:"deterministic_match_decisive" bson:"deterministic_match_decisive"`
}

type AdminConfigAPI struct {
	CDSEnabled                 bool     `json:"cds_enabled" bson:"cds_enabled"`
	SystemApplications         []string `json:"system_applications,omitempty" bson:"system_applications,omitempty"`
	AutoMergeEnabled           bool     `json:"auto_merge_enabled" bson:"auto_merge_enabled"`
	AutoMergeThreshold         float64  `json:"auto_merge_threshold" bson:"auto_merge_threshold"`
	ManualReviewThreshold      float64  `json:"manual_review_threshold" bson:"manual_review_threshold"`
	DeterministicMatchDecisive bool     `json:"deterministic_match_decisive" bson:"deterministic_match_decisive"`
}

type AdminConfigUpdateAPI struct {
	CDSEnabled                 *bool    `json:"cds_enabled" bson:"cds_enabled"`
	SystemApplications         []string `json:"system_applications,omitempty"`
	AutoMergeEnabled           *bool    `json:"auto_merge_enabled,omitempty"`
	AutoMergeThreshold         *float64 `json:"auto_merge_threshold,omitempty"`
	ManualReviewThreshold      *float64 `json:"manual_review_threshold,omitempty"`
	DeterministicMatchDecisive *bool    `json:"deterministic_match_decisive,omitempty"`
}
