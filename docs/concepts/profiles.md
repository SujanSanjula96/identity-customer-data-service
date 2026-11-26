# Profiles

A **profile** is the central entity in CDD. It represents a single person's collected data — their identity attributes, behavioural traits, and per-application data — unified across all interactions.

---

## Profile structure

| Field | Description |
|---|---|
| `profile_id` | System-generated UUID. Immutable. |
| `user_id` | The user's ID from Identity Server. Empty on anonymous profiles. Write-once once set. |
| `identity_attributes` | Attributes sourced from the identity system (e.g. email, phone, name). Keyed by attribute name. |
| `traits` | Behavioural or preference data (e.g. preferred language, segment tags). |
| `application_data` | Per-application data, keyed by application identifier. |
| `meta.created_at` | When the profile was created. Read-only. |
| `meta.updated_at` | When the profile was last modified. Read-only. |
| `merged_from` | References to profiles that were unified into this one via unification rules. |
| `merged_to` | Set on child profiles — points to the master profile they were merged into. |

---

## Application data

Each application keeps its own data on a profile, under its application
identifier, and **one application never sees another's**.

`GET /profiles/{profileId}` returns application data only when asked, with
`includeApplicationData=true`, and then returns only the calling application's
own entry. The caller is the application the access token was issued to,
identified as `application_identifier_type` in `deployment.yaml` says — the
Identity Server application ID (`app_id`, the default) or its OAuth client ID.

Applications an organization lists in `system_applications`, in its
configuration (`PATCH /config`), are trusted to read across applications: they
get every application's data, or the ones named with `application_identifier`
(`*` for all). They also read profiles without consent filtering — see
[Consent](consent.md#consent-scoped-profile-fetch).

`GET /profiles` applies the same rules to every profile it lists.

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
                │  (SESSION_TERMINATE or SESSION_EXPIRE event)
                ▼
            cookie deactivated (is_active = false)
            cookie cleanup worker removes inactive records at its next run (every 24h by default)
```

---

## Cookies and session tracking

The `profile_cookies` table maps a `cookie_id` (the value of the `cds_profile` browser cookie) to a `profile_id`. The `is_active` flag tracks whether the session is live.

- Cookie is **created** when an anonymous profile is first issued
- Cookie is **deactivated** on `SESSION_TERMINATE` (logout) and `SESSION_EXPIRE` (session timed out)
- Inactive cookie records are **batch-deleted** by the cookie cleanup worker every 24 hours

## Retention of temporary profiles

A temporary profile that is not updated is deleted after a retention period, together with its cookies, application data, consents and blocking keys.

- A profile counts as temporary when it has no `user_id`, is not merged into another profile, and has no profile merged into it. Identified profiles and merged profiles are never deleted by this job.
- The clock is the profile's `updated_at`. A visit that does not change the profile does not reset it.
- An organization sets these in its admin configuration. A sub organization follows its primary organization.

  | Setting | Meaning | Default | Allowed |
  |---|---|---|---|
  | `temporary_profile_retention_days` | Days without an update before a profile is deleted | 90 | 1 or more |
  | `temporary_profile_cleanup_interval_days` | Days between two cleanups | 30 | 1 or more |

- The server checks every hour and cleans an organization when its interval has passed. After a restart, an organization waits one interval before its first cleanup.
- A cleanup deletes 500 profiles at a time and repeats until nothing is left to delete, up to 200 batches. The rest waits for the next cleanup. The batch size is fixed in the code and is not a setting.
- Deletion is permanent.

