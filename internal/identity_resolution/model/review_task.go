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

package model

import "github.com/wso2/identity-customer-data-service/internal/system/pagination"

// ReviewTask is a candidate pair of profiles held for an administrator to merge or reject.
type ReviewTask struct {
	ID                 string `json:"id"`
	OrgID              string `json:"-"` // For the owner check only
	IncomingProfileID  string `json:"incoming_profile_id"`
	CandidateProfileID string `json:"candidate_profile_id"`
	// MatchScore is the pair's similarity before any cap. It orders a profile's tasks and is
	// what a rejection is recorded against, but it is not returned: a reviewer is shown
	// what each rule found and why the pair was held, which a single number cannot say.
	MatchScore     float64            `json:"-"`
	Status         string             `json:"status"`
	ScoreBreakdown map[string]float64 `json:"score_breakdown,omitempty"`
	// Explanation is absent on a task raised before explanations were recorded.
	Explanation *MatchExplanation `json:"explanation,omitempty"`
	CreatedAt   string            `json:"created_at"`
	ResolvedAt  string            `json:"resolved_at,omitempty"`
	ResolvedBy  string            `json:"resolved_by,omitempty"`
	Notes       string            `json:"notes,omitempty"`
}

// ReviewTaskListResponse is a page of review tasks.
type ReviewTaskListResponse struct {
	Pagination pagination.Pagination `json:"pagination"`
	Tasks      []ReviewTask          `json:"tasks"`
}

// ReviewResolveRequest is the request body for resolving a review task.
type ReviewResolveRequest struct {
	Decision string `json:"decision"`
	Notes    string `json:"notes"`
}
