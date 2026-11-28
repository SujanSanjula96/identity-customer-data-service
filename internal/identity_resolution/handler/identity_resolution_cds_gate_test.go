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

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	sharingModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingStore "github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

const reviewChildOrgID = "review-child-org-id"

// disableCDS removes the CDS configuration of the organization of the environment.
func (e *reviewEnv) disableCDS(t *testing.T) {
	t.Helper()
	_, err := e.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, e.orgID)
	require.NoError(t, err)
}

// withChild stores a sub organization of the organization of the environment, with two profiles and a
// pending task between them. The access policy of the primary organization reaches the sub organization
// only when withAccess is true.
func (e *reviewEnv) withChild(t *testing.T, withAccess bool) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO organizations (org_id, org_handle, parent_org_id, primary_org_id, status)
		VALUES ($1, 'review-child', $2, $2, 'ACTIVE')`, reviewChildOrgID, e.orgID)
	require.NoError(t, err)
	now := time.Now().UTC()
	for _, id := range []string{"c-1", "c-2"} {
		require.NoError(t, profileStore.InsertProfile(e.ctx, reviewChildOrgID, profileModel.Profile{
			ProfileID: id, OrgID: reviewChildOrgID, CreatedAt: now, UpdatedAt: now,
			ProfileStatus: &profileModel.ProfileStatus{IsReferenceProfile: true, ListProfile: true},
		}))
	}
	require.NoError(t, irStore.InsertReviewTask(e.ctx, reviewChildOrgID, irModel.ReviewTask{
		ID: "child-task", IncomingProfileID: "c-1", CandidateProfileID: "c-2", MatchScore: 0.8,
		Status: constants.ReviewStatusPending}))
	if !withAccess {
		return
	}
	require.NoError(t, sharingStore.CreatePolicy(e.ctx, sharingModel.Policy{
		PolicyID:        "access-policy",
		ResourceType:    sharingModel.ResourceOrganizationAccess,
		ResourceID:      sharingModel.OrganizationAccessResourceID,
		OwningOrgID:     e.orgID,
		InitiatingOrgID: e.orgID,
		Targets:         []sharingModel.Target{{Scope: sharingModel.ScopeAllChildren, OrgID: e.orgID}},
	}))
}

// callAsChild runs a handler on a request in the sub organization, with a token of that organization.
func (e *reviewEnv) callAsChild(t *testing.T, handler http.HandlerFunc, method, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	token := e.auth.NewJWT(t, map[string]interface{}{"org_id": reviewChildOrgID})
	request := httptest.NewRequest(method,
		"/t/"+e.handle+"/o/"+reviewChildOrgID+"/cds/api/v1/identity-resolution/review-tasks", strings.NewReader(body))
	ctx := context.WithValue(request.Context(), constants.TenantContextKey, "review-child")
	ctx = context.WithValue(ctx, constants.OrgIDContextKey, reviewChildOrgID)
	request = request.WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func assertCDSNotEnabled(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, errors2.CDSNotEnabled.Code, body["code"])
}

func TestReviewTaskHandlersRefuseAnOrganizationWithoutCDS(t *testing.T) {
	env := newReviewEnv(t)
	env.addProfile(t, "p-1")
	env.addProfile(t, "p-2")
	env.addTask(t, "task-1", "p-2")
	env.disableCDS(t)

	t.Run("list", func(t *testing.T) {
		assertCDSNotEnabled(t, env.call(t, env.h.GetReviewTasks, http.MethodGet, env.auth.ValidToken, "", "", nil))
	})
	t.Run("list for a profile", func(t *testing.T) {
		assertCDSNotEnabled(t, env.call(t, env.h.GetReviewTasks, http.MethodGet, env.auth.ValidToken,
			"?profile_id=p-1", "", nil))
	})
	t.Run("resolve", func(t *testing.T) {
		assertCDSNotEnabled(t, env.call(t, env.h.ResolveReviewTask, http.MethodPost, env.auth.ValidToken, "",
			`{"decision":"APPROVED"}`, taskID("task-1")))
	})
	task, err := irStore.GetReviewTaskByID(env.ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, constants.ReviewStatusPending, task.Status, "the task is left alone")
}

func TestReviewTaskHandlersRefuseASubOrganizationOutsideTheAccessPolicy(t *testing.T) {
	env := newReviewEnv(t)
	env.withChild(t, false)

	t.Run("list", func(t *testing.T) {
		assertCDSNotEnabled(t, env.callAsChild(t, env.h.GetReviewTasks, http.MethodGet, "", nil))
	})
	t.Run("resolve", func(t *testing.T) {
		assertCDSNotEnabled(t, env.callAsChild(t, env.h.ResolveReviewTask, http.MethodPost,
			`{"decision":"REJECTED"}`, taskID("child-task")))
	})
	task, err := irStore.GetReviewTaskByID(env.ctx, "child-task")
	require.NoError(t, err)
	assert.Equal(t, constants.ReviewStatusPending, task.Status, "the task is left alone")
}

func TestReviewTasksOfASubOrganizationInTheAccessPolicyAreListed(t *testing.T) {
	env := newReviewEnv(t)
	env.withChild(t, true)

	recorder := env.callAsChild(t, env.h.GetReviewTasks, http.MethodGet, "", nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "child-task")
}
