# Profile Schema

The **profile schema** defines the shape of data that can be stored on a profile for a given organisation. It controls attribute names, types, mutability, and how values are resolved when two profiles are merged.

---

## Schema scopes

Profile attributes are organised into three scopes:

| Scope | Description |
|---|---|
| `identity_attributes` | Attributes sourced from the identity system (e.g. email, phone, name from IS claims) |
| `traits` | Behavioural or preference data managed by the application (e.g. language, segments) |
| `application_data` | Per-application attributes, namespaced by application identifier |

Attribute names must be prefixed with their scope, e.g. `identity_attributes.email`, `traits.preferred_language`, `application_data.score`.

---

## Attribute fields

| Field | Required | Description |
|---|---|---|
| `attribute_id` | auto | System-generated UUID |
| `attribute_name` | yes | Dot-notation path including scope prefix |
| `display_name` | no | Human-readable label |
| `value_type` | yes | One of the supported data types (see below) |
| `merge_strategy` | yes | How to resolve the value when two profiles are merged |
| `mutability` | yes | Read/write behaviour (see below) |
| `multi_valued` | no | If `true`, the attribute holds an array of the declared type |
| `canonical_values` | no | Enumerated allowed values (for string attributes) |
| `sub_attributes` | no | Child attributes when `value_type` is `complex` |
| `application_identifier` | no | Scopes the attribute to a specific application (for `application_data`) |

---

## Supported value types

| Type | Description |
|---|---|
| `string` | Plain text |
| `integer` | Whole number |
| `decimal` | Floating-point number |
| `boolean` | `true` / `false` |
| `date` | Calendar date |
| `date_time` | Date and time |
| `epoch` | Unix timestamp (milliseconds) |
| `complex` | Nested object — define child fields in `sub_attributes` |

---

## Mutability

Mutability controls whether an attribute value can be changed after it is set.

| Value | Meaning |
|---|---|
| `readWrite` | Can be freely read and updated |
| `readOnly` | System-managed — cannot be updated via the API (e.g. `meta.created_at`) |
| `writeOnly` | Can be written but not read back |
| `immutable` | Must be set at creation — cannot be changed (e.g. `profile_id`) |
| `writeOnce` | Can be empty initially; once set, cannot be updated (e.g. `user_id`) |

---

## Merge strategies

When two profiles are unified, the merge strategy for each attribute determines which value wins.

| Strategy | Behaviour |
|---|---|
| `overwrite` | The incoming profile's value replaces the existing one |
| `combine` | Both values are combined into an array (requires `multi_valued: true`) |

---

## Core schema

The following attributes are built into every profile regardless of the org schema and cannot be modified:

| Attribute | Type | Mutability |
|---|---|---|
| `profile_id` | `string` | `immutable` |
| `user_id` | `string` | `writeOnce` |
| `meta.created_at` | `date_time` | `readOnly` |
| `meta.updated_at` | `date_time` | `readOnly` |
| `meta.location` | `string` | `readOnly` |

---

## Schema synchronisation with IS

When a claim is added, updated, or deleted in WSO2 Identity Server, CDS receives a schema sync event and reconciles its local schema accordingly. This keeps `identity_attributes` in CDS aligned with IS claim dialects.

See [guides/schema-sync.md](../guides/schema-sync.md) for the full sync flow.
