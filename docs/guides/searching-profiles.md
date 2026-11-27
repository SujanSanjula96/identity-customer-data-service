# Searching Profiles

`GET /profiles` lists an organization's profiles. Narrow the list with an
exact `filter`, a tolerant `fuzzyFilter`, or both.

| Parameter | Finds |
|---|---|
| `filter` | Profiles whose values satisfy a condition exactly |
| `fuzzyFilter` | Profiles whose values are *similar* to the ones given — a misspelt name, an email address or phone number written differently — each with a match score |
| both | Profiles similar on the `fuzzyFilter` values **and** exactly satisfying every `filter` condition |

Every search needs the `profile:view` scope.

---

## Exact search

A condition is written `<attribute> <operator> <value>`:

```
GET /profiles?filter=traits.city eq Colombo
GET /profiles?filter=identity_attributes.email sw jane
```

| Operator | Meaning |
|---|---|
| `eq` | equals |
| `co` | contains |
| `sw` | starts with |

Repeat `filter`, or join conditions with ` and `, to require several at once.

Results come a page at a time: `page_size` (default 5, at most 200), and
`next_cursor` / `previous_cursor` in the response to move between pages.

---

## Fuzzy search

```
GET /profiles?fuzzyFilter=traits.name eq Jonathan Smith
```

finds `Jonathon Smyth` as well as `Jonathan Smith`. Each profile comes back
with a `match_score` between 0 and 1 and a `score_breakdown` of what each rule
scored, highest score first:

```json
{
  "pagination": { "count": 2, "page_size": 5 },
  "profiles": [
    {
      "profile_id": "e8bf7337-4049-4d92-9545-711894ab2af3",
      "meta": { "...": "..." },
      "match_score": 1.0,
      "score_breakdown": { "name": 1.0 }
    },
    {
      "profile_id": "40497337-e8bf-4d92-9545-711894ab2af3",
      "meta": { "...": "..." },
      "match_score": 0.9429,
      "score_breakdown": { "name": 0.9429 }
    }
  ]
}
```

Fuzzy search uses the same engine as profile unification, so it is set up the
same way:

1. **Check that tolerant matching is on for the server**, in
   `repository/conf/deployment.yaml`. The shipped configuration turns it on;
   a configuration without the key keeps it off. While it is
   off, `fuzzyFilter` returns exact matches only.

   ```yaml
   identity_resolution:
     fuzzy_matching_enabled: true
   ```

2. **Have a unification rule for each attribute you search on**, with the
   `unification_method` `fuzzy`. An attribute no rule names is not searched,
   and the rule's `attribute_type` — `NAME`, `EMAIL`, `PHONE`, `LOCATION`,
   `FUZZY_STRING` — decides how two values are compared. See
   [Unification Rules](../concepts/unification-rules.md) and
   [how values are compared](../concepts/how-unification-works.md#comparing-two-values).

`threshold` sets the lowest score returned. Leave it out and the organization's
`manual_review_threshold` (0.75 unless changed) is used.

A search only reports. It never merges profiles and never raises a review
task, whatever the scores.

---

## Choosing what comes back

Each listed profile carries only `profile_id`, `user_id`, `meta` and
`merged_from` unless you ask for more with `attributes`:

```
GET /profiles?filter=traits.city eq Colombo&attributes=traits.city,identity_attributes
```

A name with a dot selects one attribute; a scope alone (`identity_attributes`,
`traits`, `application_data`) selects all of it.

A search returns what the caller may read, as fetching one profile does. An
application listed in the organization's `system_applications` gets full
profiles. Any other caller gets, for each profile, only its own application
data and the attributes that profile has consented to share. Pass
`consentCategoryId` as on a single fetch. A condition only selects a profile by
a value the caller could read on it, so a page can hold fewer profiles than
`page_size`. See [Consent](../concepts/consent.md#profile-listing) and
[Profiles](../concepts/profiles.md#application-data).
