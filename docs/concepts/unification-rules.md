# Unification Rules

**Unification rules** tell CDS when two separate profiles should be recognised as the same person and merged. Each rule specifies a profile attribute to match on — if two profiles share the same value for that attribute, they are candidates for merging.

---

## Upgrading from exact-only matching

Before typed matching, unification walked the active rules in priority order and merged two profiles on the **first exact match**, whatever the other rules held. Organisations that have only ever used that behaviour keep it after upgrading, with no configuration change:

- A rule stored without `attribute_type`, `unification_method` or strengths is read as `PRIMITIVE_EXACT` + `deterministic`, matched on exact equality exactly as before.
- `PRIMITIVE_EXACT` carries `match_strength: HIGH`, so a single rule matching exactly still merges on its own, at any priority, however many other rules are configured.
- The strength columns are nullable with **no column default**. A default would stamp a concrete value onto every pre-existing row when the column is added, which is indistinguishable from an operator having chosen it and would override what the attribute type implies. An unset strength is derived on every read instead, so changing a rule's `attribute_type` later re-derives it rather than leaving the old type's value behind.
- Automatic merging is on unless an organisation has explicitly turned it off. The `auto_merge_enabled` setting is stored as a row in the admin config, so an organisation configured before the key existed simply has no row for it; that absence reads as **enabled**, because reading it as disabled would silently route every merge the tenant relied on into the review queue instead.
- A deterministic match stays **decisive**. Before, the first rule that matched merged the pair outright, whatever the other rules held; that is still what happens unless the organisation chooses otherwise, through the `deterministic_match_decisive` admin setting described next. Like `auto_merge_enabled`, an organisation with no row for it reads as **on**.

### Letting other rules object: `deterministic_match_decisive`

Every rule is now evaluated for every candidate pair, so the engine knows when the *other* rules disagree with a match. What it does with that knowledge is an organisation's choice.

**`deterministic_match_decisive: true` — the default.** A pair agreeing on any deterministic rule merges immediately. The other rules are not consulted, exactly as before typed matching. Fuzzy rules are unaffected either way: a fuzzy agreement always has to survive the objections below.

**`deterministic_match_decisive: false`.** A deterministic match becomes the primary signal but the other applicable rules may object. When most of them actively disagree, the merge is not performed automatically — a review task is raised and a person decides.

With two deterministic rules on `email` (priority 1) and `phone` (priority 2):

| Both profiles hold | decisive (default) | open to objection |
|---|---|---|
| Same email, same phone | merged | merged |
| Same email, **different phone** | merged | **review task** |
| Same email, no phone on one side | merged | merged |
| Different email, **same phone** | merged | **review task** |
| No email on one side, same phone | merged | merged |
| Different email, different phone | no action | no action |

A *missing* value is not a disagreement, so it never blocks in either mode — only two present values that differ do. Two profiles sharing an email but holding different phone numbers is the shared household address case, which is why the open mode asks.

Note that with exactly two rules a single disagreement is already a majority, so a two-rule organisation in the open mode is the most conservative configuration there is. Adding a third rule can make merges *more* likely, not less.

Objections only ever downgrade an automatic merge to a review task. Nothing merges in the open mode that would not have merged in the decisive one, and nothing that merged becomes "no action" — the decision moves from automatic to human, never away.

**A disagreement on a `DATE` or `UNIQUE_ID` attribute vetoes an automatic merge** in the open mode, whatever else agrees. This cannot affect an organisation that has never typed its attributes, because both types have to be declared. It begins to apply as an operator gives attributes their real types, which is the point at which the extra caution is wanted.

---

## Rule structure

| Field | Description |
|---|---|
| `rule_id` | System-generated UUID |
| `org_handle` | The organisation this rule belongs to |
| `rule_name` | Human-readable name (also used as the `reason` recorded on a merge) |
| `property_name` | The attribute name to match on (e.g. `identity_attributes.email`) |
| `property_id` | The `attribute_id` of the schema attribute being matched |
| `priority` | Lower number = evaluated first. Rules are sorted ascending by priority. |
| `is_active` | Only active rules are evaluated during unification |
| `attribute_type` | What kind of value the attribute holds, which decides how it is compared and indexed |
| `unification_method` | `deterministic` (exact) or `fuzzy` (tolerates variation). Rules of both kinds are evaluated side by side. |
| `match_strength` | How much an agreement on this attribute supports a merge. Derived from `attribute_type` unless overridden |
| `mismatch_strength` | How much a disagreement opposes one. Derived from `attribute_type` unless overridden |
| `created_at` / `updated_at` | Timestamps |

