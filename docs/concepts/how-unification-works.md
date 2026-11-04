# How Profile Unification Works

Profile unification is the process of recognising that two separate profile records represent the same person and merging them into a single master profile. It runs asynchronously via a background worker queue.

---

## Overview

```
Profile created / updated
         │
         ▼
  Enqueued only if a matched value changed,
  the userId changed, or a rule is newer
  than the profile   (ProfileUnificationQueue)
         │
         ▼
  Worker picks up profile
         │
         ├─ Step 1: userId match? ──yes──► merge (system:user_id_match)
         │
         └─ Step 2: rule-based match?
                   │
                   ├─ block:  find candidates sharing an index key
                   ├─ score:  evaluate every rule against each candidate
                   └─ decide: AUTO_MERGE / MANUAL_REVIEW / UNIQUE
```

---

## Step 1 — System userId match

If the incoming profile has a non-empty `userId`, CDS searches for a master profile with the same user ID in the organization. If one is found, the profiles are merged without evaluating unification rules.

This is a system-level invariant. It fires before any rules are evaluated and cannot be disabled.

**Guard:** if both profiles are permanent (both have a `userId`) but with *different* user IDs, they are **not** merged.

---

## Step 2 — Rule-based matching

Active rules are fetched, defaulted, and sorted by `priority` ascending. Candidates are found from the blocking index, then every candidate is scored against the full rule set and routed by the org's thresholds.

Deterministic and fuzzy rules are not alternative engines. A single comparison honours each rule's own `unification_method` — exact equality for `deterministic`, similarity for `fuzzy` — and aggregates them into one decision.

### Finding candidates

Comparing every profile against every other is O(n²) and unusable past a few thousand people, so candidates come from an index rather than a scan. Every value a rule matches on is reduced to one or more **blocking keys**, and two profiles are only compared when they already share a key.

The exact key is the value reduced exactly as far as its matcher reduces it before comparing, which guarantees that any pair the matcher would call identical lands in the same bucket. A rule matched `fuzzy` writes additional keys that widen recall:

| Rule | Value | Keys written |
|---|---|---|
| `EMAIL` deterministic | `J.Smith@Acme.com` | `j.smith@acme.com` |
| `EMAIL` fuzzy | `J.Smith@Acme.com` | `j.smith@acme.com` + 4 LSH band hashes |
| `NAME` fuzzy | `Jonathan Smith` | `jonathan smith`, `JN0N SM0`, `ANTN XMT`, + 4 LSH bands |
| `PHONE` fuzzy | `+94 77 123 4567` | `94771234567`, `1234567` (7-digit suffix) |
| `UNIQUE_ID` deterministic | `ABC-123` | `abc-123` |
| `DATE` deterministic | `Jan 2, 2026` | `2026.01.02` |

A deterministic rule writes the exact key and nothing else — LSH bands and phonetic codes would only gather values it can never score above zero. `DATE` and `UNIQUE_ID` normalise before keying because their matchers do, so `2026-01-02` and `Jan 2, 2026` meet.

Keys are looked up grouped by attribute **and** by key kind — exact, phonetic, LSH band, phone suffix — narrowest kind first, each group capped at 100 distinct profiles. A group matching more than that is skipped and logged: a value that common identifies nobody.

Each kind is queried separately because they do not share a selectivity. A phonetic code deliberately covers every spelling that sounds alike, while each spelling produces its own LSH bands, so for a common name the phonetic code saturates long before any band does. Sharing one lookup would let it exhaust the cap and discard the bands with it, losing exactly the typo matches they exist to find.

---

### How the index is stored

`blocking_keys` is the largest table in the system. A profile writes one key per deterministic rule and several per fuzzy rule, so a tenant with five rules holds roughly twenty rows per profile — half a billion rows at twenty-five million profiles. Three things about its definition were chosen by measurement rather than taste, and the figures below are from a million-row table on PostgreSQL 14 after `ANALYZE`.

