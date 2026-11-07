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

// Package idp defines the adapter that reads data from the identity provider.
package idp

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Organization holds the details of an organization that the identity provider returns.
type Organization struct {
	Id       string
	Handle   string
	Name     string
	ParentId string // Empty for a primary organization.
	Status   string
}

// Adapter reads data from the identity provider.
type Adapter interface {
	// GetPrimaryOrganization returns the primary organization with the handle.
	GetPrimaryOrganization(ctx context.Context, primaryHandle string) (Organization, error)
	// ListDescendants returns all organizations below the primary organization, each with its
	// parent.
	ListDescendants(ctx context.Context, primaryHandle string) ([]Organization, error)
	// GetOrganization returns one organization below the primary organization. It returns nil when
	// the organization does not exist.
	GetOrganization(ctx context.Context, primaryHandle, orgId string) (*Organization, error)
	// IdentityAttributeSourceHandle returns the handle of the organization that holds the identity
	// attributes of a sub organization.
	IdentityAttributeSourceHandle(orgHandle, primaryHandle string) string
}

// ServiceCredentialsVerifier is implemented by the adapter of an identity provider that sends CDS
// the service credentials CDS calls it with, one pair per primary organization. CDS stores a pair
// only after the adapter verifies it, and refuses an encryption key for any other adapter.
type ServiceCredentialsVerifier interface {
	// VerifyServiceCredentials returns nil when the identity provider issues a token to the client
	// for the organization, and a client error when it refuses the client.
	VerifyServiceCredentials(ctx context.Context, orgHandle, clientID, clientSecret string) error
}

// TokenIntrospector is implemented by the adapter of an identity provider that
// introspects tokens itself, as with the service credentials of the
// organization. CDS introspects tokens through it when the adapter implements it.
type TokenIntrospector interface {
	// IntrospectToken returns the introspection response of the token, for the
	// organization of the request path.
	IntrospectToken(ctx context.Context, token, orgHandle string) (map[string]interface{}, error)
}

var (
	adapterMu sync.RWMutex
	adapter   Adapter
)

// GetAdapter returns the configured adapter.
func GetAdapter() Adapter {

	adapterMu.RLock()
	a := adapter
	adapterMu.RUnlock()
	if a != nil {
		return a
	}
	return NewWSO2ISAdapter()
}

// Identity provider types of the idp.type setting.
const (
	TypeWSO2IS = "wso2is"
)

// NewAdapter returns the adapter of the identity provider type. An empty type selects the default
// adapter.
func NewAdapter(idpType string) (Adapter, error) {

	switch strings.ToLower(strings.TrimSpace(idpType)) {
	case "", TypeWSO2IS:
		return NewWSO2ISAdapter(), nil
	default:
		return nil, fmt.Errorf("the identity provider type '%s' is not supported. Use %s", idpType, TypeWSO2IS)
	}
}

// SetAdapter replaces the adapter.
func SetAdapter(a Adapter) {

	adapterMu.Lock()
	adapter = a
	adapterMu.Unlock()
}