---

## How the evidence strengths are set

The engine derives both from `attribute_type`. There is nothing to choose: the table below is the whole of it, and an operator configures a rule's strengths by giving the attribute its real type.

The two are kept separate because agreement and disagreement on the same attribute rarely carry equal weight:

- Two profiles sharing an **email address** are almost certainly the same person, but two *different* addresses say very little — most people have several.
- Two profiles sharing a **date of birth** says little, since birthdays collide constantly, yet two *different* dates is close to proof they are different people.

| Value | On a match | On a mismatch |
|---|---|---|
| `HIGH` | Can merge two profiles on its own | Two differing values block an automatic merge outright |
| `MEDIUM` | Real evidence, but another attribute must also agree | Counts against the match without blocking it |
| `LOW` | Close to coincidence on its own | Says little; people legitimately have several |

Overriding a derived strength per rule is gated on `identity_resolution.allow_evidence_strength_override` in `deployment.yaml`, which is **off by default and is not a setting operators are asked to reach for**. The strengths decide whether an attribute can merge two profiles on its own and whether a difference in it blocks a merge outright, so getting one wrong silently changes who gets merged; the derived values are the supported contract and the Console offers no way to change them. The setting exists so the capability can be opened deliberately once there is a way to preview the effect on existing profiles, not as a tuning knob to be turned today.

While it is off, sending either field is rejected rather than quietly ignored — a caller that believes it set a strength and is overruled would misread every merge decision that followed.

> **Disclaimer — this setting's scope is provisional.** It currently sits at deployment
> level because it gates an API surface rather than matching behaviour: whether a field is
> writable is a property of the build being run. Every other setting that shapes who gets
> merged — `auto_merge_enabled`, `deterministic_match_decisive` and both thresholds — is per
> organisation in the admin config, so this may move there once there is a way for an operator to preview what a
> strength change would do to their existing profiles. Treat its location as unsettled and
> avoid building tooling that assumes it is server-wide.

`PRIMITIVE_EXACT` is what a rule written before typed matching resolves to, and its `match_strength` is `HIGH` on purpose: previously any rule matching exactly merged the two profiles outright, and an upgrade must not quietly change that. An operator who wants a weaker reading gives the attribute its real type.

| Attribute type | `match_strength` | `mismatch_strength` |
|---|---|---|
| `UNIQUE_ID` | HIGH | HIGH |
| `EMAIL` | HIGH | LOW |
| `PHONE` | HIGH | LOW |
| `DATE` | LOW | HIGH |
| `NAME` | LOW | MEDIUM |
| `LOCATION` | LOW | LOW |
| `FUZZY_STRING` | MEDIUM | LOW |
| `PRIMITIVE_EXACT` | HIGH | MEDIUM |

Neither strength is part of the API on a server with the override off, in either direction: sending one is rejected, and none is returned. A field no caller can set is not worth describing, and the value there is a pure function of `attribute_type` — which the response already carries — so returning it would make this table part of the API contract and turn a retune of the scoring weights into a breaking change.

Where the override is on, a response carries a strength only where an operator really set one. An absent field means the rule inherits from its `attribute_type`, which is what the row holds: reporting the derived value instead would leave an editor unable to tell a deliberate choice from an inherited one, and would freeze a rule copied through `GET`-then-`POST` against any later change to the table.

---

## How rules are evaluated

A profile write is enqueued for unification when any of the following is true. An update that changed nothing a rule matches on is skipped, because it would reach the conclusion the previous write already reached:

- a value some active rule matches on changed
- the profile's `userId` changed
- an active rule is newer than the profile's last write (see below)

The worker then:

1. Merges immediately if another master profile shares the same `userId` — a system invariant that needs no rule
2. Regenerates the profile's blocking keys from the active rules, replacing its previous set
3. Asks the index which other profiles share a key, capped per key group
4. Scores each of those candidates against the **whole** rule set
5. Routes the result by the org's thresholds: merge, raise a review task, or do nothing

