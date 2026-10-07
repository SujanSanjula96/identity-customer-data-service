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

// Package idp holds the contract between CDS and an identity provider for B2B data. CDS reads
// the org tree only through an Adapter, so that CDS does not depend on one identity provider.
package idp

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Organization is an org as the identity provider reports it.
type Organization struct {
	Id       string
	Handle   string
	Name     string
	ParentId string // Empty for a root org.
	Status   string
}

// Adapter is the org part of the IdP integration contract.
type Adapter interface {
	// GetRootOrganization returns the org that rootHandle names. The org must be a root org.
	GetRootOrganization(ctx context.Context, rootHandle string) (Organization, error)
	// ListDescendants returns all orgs below the root, each with its parent.
	ListDescendants(ctx context.Context, rootHandle string) ([]Organization, error)
	// GetOrganization returns one org below the root. It returns nil when the org does not exist.
	GetOrganization(ctx context.Context, rootHandle, orgId string) (*Organization, error)
	// IdentityAttributeSourceHandle returns the handle of the org to read the identity attributes
	// of an org from. An IdP that manages attributes for each org returns orgHandle.
	IdentityAttributeSourceHandle(orgHandle, rootHandle string) string
}

var (
	adapterMu sync.RWMutex
	adapter   Adapter
)

// GetAdapter returns the configured adapter. The default is the WSO2 IS adapter.
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
	TypeWSO2IS    = "wso2is"
	TypeThunderID = "thunderid"
)

// NewAdapter returns the adapter of the configured identity provider. One CDS deployment works with
// one identity provider (R-015). An empty type is WSO2 IS.
func NewAdapter(idpType string) (Adapter, error) {

	switch strings.ToLower(strings.TrimSpace(idpType)) {
	case "", TypeWSO2IS:
		return NewWSO2ISAdapter(), nil
	case TypeThunderID:
		return nil, fmt.Errorf("the ThunderID adapter is not available yet")
	default:
		return nil, fmt.Errorf("the identity provider type '%s' is not supported. Use %s or %s", idpType,
			TypeWSO2IS, TypeThunderID)
	}
}

// SetAdapter replaces the adapter. The server sets the configured adapter at start, and tests use it
// to plug in a fake identity provider.
func SetAdapter(a Adapter) {

	adapterMu.Lock()
	adapter = a
	adapterMu.Unlock()
}
