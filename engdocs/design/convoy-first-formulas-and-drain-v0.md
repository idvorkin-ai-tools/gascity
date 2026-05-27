---
title: Convoy-First Formulas And Drain V0
status: Proposed
source: https://github.com/gastownhall/gascity/issues/1709
---

# Convoy-First Formulas And Drain V0

## Summary

Issue #1709 describes the core orchestration problem: when a convoy is slung to
a formula today, Gas City expands the convoy before formula execution and starts
one independent formula run per member. That preserves implementation
parallelism, but it makes later verification, review, and synthesis phases see
only fragments of the work instead of the convoy as a whole.

This design changes targeted `contract = "graph.v2"` formulas to be
convoy-first. A targeted graph.v2 formula receives one reserved input,
`convoy_id`. If the operator targets a single bead, the runtime creates a
visible synthetic singleton convoy that tracks that bead and passes the
singleton convoy ID to the formula. If the operator targets a convoy, the
runtime passes that convoy ID whole; graph.v2 never expands an input convoy into
one formula run per member before formula execution.

`drain` is the v0 scatter primitive inside graph.v2. A drain step shreds the
formula input convoy into generated drain-unit convoys, then starts one ordinary
graph.v2 item formula run per generated convoy. Item formulas do not receive
`item_bead_id`, `bead_id`, or special drain variables. They receive
`convoy_id`, exactly like every other graph.v2 formula.

This is not the full O3 Run model. It is a narrow graph.v2 contract change plus
a controller-owned drain control bead that materializes visible convoys,
workflow roots, typed events, and deterministic lineage.

## Status

Proposed. This document records the agreed v0 direction before implementation.

## Goals

- Make targeted graph.v2 formulas operate over convoys instead of individual
  beads.
- Preserve the ergonomic single-bead entry point by normalizing bare beads into
  visible singleton convoys.
- Add a graph.v2 `drain` step that scatters work across generated drain-unit
  convoys while preserving convoy-level formula semantics.
- Keep item formulas ordinary graph.v2 formulas that receive only `convoy_id`.
- Support both independent parallel drain work and sticky shared-context drain
  work.
- Make drain expansion crash-safe, replay-safe, and observable.
- Keep generated drain units visible and inspectable as normal convoy beads
  while making their synthetic lifecycle explicit.
- Fail fast on ambiguous legacy graph.v2 formula variables such as `issue` and
  `bead_id`.

## Non-Goals

- Do not design the full O3 Run primitive.
- Do not design HITL, gather policy, typed human dispositions, or dashboard Run
  visualization.
- Do not add scripted shredders in v0. The syntax reserves the future slot, but
  v0 ships only built-in one-by-one shredding.
- Do not make drain imply completion of the input convoy or the original member
  beads.
- Do not add a session-level continuation cursor. `gc hook` remains stateless
  and derives continuation from bead state.
- Do not preserve graph.v2 compatibility for `{{issue}}` or `{{bead_id}}`.

## Current Behavior

Formula-backed sling paths currently treat the source ID as a bead-scoped
input. `BuildSlingFormulaVars` injects `issue=<bead-id>`, graph routing stamps
the root with source metadata, and workflow finalization can close the source
bead chain on success. Batch/convoy sling expands a convoy before formula
execution, then instantiates the formula once per open child.

That means a convoy of N members becomes N formula runs. This is correct for
some implementation work and wrong for convoy-level review, synthesis, and
coverage checks.

## Core Contract

### Graph.v2 Input

`contract = "graph.v2"` is the v0 switch for convoy-first formula semantics.
No new `compiler = 2` field is introduced.

A targeted graph.v2 invocation receives `convoy_id` as a reserved system
variable:

- If the target bead is `type = "convoy"`, use that ID as `convoy_id`.
- If the target bead is any other bead type, create or reuse a visible
  synthetic singleton convoy that tracks the bead and use that convoy ID as
  `convoy_id`.
- The input convoy is not auto-closed when the graph.v2 formula succeeds.
- The original member beads are not auto-closed when graph.v2 work succeeds.

Targetless standalone graph.v2 formulas remain valid, but they do not receive
`convoy_id`. Instantiating a targetless graph.v2 formula that references
`{{convoy_id}}` or uses `[steps.drain]` fails before any work is created.

### Canonical Invocation Seam

Every targeted graph.v2 entry point must call one pre-instantiation helper below
the CLI boundary:

```go
PrepareGraphV2Invocation(store, formula, targetIDOrNil, userVars) (GraphV2Invocation, error)
```

The exact Go name may change, but the seam must own all of these operations:

- resolve the final merged formula and confirm `contract = "graph.v2"`
- validate graph.v2 reserved variables after formula extension and
  `description_file` loading
- normalize the target to a convoy
- create or reuse the singleton convoy for bare-bead targets
- inject the reserved `convoy_id` system variable
- reject targetless use of `convoy_id` or drain through the same validation path
- reject any user, rig, order, formula, or inherited var collision with reserved
  graph.v2 names
- return the input convoy ID and variable map to the molecule/formula
  materialization path

The helper is not a sling helper. It is the graph.v2 invocation contract. New
targeted graph.v2 entry points must call it before materializing work.

Entry-point matrix:

| Entry point | V0 behavior |
|---|---|
| `gc sling <target> --formula <graph.v2>` | In contract. Parse the formula before batch expansion, normalize target to convoy, inject `convoy_id`. |
| `gc formula cook --attach <target> <graph.v2>` | In contract. Calls the same helper. |
| Formula-backed orders with a source target | In contract. Calls the same helper during dispatch. |
| Formula-backed orders without a source target | Targetless. Calls the same helper with `targetIDOrNil = nil`; valid only if the graph.v2 formula does not reference `convoy_id` and does not use drain. |
| Store-backed `MolCookOn` / attach APIs | In contract. Must call the same helper. |
| Targetless `MolCook` | Calls the same helper with `targetIDOrNil = nil`. No `convoy_id`; drain is invalid. |
| Non-graph formulas | Unchanged. They remain bead-scoped and may continue using `issue`. |

For graph.v2, container expansion never runs before formula instantiation. The
formula must receive the input convoy whole. The pre-flight contract check lives
in `internal/formula` as a lightweight `PeekContract`/resolve API used by sling
before `doSlingBatch` chooses a path. Sling ordering is: validate target and
agent inputs, resolve the formula contract, branch to graph.v2 normalization or
legacy batch expansion, then materialize work.

