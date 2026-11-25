# Logging

CDD writes three log streams. Each record has a `log_type` field that names its stream.

| Stream | `log_type` | Content | File |
|---|---|---|---|
| Application log | `app` | What the server and its workers do | `cds.log` |
| Access log | `access` | One record for each HTTP request | `http_access.log` |
| Audit log | `audit` | Who did what to which resource, with what outcome | `audit.log` |

All timestamps are UTC with milliseconds, for example `2026-11-11T09:30:15.123Z`.

## Configuration

Configure logging in the `log` section of `deployment.yaml`. All keys are optional. With no `log` section, all log
types write text records to standard output at `INFO` level.

Each log type (`app`, `access`, `audit`) has two outputs: `console` and `file`. The `log.outputs` block holds the
defaults for all log types. The block of a log type can override each key.

```yaml
log:
  outputs:                     # Defaults for all log types
    console:
      enabled: true
      format: "text"           # text or json
    file:
      enabled: false
      format: "json"           # text or json
      dir: "repository/logs"   # Relative to CDS_HOME unless absolute
      max_size_mb: 10          # A file also rotates each day
      max_backups: 20          # Rotated files to keep for each log type
  app:
    level: "INFO"              # DEBUG, INFO, WARN, ERROR
    outputs:
      file:
        name: "cds.log"
  access:
    enabled: true
    outputs:
      file:
        name: "http_access.log"
  audit:
    enabled: true
    outputs:
      file:
        name: "audit.log"
```

CDD finds the value of an output key in this sequence:

1. The block of the log type, for example `log.audit.outputs.file.enabled`.
2. The default block, `log.outputs.file.enabled`.
3. The built-in default.

| Key | Valid in | Default |
|---|---|---|
| `log.app.level` | `app` only | `INFO` |
| `log.<type>.enabled` | `access`, `audit` | `true` |
| `outputs.console.enabled` | `log.outputs` and each type | `true` |
| `outputs.console.format` | `log.outputs` and each type | `text` |
| `outputs.file.enabled` | `log.outputs` and each type | `false` |
| `outputs.file.format` | `log.outputs` and each type | `json` |
| `outputs.file.name` | Each type only | `cds.log`, `http_access.log`, `audit.log` |
| `outputs.file.dir`, `max_size_mb`, `max_backups` | `log.outputs` and each type | `repository/logs`, `10`, `20` |

Rules:

- The level applies only to the application log. The access log and the audit log have no level: use `enabled`.
- The audit log is always JSON. CDD does not start if `log.audit.outputs.<output>.format` is not `json`.
- CDD does not start if `log.outputs.file.name` is set, or if a file name is a path.
- `log.log_level` stays valid for existing deployment files. `log.app.level` takes precedence.
- A rotated file has the date of its content and a sequence number, for example `cds-2026-11-11.1.log`.
- If a log type has no output enabled, it writes to the console. A configuration error cannot silence the logs.
- When all log types go to standard output, use the `log_type` field to separate them in a log collector.

Example: the application log on the console, and the access log and the audit log in files only.

```yaml
log:
  outputs:
    file:
      enabled: true
  app:
    level: "INFO"
    outputs:
      file:
        enabled: false
  access:
    outputs:
      console:
        enabled: false
  audit:
    outputs:
      console:
        enabled: false
```

## Correlation ID

Each request has a correlation ID. All three streams report it as `correlation_id`.

- Send the ID in the `X-Correlation-ID` header (or `X-Request-ID`). CDD accepts 1 to 64 characters from
  `A-Z a-z 0-9 . _ -`.
- If the request has no valid ID, CDD creates a UUID.
- CDD returns the ID in the `X-Correlation-ID` response header.

## Application log

Text format:

```
time=2026-11-11T09:30:15.123Z level=INFO msg="Profile created." log_type=app component=ProfileService profile_id=3f6c2a1e correlation_id=8fa8f0b5 org=carbon.super
```

JSON format:

```json
{"time":"2026-11-11T09:30:15.123Z","level":"INFO","msg":"Profile created.","log_type":"app","component":"ProfileService","profile_id":"3f6c2a1e","correlation_id":"8fa8f0b5","org":"carbon.super"}
```

