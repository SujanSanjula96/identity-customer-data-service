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

// Package audit writes the audit log of CDS: who did what to which resource, in which
// organization, and with what outcome.
//
// An HTTP operation is audited where its route is registered:
//
//	mux.HandleFunc("DELETE "+base+"/profiles/{profileId}", audit.Wrap(audit.ProfileDelete, h.DeleteProfile))
//
// Wrap takes the actor, the organization, and the client from the request, the target ID from the
// path, and the outcome from the response. A handler adds what only it knows:
//
//	audit.SetTargetID(r.Context(), created.ID)
//	audit.AddDetail(r.Context(), "decision", "approved")
//
// An operation with no request, such as a merge in a worker, calls Log.
//
// An audit record holds no attribute values, user names, tokens, or request bodies. It still holds
// IDs, the client IP address, and the user agent, which can identify a person. See the logging guide.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

const (
	// SystemActorID identifies CDS itself as the actor of an operation with no request.
	SystemActorID = "cds"

	maxIDLength     = 128
	maxDetailLength = 256
)

// Options configures the audit log.
type Options struct {
	// Enabled turns the audit log on.
	Enabled bool
	// Output selects the destinations.
	Output log.OutputOptions
}

// Entry is one audited operation with no HTTP request.
type Entry struct {
	Event    Event
	TargetID string
	Outcome  Outcome
	// OrgHandle and OrgID name the organization, when the context does not carry it.
	OrgHandle string
	OrgID     string
	Details   map[string]any
}

type auditor struct {
	mu      sync.Mutex
	out     io.Writer
	closer  io.Closer
	enabled bool
}

var current = &auditor{}

// Init configures the audit log. Before Init runs, the audit log is off.
func Init(opts Options) error {
	var out io.Writer
	var closer io.Closer
	if opts.Enabled {
		var err error
		if out, closer, err = log.NewOutput(opts.Output); err != nil {
			return err
		}
	}
	setOutput(out, closer, opts.Enabled)
	return nil
}

// Close releases the audit log file. Call it at shutdown.
func Close() error {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.closer == nil {
		return nil
	}
	err := current.closer.Close()
	current.closer = nil
	current.out = os.Stdout
	return err
}

func setOutput(out io.Writer, closer io.Closer, enabled bool) {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.closer != nil {
		_ = current.closer.Close()
	}
	current.out, current.closer, current.enabled = out, closer, enabled
}

// Log records an operation with no HTTP request. The actor is the actor of the request in ctx,
// when there is one, and CDS itself otherwise.
func Log(ctx context.Context, entry Entry) {
	info := reqctx.From(ctx)
	actor := reqctx.Actor{Type: reqctx.ActorSystem, ID: SystemActorID}
	if info != nil {
		actor = info.Actor()
	}
	handle, orgID := info.Org()
	if entry.OrgHandle != "" {
		handle = entry.OrgHandle
	}
	if entry.OrgID != "" {
		orgID = entry.OrgID
	}
	write(info, entry.Event, actor, handle, orgID, entry.TargetID, entry.Outcome, entry.Details)
}

// shouldRecord reports whether the audit log is on. CDS audits only the operations that change
// data, so every event in the catalog is recorded.
func shouldRecord() bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.enabled
}

// write builds the record and writes it as one JSON line in one Write call.
func write(info *reqctx.Info, event Event, actor reqctx.Actor, orgHandle, orgID, targetID string,
	outcome Outcome, details map[string]any) {
	if !shouldRecord() {
		return
	}
	rec := record{
		LogType:       log.TypeAudit,
		SchemaVersion: SchemaVersion,
		ID:            uuid.NewString(),
		Time:          time.Now().UTC().Format(log.TimeLayout),
		Event:         event.Name(),
		Action:        string(event.Action),
		CorrelationID: info.CorrelationID(),
		Actor: actorRef{
			Type:     actor.Type,
			ID:       limit(actor.ID, maxIDLength),
			ClientID: limit(actor.ClientID, maxIDLength),
		},
		Target:  targetRef{Type: event.TargetType, ID: limit(targetID, maxIDLength)},
		Outcome: outcome,
		Details: limitDetails(details),
	}
	if orgHandle != "" || orgID != "" {
		rec.Org = &orgRef{Handle: orgHandle, ID: orgID}
	}
	if ip, userAgent := info.Client(); ip != "" || userAgent != "" {
		rec.Client = &clientRef{IP: ip, UserAgent: userAgent}
	}

	var line bytes.Buffer
	encoder := json.NewEncoder(&line)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rec); err != nil {
		log.GetLogger().WithComponent("Audit").Error("Failed to encode an audit record.",
			log.String("event", rec.Event), log.Error(err))
		return
	}

	current.mu.Lock()
	defer current.mu.Unlock()
	if current.out == nil {
		return
	}
	if _, err := current.out.Write(line.Bytes()); err != nil {
		log.GetLogger().WithComponent("Audit").Error("Failed to write an audit record.",
			log.String("event", rec.Event), log.Error(err))
	}
}

// limitDetails cuts the string values of details, so that one record stays small.
func limitDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	limited := make(map[string]any, len(details))
	for key, value := range details {
		if text, ok := value.(string); ok {
			value = limit(text, maxDetailLength)
		}
		limited[key] = value
	}
	return limited
}

func limit(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
