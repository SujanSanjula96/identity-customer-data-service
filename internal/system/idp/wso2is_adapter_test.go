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

package idp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// An organization that answers the self call on its own tenant path is the primary organization,
// with or without a parent.
func TestGetPrimaryOrganizationAcceptsAnyParent(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	_ = config.InitializeCDSRuntime("", &config.Config{
		AuthServer: config.AuthServerConfig{TokenEndpoint: "/oauth2/token"},
	})

	cases := map[string]string{
		"no parent":                 "",
		"super organization parent": `"parent":{"id":"10084a8d-113f-4211-a0d5-efe36b082211"},`,
	}
	for name, parent := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/oauth2/token"):
					_, _ = fmt.Fprint(w, `{"access_token":"token"}`)
				case r.URL.Path == "/t/acme/api/server/v1/organizations/self":
					_, _ = fmt.Fprintf(w, `{"id":"acme-id","name":"Acme",%s"status":"ACTIVE"}`, parent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			adapter := &WSO2ISAdapter{client: &client.IdentityClient{
				BaseURL:    strings.TrimPrefix(server.URL, "https://"),
				HTTPClient: server.Client(),
			}}

			org, err := adapter.GetPrimaryOrganization(context.Background(), "acme")
			if err != nil {
				t.Fatalf("GetPrimaryOrganization: %v", err)
			}
			if org.Id != "acme-id" || org.Name != "Acme" || org.ParentId != "" {
				t.Errorf("got %+v, want the organization acme-id with no parent", org)
			}
		})
	}
}

// With system app grant enabled, a sub organization without service credentials has its tokens
// introspected at the introspection endpoint of its primary organization.
func TestIntrospectTokenOfASubOrganizationAtThePrimaryOrganization(t *testing.T) {
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = fmt.Fprint(w, `{"active":true}`)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	config.OverrideCDSRuntime(config.Config{AuthServer: config.AuthServerConfig{
		ADUISHostname:           host,
		IntrospectionEndPoint:   "/oauth2/introspect",
		IsSystemAppGrantEnabled: true,
	}})
	adapter := &WSO2ISAdapter{client: &client.IdentityClient{BaseURL: host, HTTPClient: server.Client()}}

	if _, err := adapter.IntrospectToken(context.Background(), "a-token", "region1", "acme"); err != nil {
		t.Fatalf("IntrospectToken: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/t/acme/oauth2/introspect" {
		t.Errorf("got the introspection paths %v, want [/t/acme/oauth2/introspect]", paths)
	}
}