## Access log

```json
{"time":"2026-11-11T09:30:15.141Z","level":"INFO","msg":"request","log_type":"access","method":"PATCH","path":"/t/carbon.super/cds/api/v1/profiles/3f6c2a1e","protocol":"HTTP/1.1","status":200,"bytes":512,"duration_ms":18,"client_ip":"10.0.0.7","user_agent":"curl/8.7.1","correlation_id":"8fa8f0b5","org":"carbon.super"}
```

- `error_code` is present when the response is a CDD error.
- The query string is not logged, because it can hold personal data.
- `client_ip` is the address of the TCP peer. CDD does not use `X-Forwarded-For`.
- A health or readiness request that succeeds has no record.

## Audit log

Each audit record is one JSON line.

```json
{
  "log_type": "audit",
  "schema_version": 1,
  "id": "0b0f6f5e-7a53-4a0e-9c35-0f3f3f8f6f11",
  "time": "2026-11-11T09:30:15.123Z",
  "event": "profile.update",
  "action": "update",
  "correlation_id": "8fa8f0b5-263d-4876-9480-8b4666cd20c0",
  "org": { "handle": "carbon.super" },
  "actor": { "type": "user", "id": "a91904ff-155a-4f34-ab93-99b6cf0c818a", "client_id": "CONSOLE" },
  "target": { "type": "profile", "id": "3f6c2a1e-5d0b-4f0e-8a51-0c8f1f1d2b77" },
  "outcome": { "result": "succeeded", "status": 200 }
}
```

| Field | Description |
|---|---|
| `schema_version` | Version of the record. It changes only when a field is removed or changes meaning. |
| `id` | Unique ID of the record. |
| `event` | `<target.type>.<action>` |
| `action` | `create`, `update`, `delete`, `sync`, `link`, `merge`, `resolve` |
| `org` | Organization of the request: `handle`, and `id` for a sub organization path or a worker operation. |
| `actor.type` | `user`, `application`, `service` (the deployment credentials), `system` (CDD itself), `anonymous` |
| `actor.id` | Token subject (`user`), client ID (`application`), `admin_credentials` (`service`), or `cds` (`system`). Absent for `anonymous`, and for a `user` whose token has no subject. Never a user name. |
| `actor.client_id` | Only for a `user` actor: the OAuth client (application) that the user acted through. |
| `target` | Resource type, and the resource ID when the operation has one target. |
| `outcome.result` | `succeeded`, `denied` (authentication or authorization refused it), `failed` |
| `outcome.reason` | Stable code: `unauthenticated`, `forbidden`, the CDD error code of the response, `client_error`, or `server_error`. Absent on success. |
| `outcome.status` | HTTP status. Absent for an operation with no request. |
| `client` | `ip` and `user_agent` of the request. |
| `details` | Data of the event. See the event tables. |

An audit record does not hold profile attribute values, user names, tokens, or request bodies. It still holds data that
can identify a person or can be linked to one:

- `actor.id` of a user (the token subject) and `details.user_id`.
- Resource IDs, for example the profile ID in `target.id`.
- `client.ip` and `client.user_agent`.

Treat the audit log as personal data: limit access to it, and set a retention period.

### Recording rules

1. CDD audits only the operations that change data. A read or list operation has no audit record, also when it is
   denied. The access log records every request, reads included.
2. An audited operation is recorded for every outcome: succeeded, denied, or failed.

### Events

Profiles:

| Event | Operation | Details |
|---|---|---|
| `profile.create` | `POST /profiles` | |
| `profile.update` | `PATCH /profiles/{profileId}` | |
| `profile.delete` | `DELETE /profiles/{profileId}` | |
| `profile.sync` | `POST /profiles/sync` | `sync_event`, `synced_org` |
| `profile.link` | `POST /profiles/link` | `user_id` |
| `profile.merge` | Profile unification (worker or approved review task) | `merged_profile_ids`, `reason` |
| `profile_consent.update` | `PUT /profiles/{profileId}/consents` | |

Profile schema:

| Event | Operation | Details |
|---|---|---|
| `profile_schema.delete` | `DELETE /profile-schema` | |
| `profile_schema.sync` | `POST /profile-schema/sync` | `sync_event`, `synced_org` |
| `profile_schema_attribute.create` | `POST /profile-schema/{scope}` | `scope` |
| `profile_schema_attribute.update` | `PUT /profile-schema/{scope}/{attrID}` | `scope` |
| `profile_schema_attribute.delete` | `DELETE /profile-schema/{scope}/{attrID}`, `DELETE /profile-schema/{scope}` | `scope` |
| `profile_schema_sharing_policy.<action>` | `/profile-schema/{scope}/{attrID}/sharing-policies` | `scope`, `attribute_id` |

Unification rules:

| Event | Operation | Details |
|---|---|---|
| `unification_rule.create` | `POST /unification-rules` | |
| `unification_rule.update` | `PATCH /unification-rules/{ruleId}` | |
| `unification_rule.delete` | `DELETE /unification-rules/{ruleId}` | |
| `unification_rule_sharing_policy.<action>` | `/unification-rules/{ruleId}/sharing-policies` | `rule_id` |

Consent, configuration, identity resolution, and organizations:

| Event | Operation | Details |
|---|---|---|
| `consent_category.create` | `POST /consent-categories` | |
| `consent_category.update` | `PUT /consent-categories/{categoryId}` | |
| `consent_category.delete` | `DELETE /consent-categories/{categoryId}` | |
| `admin_config.update` | `PATCH /config` | |
| `admin_config.sync` | `POST /config/sync` | |
| `review_task.resolve` | `POST /identity-resolution/review-tasks/{taskId}/resolve` | `decision` |
| `organization.sync` | `POST /organizations/event-sync` (target: `org_id` of the body) | `sync_event` |
| `organization_tree.sync` | `POST /organizations/sync` | |
| `organization_access_policy.<action>` | `/organizations/access-policies` | |
| `service_credentials.sync` | `POST /service-credentials/sync` | |

## For developers

The framework is in `internal/system`:

| Package | Use |
|---|---|
| `log` | The logger, the standard field names, the formats, and the outputs |
| `reqctx` | The facts of one request: correlation ID, organization, actor, client |
| `httpmw` | The request context middleware and the access log middleware |
| `audit` | The audit event catalog, `Wrap` for routes, and `Log` for workers |

### Write an application record

```go
var logger = log.GetLogger().WithComponent("ConsentService")

func (s *Service) Update(ctx context.Context, id string) error {
    logger.WithContext(ctx).Info("Consent category updated.", log.String("category_id", id))
    return nil
}
```

Rules:

- Give the logger the context with `WithContext(ctx)`. The record then has `correlation_id` and `org`.
- Put values in fields, not in the message text. Use the names in `log/field.go`. A new name is in `snake_case`.
- Do not log personal data, tokens, or secrets. If a record must show that a value was present, use
  `log.MaskedString`.
- Levels: `ERROR` when an operation did not complete and an operator must look. `WARN` when it completed but
  something is wrong. `INFO` for a state change of the server or of a background job. `DEBUG` for developer detail.
- Do not write an `INFO` record for each request. The access log does that.

### Audit an operation

For an HTTP operation, name the event where you register the route:

```go
mux.HandleFunc("DELETE "+base+"/profiles/{profileId}", audit.Wrap(audit.ProfileDelete, h.DeleteProfile))
```

`audit.Wrap` records every outcome. It takes the actor, the organization, and the client from the request, the target
ID from the path, and the outcome from the response.

A handler adds the data that only it knows:

```go
audit.SetTargetID(r.Context(), created.ID)
audit.AddDetail(r.Context(), "decision", req.Decision)
```

For an operation with no request, such as a job in a worker:

```go
audit.Log(ctx, audit.Entry{
    Event:    audit.ProfileMerge,
    TargetID: master.ProfileId,
    Outcome:  audit.Succeeded(),
    OrgID:    orgId,
})
```

To add a new audited operation:

1. Add the `Event` variable to `internal/system/audit/event.go`. Use an existing action if one fits.
2. Wrap the route with `audit.Wrap`, or call `audit.Log`.
3. Add the event to the tables in this guide.