Graph.v2 singleton normalization supersedes legacy auto-convoy behavior.
`--no-convoy` and `--owned` are invalid with a targeted graph.v2 formula because
the graph.v2 contract owns convoy normalization and synthetic convoy lifecycle.
The rejection happens after formula contract pre-flight, so the same flags remain
valid for non-graph formulas. Targeting an epic or any other non-convoy bead with
graph.v2 creates a singleton convoy for that bead.

### Source Lifecycle Replacement

Graph.v2 workflow roots use `gc.input_convoy_id` rather than
`gc.source_bead_id` for source tracking. They must not set `gc.source_bead_id`.
Finalization suppresses `closeSourceBeadChain` by checking
`gc.formula_contract = "graph.v2"` and the absence of `gc.source_bead_id`.

Graph.v2 live-root identity uses a convoy-scoped key:

```text
gc.graphv2_root_key = graphv2-root:<input_convoy_id>:<formula_ref>:<vars_fingerprint>:<dispatch_scope>
```

`formula_ref` is the resolved formula name plus content hash. `vars_fingerprint`
is the stable hash of the fully merged non-reserved var map. `dispatch_scope` is
the stable caller scope when one exists, such as an order ID, molecule ID, or
explicit idempotency key. For ad hoc CLI sling with no durable caller scope, the
key is used only as a live-root launch lock: it prevents duplicate active roots
for the same convoy/formula/vars, but terminal roots do not block an intentional
later rerun.

Dispatch scopes are fixed by entry point:

| Entry point | `dispatch_scope` |
|---|---|
| Ad hoc `gc sling` | `adhoc:<session-or-user>:<command-start-seq>` for telemetry; live-root dedupe ignores terminal roots. |
| Formula-backed order | `order:<order_id>` |
| `gc formula cook --attach` | `cook:<caller-supplied-idempotency-key>` when supplied, otherwise ad hoc live-root semantics. |
| Store-backed `MolCookOn` | caller-supplied idempotency key; required for retryable controller paths. |
| Drain item root | `drain:<drain_control_id>:<member_id>` |
| Targetless graph.v2 | `targetless:<formula_ref>:<vars_fingerprint>:<caller_scope>` and no `gc.input_convoy_id`. |

Targetless graph.v2 roots use `gc.graphv2_root_key` but omit
`gc.input_convoy_id`. They may not use drain or reference `convoy_id`.

The launch lock, duplicate-live-root query, cleanup projection, and
orders/feed/API workflow projection must all use `gc.input_convoy_id` and
`gc.graphv2_root_key` for graph.v2 roots. Legacy non-graph formulas keep using
the existing source-bead chain behavior.

A structural test must prove graph.v2 finalization cannot reach
`closeSourceBeadChain`.

Graph.v2 operator recovery is convoy-keyed. Conflict lookup, `--force`
replacement, delete/reopen, and user-facing hints use `gc.input_convoy_id` and
`gc.graphv2_root_key`. Existing source-bead-keyed recovery commands must either
accept `--input-convoy` for graph.v2 roots or fail with a message that names the
input convoy and graphv2 root key to use.

### Reserved Variables

Graph.v2 has three schema-reserved names:

- `convoy_id`
- `issue`
- `bead_id`

Rules:

- `convoy_id` may be referenced by targeted graph.v2 templates.
- `convoy_id` may not be declared in formula `[vars]`, inherited formula vars,
  rig `formula_vars`, order vars, or CLI `--var`.
- `issue` and `bead_id` may not be referenced, declared, inherited, or supplied
  anywhere in a graph.v2 invocation.
- Reserved-name collisions are hard errors that name the formula, field, source
  layer, and key. The runtime rejects collisions; it never silently overrides or
  silently skips a reserved value.

Validation scans the fully resolved formula after `extends` merge and after all
`description_file` contents are loaded. Missing description files are validation
errors, not skipped text. The scan uses the template parser or an equivalent
AST-aware path so `{{ issue }}`, `{{.issue}}`, and `{{ index . "issue" }}` do
not bypass the ban.

The scan covers every templated formula field that can affect runtime behavior:
formula vars, descriptions and loaded description files, step titles,
step descriptions, prompts, conditions, checks, assignee/target fields,
metadata values, drain fields, output templates, and order/formula dispatch
fields.

Validation runs at formula load/resolve time and again inside
`PrepareGraphV2Invocation` on the fully merged var map: formula defaults,
inherited vars, rig `formula_vars`, order vars, and CLI `--var`. Load-time
validation catches forbidden declarations and references. Materialization-time
validation catches targetless use of `convoy_id`, drain, and dispatch-layer
reserved-name collisions.

The formula parser must preserve provenance for every scanned value after
`extends` merge: formula path or embedded pack source, field path, var source
layer, and original `description_file` path. `description_file` read failures
are hard errors that preserve the path. CI validates the embedded/materialized
pack payload, not only files under `internal/bootstrap/packs/`.

## Singleton Convoys

Singleton convoys are visible synthetic convoy beads created for targeted
graph.v2 invocations on a non-convoy bead.

Singleton creation is idempotent and shared per source bead. Two different
graph.v2 formulas targeting the same non-convoy bead reuse the same singleton
because the singleton is only a visible convoy wrapper for that bead, not a
formula-specific workflow artifact. The deterministic key is:

```text
graphv2-singleton:<source_bead_id>
```

The implementation must create-or-get by this key through the store unique-key
primitive. Retrying the same graph.v2 invocation after a crash must reuse the
same singleton convoy instead of minting another one. Concurrent invocations of
two different graph.v2 formulas on the same source bead also resolve to the same
singleton convoy and create separate graph.v2 workflow roots keyed by
`gc.graphv2_root_key`.

Singleton convoy metadata:

```text
gc.synthetic = true
gc.synthetic_kind = singleton-convoy
gc.input_bead_id = <source bead>
gc.graphv2_invocation_key = <deterministic key>
```

Singleton convoys:

- have `type = "convoy"`
- track exactly one source bead using the canonical `tracks` relation
- do not inherit labels, assignee, `gc.routed_to`, pool routing, workflow IDs,
  molecule IDs, or arbitrary metadata from the source bead
- are excluded from worker hook/claim paths
- are exempt from generic convoy auto-close
- remain visible in convoy/list/status APIs with typed synthetic fields

### Store Primitive Requirements

This design requires explicit bead-store primitives. They are part of the v0
implementation contract, not best-effort behavior:

- `CreateOrGetByKey(kind, key, spec)`: atomically create a bead with a unique
  idempotency key or return the existing bead for that key
- `MaterializeFormulaRootByKey(root_key, invocation)`: atomically materialize a
  formula root by idempotency key or return the existing live root for that key.
  This is implemented by extending `Store.MolCook`/wisp materialization to
  accept an idempotency key; controller code must not wrap an unconditional
  `MolCook` in query-then-create logic.
