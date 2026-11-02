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

import "github.com/wso2/identity-customer-data-service/internal/system/pagination"

type ReviewTask struct {
	ID                 string             `json:"id"`
	OrgHandle          string             `json:"org_handle"`
	IncomingProfileID  string             `json:"incoming_profile_id"`
	CandidateProfileID string             `json:"candidate_profile_id"`
	MatchScore         float64            `json:"match_score"`
	Status             string             `json:"status"`
	MatchReason        string             `json:"match_reason,omitempty"`
	ScoreBreakdown     map[string]float64 `json:"score_breakdown,omitempty"`
	CreatedAt          string             `json:"created_at"`
	ResolvedAt         string             `json:"resolved_at,omitempty"`
	ResolvedBy         string             `json:"resolved_by,omitempty"`
	Notes              string             `json:"notes,omitempty"`
}

type ReviewTaskListResponse struct {
	Pagination pagination.Pagination `json:"pagination"`
	Tasks      []ReviewTask          `json:"tasks"`
}

// ReviewResolveRequest is the request body for resolving a review task.
type ReviewResolveRequest struct {
	Decision string `json:"decision"`
	Notes    string `json:"notes"`
}
