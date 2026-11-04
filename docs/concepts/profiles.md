# Profiles

A **profile** is the central entity in CDS. It represents a single person's collected data — their identity attributes, behavioural traits, and per-application data — unified across all interactions.

---

## Profile structure

| Field | Description |
|---|---|
| `profile_id` | System-generated UUID. Immutable. |
| `user_id` | The user's ID from WSO2 Identity Server. Empty on anonymous profiles. Write-once once set. |
| `identity_attributes` | Attributes sourced from the identity system (e.g. email, phone, name). Keyed by attribute name. |
| `traits` | Behavioural or preference data (e.g. preferred language, segment tags). |
| `application_data` | Per-application data, keyed by application identifier. |
| `meta.created_at` | When the profile was created. Read-only. |
| `meta.updated_at` | When the profile was last modified. Read-only. |
| `merged_from` | References to profiles that were unified into this one via unification rules. |
| `merged_to` | Set on child profiles — points to the master profile they were merged into. |

---

## Temporary vs permanent profiles

Profiles are either **temporary** (anonymous) or **permanent** (identified).

**Temporary profile**
- Created when an anonymous user first interacts (e.g. visits a website)
- Has no `user_id`
- Linked to a browser cookie (`cds_profile`) stored in `profile_cookies`
- Accumulates behavioural data during the anonymous session

**Permanent profile**
- Has a `user_id` — set when the user logs in or is created in IS
- Acts as the master record for that identity
- May have one or more temporary profiles merged into it over time

---

## Profile lifecycle

```
Anonymous visit
      │
      ▼
Temporary profile created ──── cookie issued (profile_cookies)
      │
      │  (user logs in — AUTHENTICATION_SUCCESS event)
      ▼
userId attached to temp profile
      │
      ▼
Unification worker detects two profiles with same userId
      │
      ├── No existing permanent profile → temp profile promoted to permanent
      │
      └── Existing permanent profile found → temp profile merged in as child
                │
                ▼
            master profile updated with merged data
            temp profile marked as child (merged_to set)
                │
                │  (SESSION_TERMINATE event)
                ▼
            cookie deactivated (is_active = false)
            cookie cleanup worker removes inactive records after 24h
```

---

## Cookies and session tracking

The `profile_cookies` table maps a `cookie_id` (the value of the `cds_profile` browser cookie) to a `profile_id`. The `is_active` flag tracks whether the session is live.

- Cookie is **created** when an anonymous profile is first issued
- Cookie is **deactivated** on `SESSION_TERMINATE`
- Inactive cookie records are **batch-deleted** by the cookie cleanup worker every 24 hours