**There is no surrogate key.** The table had a `key_id` UUID primary key that nothing referenced, while the natural key `(org_handle, attribute_name, key_value, profile_id)` already identified a row. The surrogate cost its own column and its own index on every row — roughly 38 GB at the target size, for an identifier no query used. The natural key is now the primary key, and its column order matches the candidate lookup: equality on `org_handle` and `attribute_name`, `IN` on `key_value`.

**It is partitioned in two levels** — by `org_handle`, and by `key_value` within each of those. Both single-key arrangements fail, in opposite ways:

| Partition key | Partitions scanned | Execution | Single-tenant deployment |
|---|---|---|---|
| `(org_handle, key_value)` combined | 16 | 19.1 ms | splits |
| `org_handle` alone | 1 | 0.095 ms | **every row in one partition** |
| **two levels** | **≤ 4** | **0.15 ms** | **splits** |

A combined hash key over both columns prunes nothing, because PostgreSQL prunes hash partitions only when it has an equality clause for every column of the key, and candidate lookup supplies `key_value` as an `IN` list. Partitioning on `org_handle` alone prunes perfectly but leaves a single-organisation deployment — the common shape on-premises — with every row in one partition, which is the case most in need of smaller indexes. Two levels prune at both: `org_handle` by equality, and `key_value` because single-column hash pruning does apply to an `IN` list.

**The moduli follow the access pattern.** A lookup touches at most one sub-partition per key it searches for, so widening the second level costs reads nothing. What holds it at four is the per-profile delete during re-indexing: it has no `key_value`, so it prunes to one organisation and then probes every sub-partition beneath it. The first level's eight is a fan-out sized for the target table rather than a measured optimum — 8 x 4 is 32 leaves, roughly 15M rows each at half a billion. Both are worth revisiting before this carries real data, because a hash modulus cannot be changed without rebuilding the table.

```
4 LSH keys        3 index-only scans    0.70 ms
1 exact key       1 index-only scan     0.17 ms
crowded phonetic  1 scan, 44 rows       0.94 ms
DELETE (reindex)  4 index scans         0.22 ms
```

SQLite has no partitioning, so the inbuilt datasource takes the key change only. It is not the datasource that reaches this scale.

---

### Comparing two values

Comparison answers *how alike are these two values*; scoring answers *what does that mean for these two people*. A comparator knows nothing about thresholds or priorities, and every one follows the same shape:

```
raw equality?          → 1.0   (before any normalisation)
mode == STRICT?        → 0.0   (a deterministic rule stops here)
normalise both
normalised equality?   → 1.0
otherwise              → type-specific algorithm
```

`mode` comes from the rule, which is what lets the same comparator be exact for one rule and tolerant for another in the same evaluation.

| Type | Normalisation | Fuzzy algorithm |
|---|---|---|
| `EMAIL` | lowercase, trim | split at `@`; Levenshtein on the local part, Jaro-Winkler on the domain; score is the **lower of the two** |
| `NAME` | lowercase, strip punctuation, **sort tokens** | Jaro-Winkler; if Double Metaphone codes match exactly, floor at 0.9 |
| `PHONE` | digits only | same subscriber → 1.0, otherwise 0 |
| `LOCATION` | lowercase, trim | expand street abbreviations, then Jaccard over tokens |
| `FUZZY_STRING` | lowercase, trim | Jaro-Winkler |
| `DATE` | parse to `YYYY.MM.DD` | none — equal or 0 |
| `UNIQUE_ID` | lowercase, trim | none — equal or 0 |
| `PRIMITIVE_EXACT` | none | none — raw equality or 0 |

Email takes the *minimum* of its two halves rather than the average: a perfect local part against a stranger's domain is not a near-match, so the weaker half governs. `NAME` sorts tokens first, so `John Smith` and `Smith John` are identical.

