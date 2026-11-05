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

package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors" // Standard Go errors package
	"fmt"
	"net/http"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	customerrors "github.com/wso2/identity-customer-data-service/internal/system/errors" // Alias for the custom errors
	error2 "github.com/wso2/identity-customer-data-service/internal/system/errors"       // Importing custom error types
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
)

// HandleError sends an HTTP error response based on the provided error
func HandleError(w http.ResponseWriter, err error) {
	var clientError *customerrors.ClientError
	w.Header().Set("Content-Type", "application/json")
	if ok := errors.As(err, &clientError); ok {
		w.WriteHeader(clientError.StatusCode)
		_ = json.NewEncoder(w).Encode(struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Description string `json:"description"`
		}{
			Code:        clientError.ErrorMessage.Code,
			Message:     clientError.ErrorMessage.Message,
			Description: clientError.ErrorMessage.Description,
		})
		return
	}

	var serverError *customerrors.ServerError
	if ok := errors.As(err, &serverError); ok {
		logger := log.GetLogger()
		logger.Error(err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Internal server error",
		})
		return
	}
}

// ExtractOrgBasePath returns the path of the organization of the request: /t/{org_handle}, or
// /t/{primary_handle}/o/{org_id} for a sub organization.
func ExtractOrgBasePath(r *http.Request) string {
	if basePath, ok := r.Context().Value(constants.OrgBasePathContextKey).(string); ok && basePath != "" {
		return basePath
	}
	return "/t/" + ExtractOrgHandleFromPath(r)
}

func ExtractOrgHandleFromPath(r *http.Request) string {
	tenant := r.Context().Value(constants.TenantContextKey).(string)
	if tenant == "" {
		// If tenant is not found in context, fallback to default tenant
		tenant = "carbon.super"
	}
	return tenant
}

func StripTenantPrefix(path string) string {
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 4 {
		return "/"
	}
	return "/" + parts[3]
}

func WriteErrorResponse(w http.ResponseWriter, err *error2.ClientError) {

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.StatusCode)

	_ = json.NewEncoder(w).Encode(err.ErrorMessage)
}

func WriteBadRequestErrorResponse(w http.ResponseWriter, err *error2.ClientError) {

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)

	_ = json.NewEncoder(w).Encode("Invalid erquest format")
}

// MountTenantDispatcher mounts a dispatcher under /t/{tenant}/... and forwards requests to handlerFunc
// with the tenant added to the context. It preserves the remaining path (e.g., /cds/api/v1/...).
func MountTenantDispatcher(mux *http.ServeMux, handlerFunc http.HandlerFunc) {
	const prefix = "/t/"
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")

		if !strings.HasPrefix(path, prefix) {
			http.NotFound(w, r)
			return
		}

		parts := strings.SplitN(path[len(prefix):], "/", 2)
		if len(parts) != 2 {
			http.Error(w, "Invalid path format", http.StatusBadRequest)
			return
		}

		orgHandle := parts[0]
		remainingPath := "/" + parts[1]

		basePath := prefix + orgHandle
		ctx := context.WithValue(r.Context(), constants.TenantContextKey, orgHandle)
		if strings.HasPrefix(remainingPath, "/o/") {
			// /t/{primary_handle}/o/{org_id}/... is the path of a sub organization.
			segments := strings.SplitN(strings.TrimPrefix(remainingPath, "/o/"), "/", 2)
			if len(segments) != 2 || segments[0] == "" || SubOrgResolver == nil {
				http.NotFound(w, r)
				return
			}
			orgId := segments[0]
			subOrgHandle, err := SubOrgResolver(r.Context(), orgHandle, orgId)
			if err != nil {
				HandleError(w, err)
				return
			}
			ctx = context.WithValue(ctx, constants.TenantContextKey, subOrgHandle)
			ctx = context.WithValue(ctx, constants.OrgIdContextKey, orgId)
			// The path names the org ID, so the stores need no lookup for the org key.
			ctx = orgkey.WithRef(ctx, orgkey.Ref{ID: orgId, Handle: subOrgHandle})
			remainingPath = "/" + segments[1]
			basePath += "/o/" + orgId
		} else if IsSubOrgHandle != nil {
			isSubOrg, err := IsSubOrgHandle(r.Context(), orgHandle)
			if err != nil {
				HandleError(w, err)
				return
			}
			if isSubOrg {
				HandleError(w, error2.NewClientError(error2.ORGANIZATION_PATH_NOT_SUPPORTED, http.StatusBadRequest))
				return
			}
		}
		ctx = context.WithValue(ctx, constants.OrgBasePathContextKey, basePath)
		r = r.WithContext(ctx)
		r.URL.Path = remainingPath

		handlerFunc(w, r)
	})
}

// SubOrgResolver returns the handle of the sub organization of a sub organization path. It returns a
// client error when the organization is not in the tree, and a server error when the lookup fails.
var SubOrgResolver func(ctx context.Context, primaryHandle, orgId string) (string, error)

// IsSubOrgHandle reports whether the handle belongs to a sub organization.
var IsSubOrgHandle func(ctx context.Context, orgHandle string) (bool, error)

// RespondJSON sends a JSON response with the given status code and payload
func RespondJSON(w http.ResponseWriter, status int, payload any, resource string) {

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(payload); err != nil {
		serverError := error2.NewServerError(error2.ErrorMessage{
			Code:        error2.ENCODE_ERROR.Code,
			Message:     error2.ENCODE_ERROR.Message,
			Description: fmt.Sprintf("Failed to encode %s response", resource),
		}, err)
		HandleError(w, serverError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
