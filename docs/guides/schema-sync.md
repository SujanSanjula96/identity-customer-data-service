# Schema Sync — Keeping CDS Schema Aligned with IS

When claim definitions change in WSO2 Identity Server, CDS must update its local profile schema to stay aligned. Schema sync is driven by IS events and processed asynchronously via the schema sync worker queue.

---

## Endpoint

```
POST /cds/api/profile-schema/sync
Authorization: Basic <admin credentials>
```

---

## Request payload

```json
{
  "event": "POST_ADD_EXTERNAL_CLAIM",
  "orgId": "carbon.super"
}
```

| Field | Description |
|---|---|
| `event` | The IS event type (see below) |
| `orgId` | The organisation whose schema should be re-synced |

---

## Events

| Event constant | IS trigger |
|---|---|
| `POST_ADD_EXTERNAL_CLAIM` | A new SCIM claim was added in IS |
| `POST_UPDATE_EXTERNAL_CLAIM` | An existing SCIM claim was updated |
| `POST_DELETE_EXTERNAL_CLAIM` | A SCIM claim was deleted |
| `POST_UPDATE_LOCAL_CLAIM` | A local claim was updated |
| `POST_DELETE_LOCAL_CLAIM` | A local claim was deleted |

All events result in the same action: a full re-sync of the org's schema from IS.

---

## How schema sync works

1. IS fires a schema event to the CDS sync endpoint
2. CDS enqueues a `ProfileSchemaSync` job (containing `orgId` and `event`) onto the `SchemaSyncQueue`
3. The schema sync worker picks up the job and calls `SyncProfileSchema(orgId)`
4. `SyncProfileSchema` fetches the current claim dialects from IS via the Identity Client and reconciles them with the locally stored schema attributes for the org
5. New attributes are added, changed attributes are updated, removed attributes are deleted

The sync is **best-effort** — if it fails, an error is logged but the HTTP response to IS is not affected. IS will retry on the next relevant claim change.

---

## Initial sync

When CDS is first enabled for an organisation (`cds_enabled` config flag), an initial schema sync is triggered automatically to bootstrap the local schema from the current IS claim state. The `initial_schema_sync_done` config flag is set once this completes successfully.

---

## Relationship to profile data

Schema attributes define the allowed shape of `identity_attributes`, `traits`, and `application_data` on profiles. Schema sync only affects the schema definition — it does not modify existing profile data. However, after sync the updated merge strategies and mutability rules apply to all subsequent profile writes and unification runs.