- `CompareAndSetMetadata(bead_id, expected, updates)`: atomically update
  metadata or fail with a typed conflict
- `ReserveDrainMember(member_id, drain_control_id, expected_clear_fields)`:
  atomically reserve an original member for `member_access = "exclusive"`
- `ReleaseDrainMember(member_id, drain_control_id)`: release a reservation owned
  by that drain control
- `NextClosedSeq()`: produce a monotonic store-local close sequence written on
  terminal close transitions
- `ListByMetadata(filters, order)`: query generated units, roots, and
  synthetic convoys by deterministic metadata keys

V0 must implement these for `BdStore` and the in-memory test store used by unit
tests. File/exec/experimental stores are out of v0 scope unless they implement
the same primitives. An out-of-scope backend rejects every targeted graph.v2
materialization path that needs these primitives, including singleton
normalization, graphv2 root launch, drain, and shared-drain materialization,
with typed reason code `unsupported_store_primitive`. It must not silently
degrade to query-then-create idempotency.

`gc.closed_seq` is a Layer-1 bead-store addition. It should land with its own
primitive-layer ADR or an equivalent architecture note before shared drain
ships. Shared drain is gated on that store change; separate drain can ship
without continuation lookup if the store supports the other required drain
primitives.

### Tracks Relation

Singleton and drain-unit convoys use the existing canonical `tracks` relation as
lineage, not as an executable dependency edge. `tracks` must not make the source
bead blocked, ready, stale, or orphaned. `bd ready`, `bd blocked`, `bd stale`,
and orphan detection ignore `tracks` edges from `gc.synthetic = true` convoys
unless a command explicitly asks for lineage.

## Drain Syntax

Drain is a graph.v2-only step block.

```toml
[[steps]]
id = "review-members"
title = "Review convoy members"

[steps.drain]
context = "shared" # or "separate"
formula = "review-one-convoy"
# continuation_group = "review" # shared only, optional suffix
# member_access = "read"        # read or exclusive; default read
# max_units = 50                # optional; must be <= 100 in v0
# on_item_failure = "skip_remaining"

[steps.drain.item]
single_lane = true # required for shared drains in v0
```

Rules:

- `context` is required.
- Valid contexts are `shared` and `separate`.
- `formula` is required and names a graph.v2 formula.
- `member_access` is optional and defaults to `read`.
- Valid member access values are `read` and `exclusive`.
- `max_units` is optional. If present, it must be between 1 and 100.
- `on_item_failure` is optional. Valid values are `skip_remaining` and
  `continue`. The shared default is `skip_remaining`; the separate default is
  `continue`.
- `continuation_group` is valid only when `context = "shared"`.
- `continuation_group` is a user suffix, not the full storage key.
- If `continuation_group` is omitted for shared drain, the suffix is empty.
- `continuation_group` template substitution runs in the parent formula scope;
  `{{convoy_id}}` means the parent input convoy, not a generated drain-unit
  convoy.
- `[steps.drain.item].single_lane = true` is required for shared drains in v0.
  The controller mechanically verifies that the materialized item formula has
  one executable lane before stamping required-affinity continuation metadata.
  Separate drains do not require this annotation.

Drain is mutually exclusive with normal executable step fields. A step with
`[steps.drain]` may use `id`, `title`, `description`, `needs`, and metadata used
for tracing. It may not also define sibling `prompt`, sibling `formula`,
`assignee`, `children`, `loop`, `check`, or executable routing fields at the
same step level.

The drain step materializes as a controller-owned control bead with
`gc.kind = "drain"`. User agents do not execute that control bead.

## Drain Expansion

The source bead, input convoy, drain control bead, generated drain-unit convoys,
and item workflow roots must all live in the same bead store. Cross-store drain
is invalid in v0 and fails before expansion.

When the control dispatcher processes a drain control bead:

1. Claim the control bead with the existing controller claim/CAS mechanism.
2. Read the parent graph.v2 input `convoy_id`.
3. Resolve active members using the canonical convoy membership helper.
4. Reject dangling or missing tracked members.
5. Apply the built-in v0 shredder, `one_by_one`.
6. Persist a typed expansion ledger on the control bead before creating units.
7. Create or reuse visible drain-unit convoys by deterministic key.
8. Instantiate or reuse one item workflow root per drain-unit convoy.
9. Wire dependencies according to `context`.
10. Advance the drain control bead to terminal success or failure from the item
    workflow outcomes.

Expansion is idempotent across controller retries, controller crashes, and
partial materialization. The control bead owns a durable typed ledger. The
ledger is written once before any generated unit is created and then advanced
monotonically as artifacts are created and item roots complete.

Ledger shape:

```text
gc.drain_state = pending | expanding | expanded | completing | succeeded | failed
gc.drain_manifest_version = v1
gc.drain_context = shared | separate
gc.drain_member_access = read | exclusive
gc.drain_parent_convoy_id = <input convoy>
gc.drain_manifest.v1 = <typed DrainManifestV1 serialization>
```

`DrainManifestV1` is a typed internal object, not an untyped API map. It contains
an ordered list of:

```text
index
member_id
unit_key
unit_convoy_id
item_root_key
item_root_id
reservation_owner = <drain_control_id, exclusive only>
status = pending | reserved | unit-created | root-created | wired | skipped | succeeded | failed
outcome_bead_id
outcome_kind = root_default | artifact
failure_reason
```

The public API and SSE projections must parse this into typed response objects.
They must not expose it as `map[string]any` or `json.RawMessage`.

Generated identity is deterministic:

```text
unit_key = drain-unit:<drain_control_id>:<member_id>
item_root_key = drain-item-root:<drain_control_id>:<member_id>
```

The store operation must enforce uniqueness by key. A query-then-create without
a compare-and-set is not sufficient. Retrying after a crash reuses already
created unit convoys and item roots, fills in missing IDs, and resumes wiring.
Item workflow roots must be stamped with `gc.item_root_key` before or as part of
creation so replay can find an orphaned root if the controller crashes before
the manifest row is advanced.

Every manifest row transition uses `CompareAndSetMetadata` against the prior row
status and manifest version. The controller may update the whole serialized
manifest, but it must treat the row's prior status as the compare value so two
replaying controllers cannot clobber each other.

If the controller dies after unit creation, root creation, or dependency wiring,
restart must produce the same generated set, the same shared order, and no
duplicates.

### Membership Snapshot

V0 assumes convoy membership is fixed for the duration of one formula run. The
drain control still records the concrete expansion set in `DrainManifestV1` so
retries, downstream synthesis, and debugging use the same ordered set.