`PHONE` has no partial score: two numbers either name the same subscriber or they do not. Each number is read every way it can be written — with or without its country calling code, with a national trunk `0`, with `00` for `+` — and the two match when they share a national number and their countries do not contradict each other. So `+94 77 123 4567` and `077 123 4567` score 1.0, while `+94 77 123 4567` and `+94 70 123 4567` score 0 despite sharing their last seven digits. A phone rule never produces a borderline score, so a review task on a phone match comes from another rule objecting or from the number being shared too widely — never from two numbers merely looking alike. The 7-digit suffix is still a blocking key: it is a reasonable way to gather candidates, just not to decide between them.

A multi-valued attribute takes the best pair — every input value against every candidate value, highest wins. One matching email out of three is a match on that rule.

---

### What each rule reports

A rule produces a similarity score *and* a verdict. The verdict matters because a score alone cannot distinguish "these are different people" from "there was nothing to compare":

| Verdict | When | Effect on the decision |
|---|---|---|
| `AGREE` | score ≥ `manual_review_threshold` | Can become the primary signal; counts toward the agreement total |
| `INCONCLUSIVE` | score between the contradiction and agreement bars | Ignored in both directions |
| `DISAGREE` | score ≤ `ScoreContradictionThreshold` (0.3) | Counts as contradicting evidence |
| `UNKNOWN` | either side has no value, or holds a value that identifies nobody | Excluded entirely — never affects the score |

`UNKNOWN` covers placeholders as well as absent values: role mailboxes (`noreply@`, `info@`), reserved domains (`example.com`), sentinel dates (`1970-01-01`), repeated-digit phone numbers and `N/A`-style text. Two profiles agreeing on one of those agree on nothing.

### Evidence strength

Agreement and disagreement on the same attribute rarely carry equal weight, so each rule declares both directions independently:

| | `match_strength` | `mismatch_strength` |
|---|---|---|
| **Email** | HIGH — sharing an address strongly implies one person | LOW — most people have several addresses |
| **Date of birth** | LOW — birthdays collide constantly | HIGH — two different dates is near-proof of difference |
| **National ID** | HIGH | HIGH |
| **Name** | LOW | MEDIUM |

Both default from `attribute_type` when the operator does not set them. A rule with `mismatch_strength: HIGH` is *discriminating*: a disagreement on it vetoes automatic merging whatever else agrees.

### The algorithm

The score is **not an average**. Averaging made every rule's weight depend on how many other rules happened to be configured, so adding a rule weakened every existing match, and a rule that disagreed pulled a strong match down by exactly as much as a rule with no data — which meant two sparse profiles outscored two well-populated ones. Instead one rule speaks for the match and the rest may only object.

| Step | | |
|---|---|---|
| 1 | **Evaluate** | Score every rule in its own mode. `UNKNOWN` rules take no further part. |
| 2 | **Primary signal** | Walk rules in priority order; the first that `AGREE`s is the primary. Its score is the result — lower-priority rules cannot dilute it. |
| 3 | **Decisive short-circuit** | A `UNIQUE_ID` agreeing exactly returns 1.0 immediately. So does *any* deterministic rule agreeing — at any priority — when the org's `deterministic_match_decisive` is on, which is the default. Steps 4–6 then never run. |
| 4 | **Discriminating veto** | Any `mismatch_strength: HIGH` rule that `DISAGREE`s caps the score just below auto-merge. |
| 5 | **Conflicting evidence** | If most other applicable rules `DISAGREE`, cap below auto-merge. |
| 6 | **Lone-agreement gate** | A single agreeing rule may auto-merge only if it is `match_strength: HIGH`, or the top-priority rule whose evidence is not `LOW`, **and** the matched value is not already shared by ≥ `RarityCommonMinProfiles` profiles in the org. A sole configured rule is always top-priority, so priority alone cannot promote `LOW` evidence — otherwise an org whose only rule is a tolerant name match would merge *Ivan Petrov* into *Ivana Petrov* unattended. |

