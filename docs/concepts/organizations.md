# Organizations (B2B)

CDD follows the organization tree of the Identity Server. Every organization —
the primary organization and each sub-organization below it — has its **own
profiles**, and a profile never crosses from one organization to another. What
an organization can pass down is its *configuration*: the profile schema
attributes and unification rules it chooses to share with the organizations
below it.

---

## Switching it on

Organization support is on in the shipped `repository/conf/deployment.yaml`.
A `deployment.yaml` without the key keeps it off. The switch is:

```yaml
organization_management:
  enabled: true
```

The organization tables are part of every database script in
`repository/dbscripts/` and of the inbuilt database, so a new database needs
nothing else.

---

## The organization tree

CDD keeps its own copy of the tree, built from the Identity Server:

| How | What it does |
|---|---|
| `POST /organizations/sync` | Copies the whole tree. Only the primary organization can start it; it runs in the background |
| `GET /organizations/sync-status` | `NOT_SYNCED`, `SYNCING`, `SYNCED` or `FAILED`, with when the last sync started and finished |
| `POST /organizations/event-sync` | The Identity Server reports `ORG_CREATED`, `ORG_UPDATED` and `ORG_DELETED` as they happen. Events that arrive before the first sync are ignored |

Run the first sync once, after enabling CDD for the primary organization. The
events keep the tree current from then on. The access policy is refused until
the tree has been synced, and a sharing policy can only name organizations CDD
already knows.

---

## Which sub-organizations use CDD

A primary organization is enabled from the Identity Server, like any
organization. Its sub-organizations are not enabled one by one: the primary
organization holds **one organization access policy** that selects which of
them can use CDD.

```
POST /organizations/access-policies
```

```json
{
  "child_orgs": {
    "mode": "selected",
    "orgs": [
      { "org_id": "9b2e4c71-5d8a-4f3e-a6b0-2c7d1e8f4a95", "reach": "subtree" }
    ]
  }
}
```

A sub-organization can use CDD when its primary organization has CDD enabled
**and** the access policy reaches it. Change the selection with `PUT` on the
policy; a second `POST` is refused with a 409.

`child_orgs` is the same shape everywhere in CDD — access policies and sharing
policies alike:

| `mode` | `orgs` | Reaches |
|---|---|---|
| `all` | must be empty | Every organization below, including ones created later |
| `selected` | one or more entries | The organizations listed, each by its `reach` |

| `reach` | Reaches |
|---|---|
| `org` | That organization only |
| `subtree` | That organization and everything below it, including organizations created later |

Each organization listed must be an active, **direct** child of the
organization that owns the policy, and may appear once. To reach deeper,
use `subtree`.

---

## Identity attributes

With the Identity Server, a sub-organization uses its primary organization's
identity attributes. They appear in its schema with the origin `INHERITED` and
are read-only there: the primary organization keeps them
aligned with the Identity Server (see [Schema Sync](../guides/schema-sync.md)).

---

## Sharing schema attributes and unification rules

An organization can share what it owns with the organizations below it, so a
customer-facing schema or a matching policy is defined once rather than in
every sub-organization.

| Resource | Endpoint |
|---|---|
| A trait or application data attribute | `POST /profile-schema/{scope}/{attrID}/sharing-policies` |
| A unification rule | `POST /unification-rules/{ruleId}/sharing-policies` |

Both take the `child_orgs` body above. The rules:

- **Only the owner shares.** An organization cannot pass on a resource that was
  shared with it.
- **Identity attributes are not shared** — they come from the Identity Server.
- **A complex attribute is shared as one unit.** Its sub-attributes follow the
  parent's policy and cannot be shared alone.
- **One policy per resource and organization.** Change it with `PUT`, stop
  sharing with `DELETE`.

### In the organizations that receive it

A shared attribute or rule shows up in the receiving organization's schema or
rule list with the origin `SHARED`, beside its own resources (origin `OWNED`).

- **It is read-only there.** A change from a receiving organization is refused
  with a 403 (`CDS-17015`); only the owner can change it.
- **It reserves its name.** The receiving organization cannot create its own
  attribute with the same name, or its own rule on the same property, while
  the shared one is active — that is a 409.
- **What was there first wins.** If the receiving organization already had an
  attribute of that name, or a rule on that property, the shared one does not
  apply there. When two ancestors share attributes of the same name, the
  nearer one applies; when two ancestors share rules on the same property, the
  one higher in the tree applies. A rule that does not apply is listed with the
  state `CONFLICTED`.
- **A shared rule needs its attribute.** A rule whose attribute the receiving
  organization cannot see is listed as `INACTIVE_MISSING_ATTRIBUTE` and does
  not run.
- **Shared rules rank first.** In a receiving organization they are evaluated
  ahead of its own rules, which follow in their own priority order.

Sharing passes on definitions only. Profiles are unified within one
organization: a profile in one sub-organization is never matched against, or
merged with, a profile in another.

---

## Settings a sub-organization follows

A sub-organization uses its primary organization's identity resolution
settings — `auto_merge_enabled`, `auto_merge_threshold`,
`manual_review_threshold` and `deterministic_match_decisive` — and its
temporary-profile retention settings. Change them in the primary
organization.

---

## Calling CDD for a sub-organization

A sub-organization's API sits under its primary organization's path:

```
https://<cdd-host>/t/<primary-org-handle>/o/<sub-org-id>/cds/api/v1/...
```

with a token issued for that sub-organization. Sub-organization tokens carry
the `internal_org_cds_*` scopes rather than `internal_cds_*`; the mapping is in
the `scopes` section of `deployment.yaml`.