Active members include open and other non-terminal statuses. Closed and
tombstone members are excluded. The active member list is sorted by canonical
convoy membership order before indices are assigned. Indices are 0-based.

The manifest is canonical for membership, order, generated identity, and
reservation ownership. It is not canonical for terminal item outcomes. On every
replay, the controller reconciles row outcome from the live item-root bead state
and then advances the manifest. This prevents a stale manifest row from hiding a
root that reached terminal state while the controller was down.

`gc.drain_count` is a denormalized display field on generated units and roots.
The manifest length is canonical. A single helper, `StampDrainLineage`, writes
`gc.drain_count` together with the corresponding manifest update so it cannot
drift through ad hoc metadata writes.

### Member Access

Drain never implicitly mutates original member beads. The formula author
declares whether item work only reads members or needs exclusive access.

`member_access = "read"` is the default. The controller does not run a heuristic
busy-member check for read drains. Item formulas may inspect the underlying
one-member convoy and its tracked bead, but they must not close or mutate the
tracked bead unless they explicitly claim it in their own work.

`member_access = "exclusive"` is for item formulas that may mutate the original
member. Expansion reserves each active member with an atomic store claim before
unit creation. The claim must fail if the member is already assigned,
in progress, attached to live workflow/molecule work, or already has another
exclusive drain reservation. This check is implemented as one compare-and-set
reservation operation, not a read-then-create heuristic. If any reservation
fails, the drain emits a typed busy-rejection event and fails before creating
new units.

The reservation is stored on the original member bead as:

```text
gc.exclusive_drain_reservation = <drain_control_id>
```

Reservation is a two-step idempotent flow because the member bead and drain
control bead may not be updated atomically by all stores:

1. CAS the original member from clear reservation to
   `gc.exclusive_drain_reservation = <drain_control_id>`.
2. CAS the corresponding manifest row from `pending` to `reserved`.

If the controller crashes between those steps, replay sees the member
reservation owned by the same drain control and advances the row to `reserved`.
Reservation owner is the drain control ID, not a controller process ID. If
reserving member `k` fails, the controller releases reservations already
acquired for rows `0..k-1`, marks the drain failed, and emits `member_busy` with
per-member conflict records. Replays reuse reservations owned by the same drain
control and release stale reservations only when the manifest proves the drain
has failed or the row has reached a terminal item outcome.

`expected_clear_fields` for `ReserveDrainMember` are concrete: empty assignee,
not `status = "in_progress"`, empty `workflow_id`, empty `molecule_id`, empty
`gc.exclusive_drain_reservation`, and no open attached workflow or molecule child
according to the same attachment helper used by formula/molecule code.

This moves the policy decision into formula syntax and keeps Go responsible for
enforcing the declared access mode.

### Scale Bound

V0 is intentionally small. A drain may expand at most 100 active members unless
the formula sets a lower `max_units`. Values above 100 are invalid in v0. If the
resolved active member count exceeds the limit, expansion fails before
reservations or unit creation and emits a typed limit event.

Expansion work is chunked in controller iterations of at most 25 manifest rows.
The dispatcher must yield between chunks so health/order/reconciler work is not
starved. Future work may replace this with batched store APIs and higher
limits.

At the 100-unit cap, `DrainManifestV1` must stay below 256 KiB serialized.
`failure_reason` values are capped at 512 bytes per row; detailed evidence goes
into typed events or outcome beads. If the manifest would exceed the bound,
expansion fails with `limit_exceeded` before creating generated convoys.

## Drain-Unit Convoys

Generated drain units are normal visible convoy beads. They are not hidden
runtime state.

For v0 `one_by_one`, each generated drain-unit convoy:

- has `type = "convoy"`
- tracks exactly one original active member using the canonical `tracks`
  relation
- preserves the original member's existing parentage
- inherits priority from the original member
- does not inherit arbitrary labels
- does not inherit assignee, `gc.routed_to`, pool routing, workflow IDs,
  molecule IDs, or arbitrary metadata
- is excluded from worker hook/claim paths
- is exempt from generic convoy auto-close
- remains open after its item workflow succeeds until explicit synthetic convoy
  cleanup

Required metadata:

```text
gc.synthetic = true
gc.synthetic_kind = drain-unit-convoy
gc.parent_convoy_id = <input convoy>
gc.drain_control_id = <drain control bead>
gc.drain_index = <0-based index>
gc.drain_count = <total generated drain units>
gc.drain_member_id = <original member bead>
gc.drain_member_access = read | exclusive
gc.drain_unit_key = <deterministic unit key>
```

The generated drain-unit convoy is the item unit. There is no additional
wrapper bead between the drain control and the item formula.

### Synthetic Convoy Lifecycle

Synthetic convoys are visible, but inert:

- generic convoy auto-close ignores `gc.synthetic = true`
- `gc hook` and ready/claim paths do not return synthetic convoy beads
- synthetic convoys never carry `gc.routed_to`, pool routing metadata, workflow
  IDs, or molecule IDs
- `Ready()` and pool-demand predicates exclude `gc.synthetic = true` so demand
  calculation and hook selection share the same predicate
- dashboard/API/list surfaces expose `synthetic` and `synthetic_kind` as typed
  fields
- `gc convoy status` shows parent convoy, source member, drain control, index,
  count, and item root when present

V0 includes an explicit cleanup path:

```text
gc convoy gc --synthetic --older-than <duration> [--dry-run]
```

Cleanup closes drain-unit convoys only. Singleton convoys are not eligible for
`gc convoy gc --synthetic` in v0 because their key is stable per source bead and
later graph.v2 invocations may reuse them. A drain-unit convoy is eligible only
when every workflow root or drain control that references it is terminal and
older than the requested duration. A drain-unit convoy is not eligible while its
`gc.drain_control_id` points to a non-terminal drain control. The command never
closes original member beads, singleton convoys, or input convoys. There is no
implicit success-time auto-close for synthetic convoys, and v0 does not run this
cleanup automatically. The operational mitigation is to schedule
`gc convoy gc --synthetic --older-than <duration>` from external orchestration
for cities that use drain heavily.

Existing convoy commands handle synthetic convoys explicitly:

| Command | Synthetic behavior |
|---|---|
| `gc convoy list`, `status` | Show typed synthetic fields and lineage. |
| `gc convoy stranded`, `check` | Ignore synthetic convoys unless `--synthetic` is passed. |
| `gc convoy land`, `add`, `target`, `delete` | Reject synthetic convoy IDs with an actionable error. |
| `gc convoy gc --synthetic` | The only command that may close drain-unit synthetic convoys; it skips singleton convoys. |

## Item Formula Execution

