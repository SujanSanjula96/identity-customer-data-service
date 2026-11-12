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
package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var reviewOrgCounter atomic.Int64

type reviewEnv struct {
	ctx    context.Context
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *IdentityResolutionHandler
}

func newReviewEnv(t *testing.T) *reviewEnv {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	auth := setup.InstallAuthStub(t, "identity_resolution:view", "identity_resolution:update")

	handle := fmt.Sprintf("acme-%d", reviewOrgCounter.Add(1))
	env := &reviewEnv{ctx: context.Background(), db: testDB.DB, handle: handle, orgID: setup.TestOrgId(handle),
		auth: auth, h: NewIdentityResolutionHandler()}
	setup.TestOrg(t, handle)
	return env
}

func (e *reviewEnv) call(t *testing.T, handler http.HandlerFunc, method, token, query, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/identity-resolution/review-tasks"+query,
		strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

// addProfile stores a standalone master profile.
func (e *reviewEnv) addProfile(t *testing.T, id string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, profileStore.InsertProfile(e.ctx, e.orgID, profileModel.Profile{
		ProfileId: id,
		OrgId:     e.orgID,
		CreatedAt: now,
		UpdatedAt: now,
		Traits:    map[string]interface{}{"city": id},
		ProfileStatus: &profileModel.ProfileStatus{
			IsReferenceProfile: true,
			ListProfile:        true,
		},
	}))
}

// addTask raises a pending task whose incoming profile is p-1.
func (e *reviewEnv) addTask(t *testing.T, id, candidate string) {
	t.Helper()
	require.NoError(t, irStore.InsertReviewTask(e.ctx, e.orgID, irModel.ReviewTask{
		ID:                 id,
		IncomingProfileID:  "p-1",
		CandidateProfileID: candidate,
		MatchScore:         0.8,
		Status:             constants.ReviewStatusPending,
		ScoreBreakdown:     map[string]float64{"traits.city": 0.8},
	}))
}

func (e *reviewEnv) list(t *testing.T, query string) irModel.ReviewTaskListResponse {
	t.Helper()
	recorder := e.call(t, e.h.GetReviewTasks, http.MethodGet, e.auth.ValidToken, query, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response irModel.ReviewTaskListResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func taskID(id string) map[string]string { return map[string]string{"taskId": id} }

func TestGetReviewTasksListsThePendingTasks(t *testing.T) {
	env := newReviewEnv(t)
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		env.addProfile(t, id)
	}
	env.addTask(t, "task-1", "p-2")
	env.addTask(t, "task-2", "p-3")

	all := env.list(t, "")
	assert.Len(t, all.Tasks, 2)
	for _, task := range all.Tasks {
		assert.Equal(t, constants.ReviewStatusPending, task.Status)
		assert.Equal(t, "p-1", task.IncomingProfileID)
	}

	// profile_id selects the tasks that the profile is the incoming side of.
	incoming := env.list(t, "?profile_id=p-1")
	assert.Len(t, incoming.Tasks, 2)

	none := env.list(t, "?profile_id=unknown")
	assert.Empty(t, none.Tasks)
}

func TestGetReviewTasksOfAnOrganizationWithNone(t *testing.T) {
	env := newReviewEnv(t)

	response := env.list(t, "")

	assert.Empty(t, response.Tasks)
}

func TestGetReviewTasksRefusesABadPageSize(t *testing.T) {
	env := newReviewEnv(t)

	recorder := env.call(t, env.h.GetReviewTasks, http.MethodGet, env.auth.ValidToken, "?page_size=abc", "", nil)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestRejectingAReviewTaskKeepsTheProfilesApart(t *testing.T) {
	env := newReviewEnv(t)
	env.addProfile(t, "p-1")
	env.addProfile(t, "p-2")
	env.addTask(t, "task-1", "p-2")

	recorder := env.call(t, env.h.ResolveReviewTask, http.MethodPost, env.auth.ValidToken, "",
		`{"decision":"REJECTED","notes":"different people"}`, taskID("task-1"))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "success", body["status"])
	assert.Equal(t, "Review task task-1 rejected", body["message"])

	assert.Empty(t, env.list(t, "").Tasks, "a resolved task is no longer pending")
	task, err := irStore.GetReviewTaskByID(env.ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, constants.ReviewStatusRejected, task.Status)
	assert.Equal(t, "different people", task.Notes)
	assert.Equal(t, constants.MergeByAdmin, task.ResolvedBy, "an opaque token has no identity to record")
	rejections, err := irStore.GetRejectionsForProfile(env.ctx, env.orgID, "p-1")
	require.NoError(t, err)
	assert.Contains(t, rejections, "p-2", "the rejection is remembered")
}

func TestApprovingAReviewTaskMergesTheProfiles(t *testing.T) {
	env := newReviewEnv(t)
	env.addProfile(t, "p-1")
	env.addProfile(t, "p-2")
	env.addTask(t, "task-1", "p-2")

	recorder := env.call(t, env.h.ResolveReviewTask, http.MethodPost, env.auth.ValidToken, "",
		`{"decision":"APPROVED"}`, taskID("task-1"))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "Review task task-1 approved")
	task, err := irStore.GetReviewTaskByID(env.ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, constants.ReviewStatusApproved, task.Status)

	// Both profiles now read as merged into one master.
	incoming, err := profileStore.GetProfile(env.ctx, "p-1", env.orgID)
	require.NoError(t, err)
	candidate, err := profileStore.GetProfile(env.ctx, "p-2", env.orgID)
	require.NoError(t, err)
	require.NotNil(t, incoming.ProfileStatus)
	require.NotNil(t, candidate.ProfileStatus)
	assert.NotEmpty(t, incoming.ProfileStatus.ReferenceProfileId)
	assert.Equal(t, incoming.ProfileStatus.ReferenceProfileId, candidate.ProfileStatus.ReferenceProfileId)
}