Every rule is evaluated against every candidate — a rule does not "fire" on its own. One rule then speaks for the result and the others may only object; see [how-unification-works.md](how-unification-works.md) for that algorithm.

---

## When a rule starts applying

A rule does not only affect profiles written after it. There are three paths by which an existing profile comes under a new rule, and they exist because no single one is sufficient:

| Path | Covers | Limitation |
|---|---|---|
| The profile's next write | Any profile that is written again | Not immediate; never happens for a dormant profile |
| A write while the rule is newer than the profile | An unrelated edit still brings the profile in | Still needs the profile to be written |
| The backfill on activation | Every existing profile, immediately | Runs in the background; a restart mid-scan leaves it partial |

The backfill is the only one that is both immediate and complete, which is why activating a rule triggers it. The other two are what make the index self-healing for anything it missed.

---

## Rejections

Rejecting a review task records that an administrator decided two profiles are **different people**, along with the similarity and per-rule breakdown they decided against.

That evidence is what makes the decision durable. A rejection is a statement about identity, not about the data at the time — two different people do not become the same person because one of them changed a phone number — so an attribute changing is not by itself a reason to ask again. Clearing rejections on data change turns the review queue into a treadmill: the same pair returns whenever anything unrelated moves, and the administrator dismisses it repeatedly.

A rejected pair is put back in front of an administrator only when the new evaluation is genuinely stronger than the one they saw:

| Reopens the pair | Leaves the rejection standing |
|---|---|
| The similarity exceeds the rejected similarity by `RejectionReconsiderMargin` (0.05) | The similarity is the same, lower, or drifts up slightly |
| A rule agrees now that did not agree then — including an attribute that was not comparable before | A new attribute appears but does not agree |

The second row of the left column matters because of how scoring works: the waterfall takes its score from the first rule that agrees, so a *lower-priority* rule newly agreeing adds real corroboration without moving the number at all. It is also the only way back for a pair rejected at a similarity of 1.0, which cannot rise further.

Rejections follow their profiles. A merge repoints a rejection from the merged-away profile onto the surviving master, and deleting a profile removes its rejections.

---

## System merge reason

In addition to user-defined rules there is one built-in merge trigger:

| Reason | Trigger |
|---|---|
| `system:user_id_match` | Two profiles share the same `userId` — merged automatically without any rule |

Every other merge records the **name of the rule that drove it**, whether it merged automatically or an administrator approved it from the review queue. The rule is the highest-priority one whose score reached the agreement bar — the one the scorer held accountable — so a profile alone answers "why were these combined?" without joining to the review task.

This reason appears in `merged_from[].reason` on the master profile after the merge.

---

## Priority guidance

Priority is a **gate**, not a weight. Rules are walked in priority order and the first one that agrees sets the match score outright; lower-priority rules corroborate it but cannot raise it. Ordering therefore decides which rule is held accountable for a merge, which is what an operator sees when asking why two profiles were combined.

- Assign lower priority numbers to high-confidence identifiers (e.g. `identity_attributes.email` at priority 1)
- Assign higher priority numbers to weaker signals (e.g. `traits.city` at priority 10)
- Leave gaps between priorities (e.g. 10, 20, 30) so new rules can be inserted without reordering
- Priorities must be unique within an organisation; a duplicate is rejected

A lone agreement on a rule that is neither the top-priority one nor marked strong evidence cannot auto-merge on its own — it is capped to a review task.

---

## Enabling and disabling rules

Setting `is_active: false` excludes a rule from evaluation without deleting it, and removes that attribute's blocking keys for the organisation — the index shrinks rather than carrying entries nothing reads. Re-activating it starts the backfill again.

Changing `attribute_type` **or** `unification_method` on an active rule does both: the old keys are removed and rebuilt, because each of those changes the shape of the keys the rule writes. The method matters as much as the type — a rule switched from `deterministic` to `fuzzy` has only its exact key in the index, so without a rebuild it keeps behaving exactly like a deterministic rule for every existing profile while appearing to be fuzzy.

Changing a rule's name or priority needs no rebuild; neither affects the keys.

Existing merges already recorded are not reversed when a rule is deactivated or deleted.