The item formula named by `[steps.drain].formula` must be a graph.v2 formula.
It is instantiated once per drain-unit convoy with:

```text
convoy_id = <generated drain-unit convoy>
```

The item formula remains a normal graph.v2 formula. It does not receive
`item_bead_id`, `drain_item_id`, `drain_index`, or `drain_count` as system
variables. If it needs the underlying member in v0 one-by-one mode, it uses the
typed drain-member lookup contract:

```text
gc.drain_member_id on the generated drain-unit convoy
```

or a CLI/API helper that reads the same metadata. It must not infer the source
member by listing active members of the unit convoy, because read-mode drains
allow the original member to close while item work is pending.

The controller stamps item workflow roots with:

```text
gc.drain_control_id = <drain control bead>
gc.drain_index = <0-based index>
gc.drain_count = <total generated drain units>
gc.drain_member_id = <original member bead>
gc.drain_member_access = read | exclusive
gc.item_root_key = <deterministic item root key>
gc.input_convoy_id = <generated drain-unit convoy>
```

Drain completion is based on item workflow roots, not on generated drain-unit
convoys closing.

## Drain Output Contract

The drain control bead is the output handle for the drain step. Downstream
formula steps that `need` the drain step depend on the drain control bead.

Consumers discover item outputs from the typed `DrainManifestV1` on the control
bead. The manifest is ordered by `index` and maps each original `member_id` to
its generated `unit_convoy_id`, `item_root_id`, terminal status, and
`outcome_bead_id`.

`outcome_bead_id` is mandatory for successful item completion. The default
outcome is the item workflow root itself with `outcome_kind = "root_default"`.
A graph.v2 formula that produces a more specific durable artifact, such as
`review-quorum.summary.v1`, may stamp the item root with
`gc.outcome_bead_id = <artifact bead>`; the drain controller copies that value
into the manifest with `outcome_kind = "artifact"` during replay/completion.
The copy is monotonic: once a manifest row reaches terminal status, the
controller freezes `outcome_bead_id` and `outcome_kind` with a row-status CAS.
Downstream formulas read the typed artifact contract from the outcome bead
rather than guessing from item formula internals.

If `outcome_kind = "root_default"`, the item root remains addressable until the
drain control and its drain-unit convoy are cleanup-eligible. Wisp/root cleanup
must not remove a default outcome before downstream steps depending on the drain
control have reached terminal state.

This is not a gather policy. The controller does not interpret review results,
choose winners, synthesize prose, or close original members. It only provides a
stable, typed list of generated work so a downstream formula can gather or
synthesize explicitly.

Core review-quorum formulas must become convoy-native in the same migration:

- direct graph.v2 review-quorum runs derive their subject from `convoy_id`
- drain item review-quorum runs derive their subject from the one-member input
  convoy
- durable review outputs include `gc.input_convoy_id`,
  `gc.drain_control_id`, and `gc.drain_member_id` when present

## Shared Versus Separate

### Separate Context

`context = "separate"` creates independent item workflow roots.

- No continuation group is stamped by default.
- Item workflow roots are not chained to each other.
- The drain control waits for all item workflow roots to become terminal.
- The drain succeeds only if all item workflows pass.
- The drain fails if any item workflow fails, but it collects all reachable
  item outcomes before failing.

This is the parallel scatter form.

### Shared Context

`context = "shared"` preserves one live session context across the drain.

- Item workflow roots are chained at the item boundary.
- Item `i+1` waits according to `on_item_failure`: `skip_remaining` requires
  item `i` to reach successful terminal state; `continue` requires item `i` to
  reach any terminal state.
- Each item formula keeps its internal graph unchanged.
- The controller walks the materialized item workflow and stamps executable work
  beads, not just roots, with continuation metadata.
- Executable work receives `gc.session_affinity = "require"`.
- Failure behavior follows the formula-authored `on_item_failure` value. The
  shared default is `skip_remaining`: if item `i` fails, the controller closes
  not-yet-started later item roots with `gc.outcome = "skipped"` and
  `gc.skip_reason = "upstream_failed"`, then marks the drain failed. The drain
  control always reaches a terminal outcome.

Shared drain is valid only when the formula author declares
`[steps.drain.item].single_lane = true`. The controller then mechanically
verifies that the materialized item workflow has one executable target lane
before applying required-affinity continuation metadata. A graph.v2 item formula
that fans out to multiple independent dispatch targets, including v0
review-quorum, must use `context = "separate"`. This is a v0 restriction; a
future primary-lane annotation may allow shared context through one selected
lane without serializing independent quorum lanes. The issue #1709 review and
synthesis use case is satisfied in v0 by separate drain plus the typed drain
manifest; it does not require shared-context review-quorum.

An executable lane is a distinct materialized dispatch target reachable after
`extends` resolution, static condition evaluation, and loop/drain validation.
Retry/attempt beads inside one target lane do not create additional lanes.
If lane count cannot be statically determined before execution, the verifier
rejects shared drain and reports `invalid_item_formula`.

The stored continuation group key is controller-namespaced:

```text
drain:<drain_control_id>[:<operator_suffix_hash>]
```

The optional operator `continuation_group` value contributes only the suffix.
It cannot replace the controller namespace. Template substitution for that
suffix runs in the parent formula scope; `{{convoy_id}}` is the parent input
convoy.

Continuation metadata belongs on the work beads agents claim. It may also be
stamped on workflow roots for traceability, but hook and assignment behavior
must operate on executable work beads.

## Continuation Hook Behavior

`gc hook` preserves continuation context without adding a session cursor.

When a bead transitions to a closed terminal status, the store stamps:

```text
gc.closed_seq = <monotonic store-local close sequence>
gc.closed_at = <timestamp>
```

The continuation lookup uses `gc.closed_seq`, not creation time. Stores that
cannot provide a monotonic sequence reject shared drain with
`unsupported_store_primitive`.

`gc hook` v0 returns a structured status enum:

```text
status = work | wait | empty
```

`work` includes the assigned bead to run. `wait` means the caller is still bound
to a controller-namespaced drain continuation group but no same-group work is
ready yet. `empty` means normal hook selection found no work. Existing
non-drain continuation groups keep the old work/empty behavior; wait semantics
apply only to groups with the `drain:` prefix.

V0 hook order:

1. Respect already assigned in-progress work.
2. Query the latest terminal closed bead assigned to `$GC_SESSION_NAME`, ordered
   by `gc.closed_seq DESC`.
3. If that bead has no `gc.continuation_group`, run the normal work query.
4. If it has a continuation group, query same-group executable work assigned to
   `$GC_SESSION_NAME`.
