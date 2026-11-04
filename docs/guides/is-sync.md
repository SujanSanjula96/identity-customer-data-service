# IS Sync — Identity Server Event Integration

CDS receives lifecycle events from WSO2 Identity Server (IS) via a webhook-style HTTP endpoint. These events keep CDS profiles and schema in sync with identity data as users are created, updated, authenticated, and deleted.

---

## Endpoint

```
POST /cds/api/profiles/sync
Authorization: Basic <admin credentials>
```

All events use the same endpoint. The `event` field in the request body determines how CDS handles the payload.

---

## Events

### `POST_ADD_USER`

Fired when a new user is registered in IS.

**With cookie (anonymous → registered):**
1. Resolves the anonymous profile from the cookie
2. Attaches the `userId` and merges incoming claims into `identity_attributes`
3. Saves the updated profile — the unification worker promotes it to permanent

**Without cookie:**
1. Checks if a profile already exists for the `userId`
2. If not, creates a new permanent profile with the claims as `identity_attributes`

---

### `AUTHENTICATION_SUCCESS`

Fired on every successful login.

| Scenario | Action |
|---|---|
| Cookie present + anonymous profile + existing permanent profile (different) | Attaches `userId` to the anonymous profile and enqueues for unification. Worker merges anonymous into permanent via `system:user_id_match`. |
| Cookie present + anonymous profile + no permanent profile | Promotes the anonymous profile to permanent by attaching `userId` |
| Cookie present + already the user's profile | No-op |
| No cookie | No-op (logged) |

---

### `POST_SET_USER_CLAIM_VALUE_WITH_ID` / `POST_SET_USER_CLAIM_VALUES_WITH_ID`

Fired when one or more claims are updated for a user in IS.

1. Looks up the permanent profile by `userId`
2. Translates IS claim URIs to CDS attribute key paths
3. Merges the new values into `identity_attributes`
4. Saves the updated profile

---

### `SESSION_TERMINATE`

Fired when a user session ends (logout or expiry).

1. Looks up the permanent profile by `userId`
2. Resolves the cookie record by `profileCookie`
3. Verifies the cookie belongs to the profile (or one of its merged children)
4. Deactivates the cookie (`is_active = false`)

The cookie cleanup worker removes deactivated cookie records in batches after 24 hours.

---

### `POST_DELETE_USER_WITH_ID`

Fired when a user is deleted from IS.

1. Looks up the profile by `userId`
2. Soft-deletes the profile (`delete_profile = true`, `list_profile = false`)

---

## Schema sync events

Schema changes in IS (claim additions, updates, deletions) are delivered to a separate endpoint and handled by the schema sync worker. See [schema-sync.md](schema-sync.md).