func TestResolveReviewTaskRefusals(t *testing.T) {
	env := newReviewEnv(t)
	env.addProfile(t, "p-1")
	env.addProfile(t, "p-2")
	env.addTask(t, "task-1", "p-2")
	first := env.call(t, env.h.ResolveReviewTask, http.MethodPost, env.auth.ValidToken, "",
		`{"decision":"REJECTED"}`, taskID("task-1"))
	require.Equal(t, http.StatusOK, first.Code)

	tests := []struct {
		name   string
		body   string
		ids    map[string]string
		status int
	}{
		{"no task in the path", `{"decision":"APPROVED"}`, nil, http.StatusBadRequest},
		{"malformed JSON", `{"decision":`, taskID("task-1"), http.StatusBadRequest},
		{"a decision that is not allowed", `{"decision":"MAYBE"}`, taskID("task-1"), http.StatusBadRequest},
		{"a lower case decision", `{"decision":"approved"}`, taskID("task-1"), http.StatusBadRequest},
		{"a task that does not exist", `{"decision":"APPROVED"}`, taskID("missing"), http.StatusNotFound},
		{"a task that is already resolved", `{"decision":"APPROVED"}`, taskID("task-1"), http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.call(t, env.h.ResolveReviewTask, http.MethodPost, env.auth.ValidToken, "", tc.body, tc.ids)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestATaskOfAnotherOrganizationIsReportedAsNotFound(t *testing.T) {
	env := newReviewEnv(t)
	env.addProfile(t, "p-1")
	env.addProfile(t, "p-2")
	require.NoError(t, irStore.InsertReviewTask(env.ctx, "another-org", irModel.ReviewTask{
		ID: "foreign", IncomingProfileID: "p-1", CandidateProfileID: "p-2", MatchScore: 0.8,
		Status: constants.ReviewStatusPending}))

	recorder := env.call(t, env.h.ResolveReviewTask, http.MethodPost, env.auth.ValidToken, "",
		`{"decision":"REJECTED"}`, taskID("foreign"))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	task, err := irStore.GetReviewTaskByID(env.ctx, "foreign")
	require.NoError(t, err)
	assert.Equal(t, constants.ReviewStatusPending, task.Status, "the task is left alone")
}

func TestReviewTaskHandlersRequireAuthentication(t *testing.T) {
	env := newReviewEnv(t)
	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"list", env.h.GetReviewTasks, http.MethodGet, ""},
		{"resolve", env.h.ResolveReviewTask, http.MethodPost, `{"decision":"REJECTED"}`},
	}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, "", "", tc.body, taskID("task-1"))

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, env.auth.OtherToken, "", tc.body, taskID("task-1"))

			assert.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}
}