5. If same-group ready work exists, return it.
6. If same-group blocked work exists and the drain manifest is not terminal,
   return `status = wait` so the session retries `gc hook` rather than falling
   through to unrelated work.
7. Fall through to normal work query only when the group is exhausted,
   terminal, or has no remaining work assigned to the session.
8. If no same-group work is found but the drain manifest is not terminal, return
   `status = wait` with reason `awaiting_materialization`; the next item may not
   be materialized or unblocked yet.

`gc.session_affinity = "require"` is enforced by the controller and hook
together:

- the controller assigns future same-group executable work to the current
  session name before the prior item sends `drain-continue`
- `gc hook` only returns required-affinity work to the matching session name
- a different session seeing required-affinity work treats it as not ready
- if the required session crashes, normal session crash-adoption may resume the
  same canonical session name
- if the session cannot be resumed according to existing session policy, the
  controller closes the current item root with `gc.outcome = "failed"` and
  `gc.failure_reason = "session_affinity_unavailable"`, closes any currently
  assigned same-group executable work it owns as failed when the store permits,
  skips untouched successors, and fails the drain instead of silently moving the
  work to another session

The affinity anchor is acquired mechanically. The first executable work bead in
item 0 that is claimed for a shared drain records its session name on the drain
control bead with `gc.drain_affinity_session = <session>`. This is a CAS: the
first claimant wins, and later executable work in the same drain must match that
session. The controller uses that session name when assigning item 1 and later
same-group executable work.

`gc runtime drain-continue` is a notification, not the source of truth for item
success. The name is intentionally distinct from the existing runtime
drain/shutdown command family. It targets a drain control ID and item index:

```text
gc runtime drain-continue <drain_control_id> --item-index <n> --last-work <bead_id>
```

The controller still derives item outcome from the item workflow root and
manifest reconciliation. The ack records that the owning session has finished
the current same-group work it can see and is available to continue. It may
advance assignment of the next shared item, but it cannot mark item roots
successful, skipped, or failed by itself.

The canonical shared-drain session loop is:

1. run work returned by `gc hook`
2. close the completed work/root according to existing formula behavior
3. call `gc hook` again
4. if `gc hook` returns `work`, continue in the same session
5. if `gc hook` returns `wait`, sleep and retry
6. if `gc hook` returns `empty` for the drain group, call
   `gc runtime drain-continue`

`wait` responses include `retry_after_ms`. The prompt starts at 2000 ms,
exponentially backs off to 30000 ms, and resets after any `work` response. The
controller emits `formula.continuation_hook_waiting` at most once per
continuation group per 60 seconds for the same reason code. The prompt must not
hand-roll a broad `bd ready` scan.

## Events And Operator Surface

Every new lifecycle transition emits a typed event with a registered payload in
`events.KnownEventTypes`.

Minimum event set:

| Event | Required payload fields |
|---|---|
| `formula.graphv2.singleton_created` | `source_bead_id`, `singleton_convoy_id`, `invocation_key`, `created_or_reused` |
| `formula.graphv2.invocation_prepared` | `formula_name`, `target_id`, `input_convoy_id`, `target_was_singleton` |
| `formula.graphv2.root_reused` | `graphv2_root_key`, `existing_root_id`, `input_convoy_id`, `reason` |
| `formula.drain.expansion_started` | `drain_control_id`, `parent_convoy_id`, `context`, `member_access`, `member_count` |
| `formula.drain.rejected` | `drain_control_id`, `parent_convoy_id`, `reason_code`, `member_ids`, `typed_reason_details` |
| `formula.drain.state_advanced` | `drain_control_id`, `prior_state`, `new_state`, `rows` |
| `formula.drain.reservation_released` | `drain_control_id`, `member_id`, `reservation_owner`, `release_reason` |
| `formula.drain.expanded` | `drain_control_id`, `unit_convoy_ids`, `item_root_ids`, `created_count`, `reused_count`, `rows` |
| `formula.drain.replay_reused` | `drain_control_id`, `unit_convoy_ids`, `item_root_ids`, `rows` |
| `formula.drain.affinity_anchored` | `drain_control_id`, `session_name`, `anchor_work_bead_id`, `continuation_group` |
| `formula.drain.item_chain_advanced` | `drain_control_id`, `from_index`, `to_index`, `session_name`, `continuation_group` |
| `formula.drain.completed` | `drain_control_id`, `status`, `succeeded_count`, `failed_count`, `skipped_count` |
| `formula.continuation_hook_matched` | `session_name`, `continuation_group`, `work_bead_id`, `closed_seq` |
| `formula.continuation_hook_waiting` | `session_name`, `continuation_group`, `reason_code`, `closed_seq` |
| `formula.synthetic_convoy_cleaned` | `convoy_id`, `synthetic_kind`, `reason`, `closed_at` |

Reason codes are enums, not free-form strings. Required v0 reason codes:
`dangling_member`, `missing_member`, `member_busy`, `limit_exceeded`,
`invalid_item_formula`, `session_affinity_unavailable`,
`upstream_failed`, `materialization_error`, `unsupported_store_primitive`,
`blocked_upstream`, `awaiting_materialization`, `affinity_mismatch`, and
`group_exhausted`.

`typed_reason_details` is a typed sum, not `map[string]any`. Each reason code
has its own payload struct. For `member_busy`, the payload contains one conflict
record per member with stable conflict enums and conflicting reservation,
workflow, molecule, session, and assignee IDs when present. For
`limit_exceeded`, the payload contains `resolved_count` and `max_units`. For
`dangling_member` and `missing_member`, the payload names the broken relation or
missing bead ID.

Drain row payloads share one typed row shape:

```text
index
member_id
unit_key
item_root_key
prior_status
new_status
action = created | reused | reserved | released | skipped | reconciled
release_reason = rollback | terminal_row | drain_failed | replay_stale
```

`gc trace` renders singleton normalization, drain expansion, replay reuse,
busy/limit rejection, item-chain advancement, and drain completion from typed
events. The API/SSE surface exposes typed drain lineage fields and synthetic
convoy fields; it must not require clients to parse raw `gc.*` metadata.

Critical-tier events are `formula.graphv2.root_reused`,
`formula.drain.rejected`, `formula.drain.state_advanced`,
`formula.drain.reservation_released`, `formula.drain.affinity_anchored`, and
`formula.drain.completed`. High-volume hook waiting events may use the optional
tier because `gc hook` responses are the source of truth for the caller.

## Future Shredders

V0 ships only the built-in `one_by_one` shredder. The syntax reserves space for
future custom shredders that take an input convoy and return an ordered list of
convoys:

