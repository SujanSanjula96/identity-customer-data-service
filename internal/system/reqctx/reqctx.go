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

// Package reqctx carries the facts of one request that every log stream reports: the correlation
// ID, the organization, the actor, and the client. The request middleware creates one Info per
// request and puts it on the context. Later stages, such as the tenant dispatcher and the
// authentication step, fill it in. The application log, the access log, and the audit log read it,
// so the three streams always agree.
//
// The package imports no other CDS package, so every component can use it.
package reqctx

import (
	"context"
	"sync"
)

// Actor types.
const (
	// ActorUser is a person, identified by the subject of the token.
	ActorUser = "user"
	// ActorApplication is an OAuth client that acts for itself.
	ActorApplication = "application"
	// ActorService is a trusted back-channel caller that uses the deployment credentials.
	ActorService = "service"
	// ActorSystem is CDS itself, for example a background worker.
	ActorSystem = "system"
	// ActorAnonymous is a caller with no verified identity.
	ActorAnonymous = "anonymous"
)

// Actor is the principal that made a request.
type Actor struct {
	// Type is one of the Actor* constants.
	Type string
	// ID identifies the principal. It is empty for an anonymous actor.
	ID string
	// ClientID is the OAuth client that the principal used, when one is known.
	ClientID string
}

// Info holds the facts of one request. All methods are safe on a nil Info and safe for concurrent
// use.
type Info struct {
	mu            sync.RWMutex
	correlationID string
	orgHandle     string
	orgID         string
	actor         Actor
	clientIP      string
	userAgent     string
}

type contextKey struct{}

// New creates the Info of a request.
func New(correlationID, clientIP, userAgent string) *Info {
	return &Info{correlationID: correlationID, clientIP: clientIP, userAgent: userAgent}
}

// With returns a context that carries info.
func With(ctx context.Context, info *Info) context.Context {
	return context.WithValue(ctx, contextKey{}, info)
}

// From returns the Info of the context, or nil when the context has none.
func From(ctx context.Context) *Info {
	if ctx == nil {
		return nil
	}
	info, _ := ctx.Value(contextKey{}).(*Info)
	return info
}

// Detach returns a new background context that keeps the Info of ctx. Use it for work that must
// continue after the request ends, so that its log records keep the correlation ID.
func Detach(ctx context.Context) context.Context {
	if info := From(ctx); info != nil {
		return With(context.Background(), info)
	}
	return context.Background()
}

// CorrelationID returns the correlation ID of the request.
func (i *Info) CorrelationID() string {
	if i == nil {
		return ""
	}
	return i.correlationID
}

// Client returns the client IP address and the user agent of the request.
func (i *Info) Client() (ip, userAgent string) {
	if i == nil {
		return "", ""
	}
	return i.clientIP, i.userAgent
}

// SetOrg records the organization of the request. orgID is set only for a sub organization path.
func (i *Info) SetOrg(handle, orgID string) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.orgHandle, i.orgID = handle, orgID
}

// Org returns the organization handle and the organization ID of the request.
func (i *Info) Org() (handle, orgID string) {
	if i == nil {
		return "", ""
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.orgHandle, i.orgID
}

// SetActor records the authenticated actor of the request.
func (i *Info) SetActor(actor Actor) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.actor = actor
}

// Actor returns the actor of the request. It is anonymous until authentication succeeds.
func (i *Info) Actor() Actor {
	if i == nil {
		return Actor{Type: ActorAnonymous}
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.actor.Type == "" {
		return Actor{Type: ActorAnonymous}
	}
	return i.actor
}