Caps only ever downgrade `AUTO_MERGE` to `MANUAL_REVIEW`. They never suppress a match to `UNIQUE` — the primary signal still stands. If no rule agrees, the highest applicable score is returned, which is below the review threshold by construction.

**Value rarity** (step 6) is read from the `blocking_keys` index: how many profiles in the org already carry the matched value. Rarity may only weaken evidence, never strengthen it — an uncommon value being wrong is just as wrong as a common one, so rarity lowers the chance of coincidence without adding corroboration.

### Decision thresholds

| Score | Decision |
|---|---|
| ≥ `auto_merge_threshold` (default 1.0) | `AUTO_MERGE` |
| ≥ `manual_review_threshold` (default 0.75) | `MANUAL_REVIEW` — a review task is created |
| below that | `UNIQUE` — no action |

`AUTO_MERGE` additionally requires `auto_merge_enabled` on the org's admin config.

A merge records the name of the rule that **decided** it, which is not always the first rule that agreed. With `deterministic_match_decisive` on, an exact match at any priority ends the evaluation, so a fuzzy rule ranked above it agreed without deciding anything; attributing the merge to that fuzzy rule would record a similarity match where an exact identifier was the cause. The reason follows the same order the scorer used: a unique identifier matching exactly as the primary signal, then a deterministic agreement where those are decisive, then the first agreement of any kind.

Both thresholds are validated against each other, not only against 0 and 1: an organisation must keep `0.3 < manual_review_threshold <= auto_merge_threshold - 0.01`. The upper bound keeps the cap applied to a held-back match above the review threshold, so an objection downgrades a merge to a review task rather than suppressing the pair entirely. The lower bound matters because `manual_review_threshold` is also the bar a rule must clear to count as agreeing, so a value near the contradiction bar lets near-unrelated values become the primary signal.