```toml
[steps.drain.shredder]
command = "./scripts/slice-convoy --convoy {{convoy_id}}"
```

Future command contract:

- input is the original parent `convoy_id`
- output is an ordered JSON list of real convoy IDs, or enough structured data
  for the controller to create them
- returned convoys become drain units
- returned order defines shared-mode item order

Scripted shredders are not part of v0 because the one-by-one model is enough to
prove convoy-first formula execution and drain scheduling.

## Migration

Existing non-graph formulas remain bead-scoped and may continue using `issue`.

Existing graph.v2 formulas must migrate:

- replace `{{issue}}` with `{{convoy_id}}` when the formula operates on the
  input convoy as a whole
- if the formula really needs per-member work, express that as a drain whose
  item formula also receives a convoy
- stop relying on graph.v2 success to close the source input

The migration is intentionally fail-fast. There is no compatibility mode that
maps `issue` to either the source convoy or a singleton member.

The same PR that introduces validation must migrate and validate bundled
formulas:

- `internal/bootstrap/packs/core/formulas/mol-scoped-work.toml`
- `internal/bootstrap/packs/core/formulas/mol-review-quorum.toml`
- `cmd/gc/testdata/formulas/ralph-*.toml`
- examples and docs formula snippets

CI must load, resolve, and validate every formula under
`internal/bootstrap/packs/`, `examples/`, `docs/`, and formula testdata. The
gate must run the graph.v2 reserved-variable scan after `extends` and
`description_file` resolution so embedded formulas cannot ship broken. The gate
must validate the `go:embed` materialized pack payload as part of `go test`, so
an embedded broken graph.v2 formula fails before first runtime invocation.

Required bundled-formula migration shape:

- `mol-scoped-work.toml`: replace every `{{issue}}` reference with
  `{{convoy_id}}` and update prompts to inspect the convoy when the underlying
  bead is needed.
- `mol-review-quorum.toml`: derive `subject` from `convoy_id` when a caller does
  not provide a subject, stamp durable review outputs with `gc.input_convoy_id`,
  and stamp `gc.outcome_bead_id` on the item root when synthesis creates the
  `review-quorum.summary.v1` bead.
- `ralph-*` test formulas and docs snippets: either stay non-graph and keep
  `issue`, or become graph.v2 and use `convoy_id`.

## Worked Example

Input convoy `C0` tracks three open beads: `B0`, `B1`, and `B2`.

Shared drain over `C0` creates:

```text
D0  type=task    gc.kind=drain
U0  type=convoy  tracks B0  gc.synthetic=true  gc.synthetic_kind=drain-unit-convoy  gc.drain_index=0
U1  type=convoy  tracks B1  gc.synthetic=true  gc.synthetic_kind=drain-unit-convoy  gc.drain_index=1
U2  type=convoy  tracks B2  gc.synthetic=true  gc.synthetic_kind=drain-unit-convoy  gc.drain_index=2
R0  type=task    item workflow root, input convoy U0
R1  type=task    item workflow root, input convoy U1, depends on R0 success
R2  type=task    item workflow root, input convoy U2, depends on R1 success
```

Executable work inside `R0`, `R1`, and `R2` is stamped with the same
controller-namespaced continuation group and `gc.session_affinity = "require"`.
If `R1` fails, the controller marks `R2` skipped and marks `D0` failed.

Separate drain over `C0` creates the same `U*` and `R*` set, but `R0`, `R1`, and
`R2` are independent roots and no continuation group is stamped by default.

A downstream synthesis step depends on `D0`, reads `DrainManifestV1`, and gathers
the ordered item outcomes:

```toml
[[steps]]
id = "review-members"

[steps.drain]
context = "separate"
formula = "mol-review-quorum"

[[steps]]
id = "synthesize-reviews"
needs = ["review-members"]
prompt = "Read the drain manifest from the review-members control bead. For each row, open outcome_bead_id in index order and synthesize coverage across the parent convoy."
```

The synthesis step does not need `item_bead_id`. It uses the drain control bead
from `needs`, reads rows ordered by `index`, and follows each row's
`outcome_bead_id`.

A graph.v2 formula invoked directly on bead `B0` creates or reuses:

```text
S0  type=convoy  tracks B0  gc.synthetic=true  gc.synthetic_kind=singleton-convoy
```

The formula receives `convoy_id = S0`.

## Implementation Sketch

### Formula Layer

- Add `Step.Drain` parsing and validation.
- Reject `[steps.drain]` outside `contract = "graph.v2"`.
- Reject drain item formulas that are not graph.v2 at load/validate time, not
  first expansion.
- Require `[steps.drain.item].single_lane = true` for shared drain and
  mechanically verify the materialized item formula has one executable lane.
- Parse and validate `on_item_failure`.
- Validate drain-step field exclusivity.
- Implement graph.v2 reserved-name validation across fully resolved formulas.
- Preserve parser provenance for descriptions, description files, inherited vars,
  and dispatch-layer var collisions.
- Treat `convoy_id` as a system var for targeted graph.v2 invocation.

### Invocation And Dispatch

- Add the canonical graph.v2 invocation helper below CLI code.
- Detect graph.v2 formulas before sling batch/container expansion.
- Prove `PeekContract` and `PrepareGraphV2Invocation` use the same resolver and
  produce the same contract verdict for the same formula.
- Add a structural CI test that forbids targeted graph.v2 materialization outside
  `PrepareGraphV2Invocation`.
- For targeted graph.v2 invocation, normalize target to a convoy before
  template substitution.
- Create or reuse visible singleton convoys for bare beads.
- Reject `--no-convoy` with targeted graph.v2.
- Do not inject `issue` for graph.v2.
- Do not set source-chain metadata that triggers `closeSourceBeadChain`; use
  `gc.input_convoy_id`, `gc.graphv2_root_key`, and suppress source auto-close
  for graph.v2.

### Control Dispatcher

- Add `gc.kind = "drain"` handling.
- Claim drain control beads before expansion.
- Persist `DrainManifestV1` before generated artifact creation.
- Implement store unique-key upsert, CAS metadata, exclusive reservation, and
  closed-sequence primitives for in-scope stores.
- Expand drain idempotently into drain-unit convoys and item workflow roots.
- Enforce `member_access = "exclusive"` with atomic member reservations.
- Stamp `gc.item_root_key` on item workflow roots.
- Reconcile manifest outcomes from live item-root state on every replay.
- Enforce `max_units` and chunked expansion.
- Chain item workflow roots for shared context.
- Leave item workflow roots independent for separate context.
- Apply `on_item_failure` when item roots fail.
- Mark drain outcome from item workflow outcomes.

### Hook And Prompts

