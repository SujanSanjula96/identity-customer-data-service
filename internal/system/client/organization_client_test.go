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
package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSelfOrganization(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/organizations/self", func(w http.ResponseWriter, r *http.Request) {
		requireToken(t, r, false)
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": "org-1", "name": "Acme", "orgHandle": "acme",
			"status": "ACTIVE"})
	})

	org, found, err := c.GetSelfOrganization("acme")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "org-1", org.ID)
	assert.Equal(t, "acme", org.OrgHandle)

	_, found, err = c.GetSelfOrganization("unknown")
	require.NoError(t, err, "an organization that does not exist is not an error")
	assert.False(t, found)
}

func TestGetOrganization(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/organizations/child-1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": "child-1", "name": "Child",
			"parent": map[string]string{"id": "org-1"}})
	})
	is.handle("/t/acme/api/server/v1/organizations/broken", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	is.handle("/t/acme/api/server/v1/organizations/garbled", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	org, found, err := c.GetOrganization("acme", "child-1")
	require.NoError(t, err)
	assert.True(t, found)
	require.NotNil(t, org.Parent)
	assert.Equal(t, "org-1", org.Parent.ID)

	_, found, err = c.GetOrganization("acme", "missing")
	require.NoError(t, err)
	assert.False(t, found)

	_, _, err = c.GetOrganization("acme", "broken")
	requireServerError(t, err)
	_, _, err = c.GetOrganization("acme", "garbled")
	requireServerError(t, err)
}

func TestListDescendantOrganizationsFollowsTheNextLink(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" {
			require.Equal(t, "true", r.URL.Query().Get("recursive"))
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"organizations": []map[string]string{{"id": "o-1"}, {"id": "o-2"}},
				"links": []map[string]string{
					{"rel": "previous", "href": "/ignored"},
					{"rel": "next", "href": "/t/acme/api/server/v1/organizations?recursive=true&limit=100&after=o-2"},
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"organizations": []map[string]string{{"id": "o-3"}}})
	})

	orgs, err := c.ListDescendantOrganizations("acme")

	require.NoError(t, err)
	ids := make([]string, 0, len(orgs))
	for _, org := range orgs {
		ids = append(ids, org.ID)
	}
	assert.Equal(t, []string{"o-1", "o-2", "o-3"}, ids)
}

func TestListDescendantOrganizationsFollowsAnAbsoluteLinkToTheSameHost(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"organizations": []map[string]string{{"id": "o-1"}},
				"links": []map[string]string{{"rel": "next",
					"href": "https://" + c.BaseURL + "/t/acme/api/server/v1/organizations?after=o-1"}},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"organizations": []map[string]string{{"id": "o-2"}}})
	})

	orgs, err := c.ListDescendantOrganizations("acme")

	require.NoError(t, err)
	assert.Len(t, orgs, 2)
}

func TestListDescendantOrganizationsRefusesALinkToAnotherHost(t *testing.T) {
	var otherHostCalls atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHostCalls.Add(1)
	}))
	t.Cleanup(other.Close)

	is, c := newIdentityServer(t, false)
	is.handle("/t/acme/api/server/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"organizations": []map[string]string{{"id": "o-1"}},
			"links":         []map[string]string{{"rel": "next", "href": other.URL + "/collect"}},
		})
	})

	_, err := c.ListDescendantOrganizations("acme")

	requireServerError(t, err)
	assert.Zero(t, otherHostCalls.Load(), "the token must not go to another host")
}

func TestResolvePageLink(t *testing.T) {
	const host = "is.example.com:9443"
	for _, href := range []string{
		"/t/acme/api/server/v1/organizations?after=o-1",
		"https://is.example.com:9443/t/acme/api/server/v1/organizations?after=o-1",
		"https://IS.example.com:9443/t/acme/api/server/v1/organizations?after=o-1",
	} {
		resolved, err := resolvePageLink(host, href)
		require.NoError(t, err, href)
		assert.True(t, strings.HasPrefix(strings.ToLower(resolved), "https://is.example.com:9443/t/acme/"), resolved)
	}

	resolved, err := resolvePageLink("is.example.com", "https://is.example.com:443/t/acme/x")
	require.NoError(t, err, "the default https port is the same host")
	assert.Equal(t, "https://is.example.com:443/t/acme/x", resolved)

	for _, href := range []string{
		"https://attacker.example.com/t/acme/x",
		"//attacker.example.com/t/acme/x",
		"http://is.example.com:9443/t/acme/x",
		"https://is.example.com:8443/t/acme/x",
		"https://user@is.example.com:9443/t/acme/x",
		"https://is.example.com:9443.attacker.example.com/x",
		"%zz",
	} {
		_, err := resolvePageLink(host, href)
		requireServerError(t, err)
	}
}

func TestListDescendantOrganizationsFailure(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.handle("/t/broken/api/server/v1/organizations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, "upstream down")
	})

	_, err := c.ListDescendantOrganizations("broken")

	requireServerError(t, err)
}

func TestOrganizationCallFailsWhenTheTokenCannotBeFetched(t *testing.T) {
	is, c := newIdentityServer(t, false)
	is.server.Close()

	_, _, err := c.GetSelfOrganization("acme")

	assert.Error(t, err)
}