Whether the objections in steps 4–6 apply to a deterministic match at all is also per organisation: `deterministic_match_decisive` (default on) makes an exact match conclusive on its own, as it was before typed matching; turning it off lets the other rules send it to review instead. See [Unification Rules](unification-rules.md#letting-other-rules-object-deterministic_match_decisive).

### Why a pair is in review

A score that a cap held back is not a measure of anything: it is `auto_merge_threshold - 0.01` whatever held it, and it moves when the threshold does. Two numbers are therefore kept apart:

| | What it is | Where it is used |
|---|---|---|
| **Score** | The primary signal after every cap | Routing only: merge, review or no action |
| **Similarity** | The primary signal before any cap | Recorded on the review task and the rejection; ranks fuzzy search |

A review task does not return either number. It returns an **explanation**, recorded when the task is raised, from which a client builds its own sentence:

- `rules`: every active rule, with its ID, name, property, attribute type, method, priority and strengths, the `score` it gave (absent when either profile had no value) and its `verdict` (`AGREE`, `INCONCLUSIVE`, `DISAGREE` or `UNKNOWN`). The rule that spoke for the match is marked `primary`.
- `reasons`: every reason the pair was held, each a `code` with the IDs of the rules it concerns.

| Code | Holds when |
|---|---|
| `BELOW_AUTO_MERGE_THRESHOLD` | The primary rule scored below `auto_merge_threshold`, given as `threshold` as it was at the time |
| `DISCRIMINATING_MISMATCH` | A rule with `mismatch_strength: HIGH` disagrees (step 4) |
| `CONFLICTING_EVIDENCE` | Most of the other applicable rules disagree (step 5) |
| `WEAK_SOLE_EVIDENCE` | Only one rule agrees, and it cannot identify a person alone (step 6) |
| `COMMON_VALUE` | Only one rule agrees, on a value at least `shared_by_at_least` profiles share (step 6) |
| `AUTO_MERGE_DISABLED` | The pair scored for a merge, but the organisation has `auto_merge_enabled` off |
| `REJECTION_RECONSIDERED` | An administrator rejected the pair before; names the rules that agree now and gives `previous_similarity` |
| `AUTO_MERGE_FAILED` | The pair scored for a merge, but the merge could not be made |
| `MERGED_WITH_ANOTHER_PROFILE` | The pair scored for a merge, but the profile merged with another candidate in the same evaluation |

A reason is reported whenever its condition holds, not only when it was the one that lowered the score. *Marcus Delacroix* against *Marcus Delacroix* with different emails has a similarity of 1.0 and two reasons, `CONFLICTING_EVIDENCE` on the email rule and `WEAK_SOLE_EVIDENCE` on the name rule; either alone would have held it.

### Worked examples

Rules: **P1** `nic` (UNIQUE_ID, deterministic) · **P2** `name` (NAME, fuzzy) · **P3** `city` (LOCATION, fuzzy). Thresholds 0.95 / 0.75 — an organisation that has lowered the automatic threshold from its 1.0 default, so tolerant matches can merge unattended.

| # | Scenario | nic | name | city | Outcome | Why |
|---|---|---|---|---|---|---|
| 1 | All agree | 1.00 | 0.97 | 1.00 | **AUTO_MERGE** | Unique ID agrees exactly — short-circuit |
| 2 | NIC differs, name close | 0.00 | 0.97 | 1.00 | **MANUAL_REVIEW** | Name is primary; NIC is discriminating and disagrees → veto |
| 3 | NIC absent on one side | — | 0.97 | 1.00 | **AUTO_MERGE** | Name and city both agree — two independent agreements |
| 4 | NIC absent, only name | — | 0.97 | — | **MANUAL_REVIEW** | Lone agreement on a LOW-strength attribute |
| 5 | Only city shared | — | — | 1.00 | **MANUAL_REVIEW** | Lone agreement, not top priority, weak attribute |
| 6 | NIC agrees, everything else differs | 1.00 | 0.10 | 0.00 | **AUTO_MERGE** | Unique ID is conclusive on its own |
| 7 | Nothing reaches the bar | 0.00 | 0.42 | 0.30 | **UNIQUE** | No primary signal |

Rules: **P1** `email` (EMAIL, deterministic) · **P2** `dob` (DATE, deterministic).

| # | Scenario | email | dob | Outcome | Why |
|---|---|---|---|---|---|
| 8 | Email agrees, DOB absent | 1.00 | — | **AUTO_MERGE** | Email is `match_strength: HIGH` and top priority |
| 9 | Email agrees, DOB differs | 1.00 | 0.00 | **MANUAL_REVIEW** | DOB is `mismatch_strength: HIGH` → veto |
| 10 | Email agrees but 300 profiles share it | 1.00 | — | **MANUAL_REVIEW** | Value too common to identify anyone |
| 11 | Both hold `noreply@example.com` | — | — | **UNIQUE** | Placeholder reads as `UNKNOWN`, not as agreement |

### Why the previous model was replaced

With P1 `nic` deterministic and P2 `name` fuzzy, weights 2 and 1, the old weighted average divided by the total applicable weight:

```
NIC differs, name matches 0.95:   (0.0 × 2 + 0.95 × 1) / 3  =  0.32  →  UNIQUE
NIC absent,  name matches 0.95:   (0.95 × 1) / 1            =  0.95  →  AUTO_MERGE
```

The same name evidence produced no action when the NICs contradicted, and an automatic merge when the NIC was simply missing — profiles merged more readily the less was known about them. Both rows are now `MANUAL_REVIEW`.

---

## Switching tolerant matching on and off

`identity_resolution.fuzzy_matching_enabled` can be turned on or off on a running deployment. The schema is identical either way, so neither direction needs a migration — but the two are not symmetric.

**Off** is immediate. A rule stored as `fuzzy` is read as exact everywhere: the worker, the synchronous search, the index backfill, and the options endpoint stops offering the method. The stored value is untouched, so switching back on restores what the operator configured rather than requiring the rules to be written again. Two residues, neither harmful: phonetic and LSH keys already written stay until each profile is next re-indexed, where they gather candidates that then score exactly — comparisons, not wrong answers — and review tasks raised while it was on stay in the queue to be answered.

**On needs a reindex.** Nothing tolerant was written while it was off, so blocking cannot reach any profile created or updated in that period, and tolerant matching quietly under-matches. Deactivating and reactivating each fuzzy rule rebuilds that attribute's keys across the organisation. Without that step, enabling the feature looks like it did nothing.

---

## Searching profiles by similarity

Unification runs on a queue after a write. The same engine is also reachable synchronously, as a search: `GET /profiles?fuzzyFilter=traits.name eq Jonathan Smith` returns profiles *similar* to the values given rather than equal to them, each with its similarity and the per-rule breakdown behind it. Similarity is not lowered by a rule that disagrees: that decides whether profiles merge unattended, not how alike they are.

Two modes, chosen by what the request carries:

| Request | Behaviour |
|---|---|
| `fuzzyFilter` alone | Candidates come from the blocking index, every one is scored, and those at or above `threshold` are returned highest first |
| `fuzzyFilter` **and** `filter` | Hybrid: a profile is returned only where it is similar on the fuzzy values **and** exactly satisfies every deterministic condition |

It is the same blocking index and the same comparators the worker uses, driven by the organisation's own unification rules — so an attribute is searchable this way only where a rule names it, and the rule's attribute type decides how two values are compared. A rule matched exactly contributes nothing a plain `filter` would not, which is why tolerant search needs at least one rule set to `fuzzy`.

The decision thresholds do not apply here. A search reports scores and lets the caller decide; it never merges anything, and never raises a review task. The `threshold` parameter is a floor on what is returned, unrelated to the organisation's auto-merge and review settings.

Both paths honour `identity_resolution.fuzzy_matching_enabled`. With it off a rule stored as fuzzy is read as exact everywhere, search included, so the endpoint keeps working and returns only exact matches. That is worth stating because the two halves fail differently: with the switch off no new tolerant blocking keys are written, but keys written earlier remain until each profile is next re-indexed — so blocking may still reach a candidate that scoring then declines to call a match.

---

## Merge cases

Once a match is found, the worker determines the master/child relationship based on whether each profile is *permanent* (has a `userId`) or *temporary* (anonymous, no `userId`).

### Permanent + temporary

The permanent profile always becomes the master. The temporary profile is added as a child reference.

```
existing: permanent  +  new: temporary
  → existing stays as master
  → new becomes child of existing
```

```
existing: temporary  +  new: permanent
  → new becomes master
  → existing becomes child of new
  → existing's children (if any) are re-parented to new
```

### Both temporary

The existing profile becomes master, the new profile becomes its child.

### Both permanent (same userId)

Treated the same as both-temporary — existing becomes master, new becomes child.

### Both permanent (different userIds)

**Not merged.** CDS logs a warning and returns without action.

---

## Data merging

After the master/child relationship is resolved, `MergeProfiles` is called. It walks each schema attribute for the org and applies the attribute's `merge_strategy`:

| Strategy | Behaviour |
|---|---|
| `overwrite` | Incoming value replaces existing |
| `combine` | Both values combined into an array |

The merged result is written back to the master profile across three stores: `identity_attributes`, `traits`, and `application_data`.

---

## Result on the profile

After a merge the API response reflects:

- **Master profile** — `merged_from` contains a `Reference` entry for each child profile with the rule name (or `system:user_id_match`) as the `reason`
- **Child profile** — `merged_to` points back to the master profile's `profile_id`

```json
// Master
{
  "profile_id": "master-123",
  "user_id": "alice@example.com",
  "merged_from": [
    { "profile_id": "anon-456", "reason": "system:user_id_match" },
    { "profile_id": "old-789",  "reason": "email_match" }
  ]
}

// Child
{
  "profile_id": "anon-456",
  "merged_to": { "profile_id": "master-123", "reason": "system:user_id_match" }
}
```