- Stamp `gc.closed_seq` on terminal close transitions.
- Extend `gc hook` with the `work | wait | empty` status enum and
  continuation-first lookup based on `gc.closed_seq`.
- Make hook wait instead of falling through when same-group assigned work exists
  but is still blocked.
- Enforce `gc.session_affinity = "require"` in hook readiness.
- Define `gc runtime drain-continue` as a notification that advances assignment but
  never determines item success.
- Update graph worker prompts to rely on `gc hook` before `drain-continue`
  instead of hand-rolled `bd ready` continuation queries.

### Events, API, And CLI

- Register every new event payload with `events.RegisterPayload`.
- Add typed API/SSE projections for synthetic convoy fields and drain lineage.
- Use typed per-reason rejection details, not unstructured event payloads.
- Extend `gc trace` to render graph.v2 invocation, drain expansion, replay,
  continuation, and drain completion events.
- Add `gc convoy gc --synthetic --older-than <duration> [--dry-run]`.

### Structural Tests

- A graph.v2 formula cannot be materialized from CLI, order dispatch, or
  store-backed attach paths without passing through `PrepareGraphV2Invocation`,
  including targetless graph.v2 materialization.
- `PeekContract` and `PrepareGraphV2Invocation` produce identical graph.v2 vs
  non-graph verdicts for the same resolved formula.
- `Ready()` and pool-demand predicates share the synthetic-convoy exclusion.
- `bd ready`, `bd blocked`, `bd stale`, and orphan detection share the same
  synthetic-convoy and synthetic-`tracks` exclusions.
- Graph.v2 roots never set `gc.source_bead_id`, and graph.v2 finalization cannot
  call `closeSourceBeadChain`.
- Embedded core formulas and materialized pack formulas pass graph.v2 validation
  after `go:embed`.
- Store backends either implement the required primitives or return
  `unsupported_store_primitive` before targeted graph.v2 or drain
  materialization.
- Cross-store source/convoy/drain/item materialization is rejected before
  expansion.

## Acceptance Criteria

Separate drain and targeted graph.v2 can ship once the store key/upsert
primitives land. Shared drain acceptance criteria are additionally gated on the
`gc.closed_seq` primitive-layer ADR and hook status enum.

- A graph.v2 formula invoked on a single bead creates or reuses a visible
  singleton convoy and receives `convoy_id`.
- Retrying that invocation after a controller crash reuses the same singleton
  convoy.
- Two different graph.v2 formulas invoked on the same non-convoy bead share the
  same singleton convoy and create distinct graph.v2 roots.
- A graph.v2 formula invoked on a convoy receives that convoy ID and does not
  expand it into one formula run per member.
- `gc sling`, `gc formula cook --attach`, formula-backed orders, and
  `MolCookOn` use the same graph.v2 target-normalization and var-injection
  path.
- Targetless graph.v2 `MolCook` and source-less orders use the same helper with
  `targetIDOrNil = nil` and reject `convoy_id`/drain before work is created.
- Concurrent identical graph.v2 launches produce one live root for a
  `gc.graphv2_root_key`; concurrent drain replays produce one item root per
  `gc.item_root_key`.
- Graph.v2 workflow conflict lookup, force replacement, and delete/reopen
  recovery operate on `gc.input_convoy_id` and `gc.graphv2_root_key`, not
  `gc.source_bead_id`.
- Graph.v2 formulas containing `{{issue}}`, `{{ issue }}`, `{{.issue}}`,
  `{{bead_id}}`, or reserved-name declarations fail validation before work is
  created.
- User, rig, order, inherited, and formula vars cannot override `convoy_id`.
- Targetless graph.v2 formulas still work when they do not reference
  `convoy_id` or use drain.
- Bundled core formulas and formula testdata pass the graph.v2 validation gate.
- `mol-review-quorum` can run directly on a convoy and as a separate drain item;
  successful item roots expose mandatory `outcome_bead_id` values pointing to
  the durable review summary artifact or to the item root default.
- A shared drain over three active members creates three drain-unit convoys and
  three item workflow roots chained in member order.
- A separate drain over three active members creates three independent item
  workflow roots and reports failure only after all reachable roots finish.
- Killing the controller after unit creation, after root creation, and after
  dependency wiring produces the same generated set and no duplicates on
  restart.
- Killing the controller after exclusive reservation but before unit creation
  releases or reuses reservations deterministically on replay and never strands
  a reservation owned by a dead controller process.
- Killing the controller after item root creation but before manifest update
  finds the root by `gc.item_root_key` on replay.
- Replaying after an item root reached terminal state while the controller was
  down reconciles the manifest from live item-root state.
- If item `i` fails in shared context, later item roots close with
  `gc.outcome = "skipped"` and the drain control bead becomes terminal failed.
- Drain-unit convoys are visible with `gc convoy status`, expose typed synthetic
  lineage, and track original members via `tracks`.
- Item formulas that need the original member read `gc.drain_member_id`; they do
  not infer it by listing active unit-convoy members.
- Synthetic singleton and drain-unit convoys are not returned by worker hook or
  ready/claim paths and are exempt from generic convoy auto-close.
- `gc convoy gc --synthetic` never closes singleton convoys in v0.
- Input convoys, drain-unit convoys, and original members remain open after
  successful item workflows unless formula work or explicit synthetic cleanup
  closes them.
- `gc hook` returns ready same-continuation-group assigned work before falling
  through to normal work query.
- `gc hook` returns `status = wait` instead of falling through when same-group
  assigned work is blocked or not yet materialized but the shared drain manifest
  is not terminal.
- Required-affinity work is never returned to a different session name.
- A shared drain whose required session cannot be resumed fails the drain
  instead of silently moving work to another session.
- `gc runtime drain-continue` can advance same-session assignment but cannot
  mark an item successful without live item-root terminal state.
- Drain expansion emits typed events for start, rejection, expansion,
  replay reuse, item-chain advancement, completion, and failure.
- Drain rejection events expose typed per-reason details; no rejection event uses
  unstructured `map[string]any` details.
- `gc trace` can reconstruct a drain run without parsing raw metadata.
- `gc convoy gc --synthetic --older-than <duration> --dry-run` lists only
  eligible drain-unit synthetic convoys whose referencing roots/controls are
  terminal and never singleton convoys, original members, or input convoys.
- A drain over more than 100 active members fails before creating generated
  convoys and emits a typed limit rejection.

## Future Work

- Scripted shredders that return ordered convoys or convoy specs.
- Rich gather policies and typed disposition aggregation.
- Dashboard Run visualization once O3 Run is designed.
- Higher drain fan-out limits backed by batch store APIs.
