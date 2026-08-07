# Research Artifact Passport and Data-Access Design

Status: proposed; awaiting user approval; design only

Date: 2026-08-07

Scope: autonomous research plan Chapter D. This document does not implement production code, does not freeze the V6 Plan/Result/Prompt/Gate schema reserved for Chapter E, and does not change the default orchestrator version.

## 1. Proposed decision

Use a normalized PostgreSQL artifact-passport model with four distinct concerns:

1. a stable passport identifies one canonical Research entity;
2. append-only versions bind an immutable schema version, canonical content hash, provenance, run versions, and access class to that entity;
3. a frozen context manifest records the exact artifact versions and representation bytes authorized for one Attempt;
4. typed input and supersession edges preserve derivation without copying domain content into the passport.

This is a class-table-inheritance design: for a domain row that already has a UUID, `research_artifact_passport.id` equals that canonical row ID. The domain row references the passport through its own UUID. A result currently embedded in `research_task_attempt.result` gains a first-class `research_result_artifact` row and a distinct UUID. Future E–N entities use the same convention from creation.

PostgreSQL remains the only canonical state store. No graph database, object-level ACL service, or second event store is introduced.

This proposal is not approved for implementation. User approval of this design is the only open decision; implementation planning and code changes must wait for that approval.

## 2. Existing system inspected

The current V1–V5 implementation has useful normalized domain state but no uniform passport or authorization graph:

- `research_task` identifies goal/plan version, kind, objective, expected result, acceptance criteria, assignment, lifecycle, and limits.
- `research_task_attempt` identifies one dispatch, immutable Agent attribution, inbox binding, result hash/JSON, runtime target fingerprints, lifecycle, lease, cancellation, and failure facts.
- V1–V5 `ResultEnvelope` is strict and version-selected. Source, Observation, Claim, Report, and Evaluation proposals are validated together; unknown fields fail closed.
- `research_source_snapshot` has source identity, bounded text, content hash, evidence traits, retrieval time, producer Task, and verification status.
- `research_observation` has Snapshot, quote/datum, locator, interpretation, content hash, producer Task, and verification status.
- `research_claim` and `research_claim_evidence` normalize Claim state and support/contradict links, but producer Attempt/Agent/model and exact consumed artifact versions are absent.
- `research_report` is revisioned and records producing Task, Attempt, and Agent. `research_report_claim` records exact prose anchors.
- V2–V5 quality/citation Evaluations are stored as `research_decision.outcome`; legacy surface evaluations also exist in `research_stage_eval`. V5 defects are structured but do not have an independent canonical artifact identity.
- `PostgresStore.TaskContext` currently loads the entire Run, Contract, Method, Questions, Tasks, Attempts, Sources, Observations, Claims, and Gate. `taskPromptModule` receives that broad `RunSnapshot`; the immutable V1–V5 prompts tell the Agent to call `multica research session get` for the complete session.
- `GetResearchSessionSnapshot` returns a workspace-scoped combined legacy and durable snapshot. The Agent route delegates to this same handler.
- result submission already checks workspace, active Fleet membership, task-bound credential, Inbox/Session/Task/Attempt binding, assigned Agent, orchestrator version, strict schema, and final identity/state inside the `AcceptResult` transaction.
- the offline evaluation harness correctly excludes hidden `Oracle` data from `SubjectInput`, but this isolation is a Go type boundary rather than a persisted access-class invariant.

Consequences: current code can prove who submitted a Result and whether evidence is structurally valid, but cannot prove the exact artifact versions shown to the Agent, enforce a reusable access lattice, identify Result/Evaluation as stable artifacts, or prevent a future context assembler from accidentally mixing evaluation-private or raw data into a task.

## 3. Alternatives considered

### 3.1 JSONB passport embedded in every domain table

Add a common `passport JSONB` column to Task, Attempt, Source, Observation, Claim, Report, and future tables.

Advantages: few joins and a small initial migration.

Rejected because PostgreSQL cannot enforce uniform keys, immutable provenance, cross-workspace references, exact version references, or access-class values inside independently evolving JSONB documents without duplicated triggers. Every E–N entity would repeat the same indexing and authorization logic. Input lineage would remain arrays of polymorphic JSON references, making referential integrity and reverse traversal unreliable.

### 3.2 Event-only provenance

Treat `research_run_event` as the passport and reconstruct consumed/produced state from events.

Advantages: append-only history and natural replay.

Rejected because current events are explicitly a projection outbox, not full event sourcing. Their payloads cannot rebuild all canonical tables. Retrofitting complete events would duplicate current normalized state, make authorization depend on replay, and exceed Chapter D. It would also fail the autonomous plan's rule that transaction order is an implementation dependency, not permission to reduce E–N.

### 3.3 Normalized passport, immutable versions, manifests, and edges — selected

A stable supertype row identifies the entity; append-only version rows hold immutable metadata; normalized manifests and references hold exact authorization and derivation.

Advantages: foreign keys and unique constraints enforce tenant/session boundaries; immutable versions remain meaningful while domain status changes; future E–N entities reuse one contract; reverse lineage and Projection edges are queryable; context and acceptance can authorize the same version IDs; migration can represent partial legacy knowledge honestly.

Cost: more joins and careful transaction ownership. The proposal accepts that cost because context assembly and result acceptance are bounded backend operations, and PostgreSQL indexes can serve their access patterns without a second store.

## 4. Canonical identity and normalized schema

Names below are normative for implementation planning. The migration may use PostgreSQL domains or CHECK constraints rather than native enums so additive future values do not require blocking enum rewrites, but accepted values and fail-closed behavior remain as specified.

### 4.1 Scope-key convention

Every D-owned version, result, context, link, omission, supersession, lifecycle, and policy row has `workspace_id uuid not null` and `session_id uuid not null`. No relationship relies on globally unique UUIDs alone. Before adding D foreign keys, the migration adds `UNIQUE (workspace_id, id)` to `research_session` and `UNIQUE (workspace_id, session_id, id)` to each referenced session-owned table that does not already have that key: `research_task`, `research_task_attempt`, `research_contract_revision`, `research_question`, `research_source_snapshot`, `research_observation`, `research_claim`, `research_report`, `research_decision`, `research_stage_eval`, `research_message`, `research_product_round_card`, `research_source`, `research_graph_node`, `research_graph_edge`, `research_claim_evidence`, `research_run_event`, and the D tables below. Existing narrower unique keys remain.

All scoped child references use `MATCH SIMPLE`, because the endpoint UUID may be nullable while the child scope is not. A non-null endpoint uses all three columns. Every two-ended link carries one scope pair and has a composite FK to each endpoint; therefore either endpoint in another workspace/session is rejected by PostgreSQL. Child indexes begin with `(workspace_id, session_id, ...)` in the same endpoint order as their FKs.

### 4.2 `research_artifact_passport`

One stable identity per canonical entity:

| Field | Contract |
| --- | --- |
| `id uuid primary key` | Canonical artifact ID. Equals the domain entity UUID for class-table kinds; Result Artifact and Context Manifest receive their own UUIDs. |
| `workspace_id uuid not null` | Tenant boundary. |
| `session_id uuid not null` | Research Run boundary; current Run identity is the durable Research Session ID. |
| `entity_kind text not null` | Registered kind; the SQL registry fails closed for unknown values. |
| `current_version integer` | Null until a verifiable version exists; thereafter a monotonically increasing projection of the latest committed version. |
| `eligibility_revision bigint not null default 1` | CAS fence. Advances whenever lifecycle, current version, verification eligibility, supersession, withdrawal, or access classification can change admission. |
| `lifecycle_status text not null` | `registered | accepted | rejected | stale | superseded | withdrawn`. Artifact admissibility, not domain status. |
| `provenance_completeness text not null` | `complete | partial | unknown`; legacy backfill states only what storage proves. |
| `source_created_at timestamptz` | Existing domain timestamp when stored; otherwise null. |
| `registered_at timestamptz not null` | Registration time; migration time for backfill, never fabricated historical acceptance. |
| `accepted_at`, `superseded_at` | Write-once nullable facts set only by the owning transaction. |

Required constraints include `UNIQUE (workspace_id, session_id, id)` and `UNIQUE (workspace_id, session_id, entity_kind, id)`. `current_version IS NULL` exactly when no version exists; otherwise `(workspace_id, session_id, id, current_version)` names a version. Terminal status cannot return to `accepted`. The passport stores no Question text, source bytes, Claim prose, Report Markdown, rubric, or Result body.

### 4.3 `research_artifact_version`

One append-only immutable metadata version:

| Field | Contract |
| --- | --- |
| `id uuid primary key` | Stable version reference. |
| `workspace_id`, `session_id`, `artifact_id` | Required scope and composite FK to `(workspace_id, session_id, id)` on passport. |
| `version integer not null` | Monotonic per passport; `UNIQUE (workspace_id, session_id, artifact_id, version)`. |
| `schema_name`, `schema_version` | Stable entity-content schema family and version. |
| `canonicalization_version` | Initially `research-artifact-c14n-v1`. |
| `content_hash` | `sha256:<64 lowercase hex>` over canonical immutable content. |
| `access_level` | One of section 6's four profiles. |
| `goal_version`, `plan_version` | Exact Run versions where known; null only when inapplicable or provably unknown. |
| `contract_revision_id` | Nullable scoped FK to `research_contract_revision`; null is not “current by implication.” |
| `strategy_version_id` | `CHECK (strategy_version_id IS NULL)` in D. Chapter M replaces that check with its scoped FK in the same migration that creates Strategy Version. |
| `produced_by_task_id`, `produced_by_attempt_id` | Nullable scoped FKs to Task and Attempt. |
| `produced_by_agent_id` | Immutable attribution value, deliberately not deletion-cascaded through `agent`. |
| `model`, `provider`, `execution_adapter` | Frozen producing Attempt facts when known. |
| `hash_origin` | `production | migration_recomputed | legacy_stored`. |
| `created_at` | Version registration time. |

Required keys/FKs are executable, not comments:

- `UNIQUE (workspace_id, session_id, id)` on version;
- FK `(workspace_id, session_id, artifact_id)` → passport;
- FK `(workspace_id, session_id, produced_by_task_id)` → Task;
- FK `(workspace_id, session_id, produced_by_attempt_id)` → Attempt;
- FK `(workspace_id, session_id, contract_revision_id)` → Contract Revision;
- when both producer IDs are non-null, a deferred check verifies the Attempt's `task_id` equals `produced_by_task_id`;
- the passport current-version pointer is enforced by a deferred scoped FK from `(workspace_id, session_id, id, current_version)` to version `(workspace_id, session_id, artifact_id, version)`; PostgreSQL permits the insertion cycle because it is `DEFERRABLE INITIALLY DEFERRED`.

An immutable trigger rejects update or delete of every version field. Corrections insert a new version and CAS passport `(current_version, eligibility_revision)` in the same transaction. Canonicalization excludes lifecycle projections; semantic content or access-class change creates a version, while pure lifecycle changes increment `eligibility_revision`. Canonical JSON uses sorted keys, semantic array order, normalized UUID/RFC3339 forms, and no locale formatting. Every registered kind has fixed test vectors.

### 4.4 `research_result_artifact`

Result and Attempt are distinct canonical entities. The row has `workspace_id`, `session_id`, `id`, `attempt_id`, orchestrator/result schema versions, bounded `result jsonb`, `client_request_id`, canonical result hash, `acceptance_policy_watermark`, and `accepted_at` (the last two nullable only when legacy acceptance policy/time is unprovable).

It has `UNIQUE (workspace_id, session_id, id)`, `UNIQUE (workspace_id, session_id, attempt_id)`, FK `(workspace_id, session_id, id)` → passport, and FK `(workspace_id, session_id, attempt_id)` → Attempt. A deferred kind trigger requires that passport to be `result_artifact`. These constraints make Result↔Attempt cross-scope attachment impossible and enforce one Result Artifact per Attempt.

The row is canonical for new writes. During rollback compatibility, Attempt `result`, `result_hash`, and `client_request_id` are written in the same `result.accept` transaction and equality-checked by a deferred constraint trigger. New readers use Result Artifact; old columns are not dropped in D.

### 4.5 Policy state, Context Manifest, entries, and omissions

`research_artifact_policy_state` has `workspace_id`, `session_id`, `policy_version`, and `watermark bigint not null`; this `watermark` is the session policy/lifecycle watermark; primary key `(workspace_id, session_id)`, unique `(workspace_id, session_id, watermark)`, and FK `(workspace_id, session_id)` → session `(workspace_id, id)`. Every lifecycle/access/verification/supersession mutation locks this row first and increments `watermark` in the same transaction. Policy deployment that changes admission increments every affected session watermark before new dispatch/acceptance.

`research_artifact_policy_mutation` is the append-only enforcement ledger. It has `workspace_id`, `session_id`, `watermark`, `artifact_id`, `old_eligibility_revision`, `new_eligibility_revision`, and `mutation_kind`; it has a scoped passport FK and unique `(workspace_id, session_id, watermark, artifact_id, new_eligibility_revision)`. An owner increments policy state once with `UPDATE ... SET watermark = watermark + 1 ... RETURNING watermark`, then inserts one ledger row per affected passport at that returned watermark. Deferred eligibility triggers require a matching ledger row and require policy state to be at least that watermark, so a direct lifecycle/access/verification/current-version update without the coupled advance fails at commit.

One `research_artifact_context_manifest` is frozen per Attempt. It has `workspace_id`, `session_id`, `id`, `attempt_id`, `task_id`, `purpose`, `policy_version`, `policy_watermark`, `through_state_version`, normal/evaluation clearances, bounded `principal_header_bytes`, `principal_header_hash`, `manifest_hash`, and `created_at`. The principal header freezes the compatibility Fleet/roster metadata that is not itself a Research artifact. It has scoped unique keys for `id` and `attempt_id`, FKs to Attempt and Task, FK from its `id` to a `context_manifest` passport, and a deferred check that Attempt.task_id equals manifest.task_id.

Each `research_artifact_context_entry` has `workspace_id`, `session_id`, `manifest_id`, stable `ordinal`, `artifact_version_id`, the selected passport `eligibility_revision`, `representation` (`metadata | excerpt | full`), bounded `representation_bytes bytea`, `representation_hash`, `use_kind`, and bounded reason. It has scoped FKs to manifest and version, unique `(workspace_id, session_id, manifest_id, ordinal)`, and unique `(workspace_id, session_id, manifest_id, artifact_version_id, representation, use_kind)`. The bytes are the exact frozen safe representation, not a pointer to mutable domain prose.

Each `research_artifact_context_omission` also carries scope, manifest ID, candidate version ID, ordinal, and reason (`access_denied | stale | superseded | duplicate | token_budget | irrelevant`). It has scoped FKs to both manifest and candidate version and stores no denied content, representation bytes, private hash, or private metadata.

Ordering is deterministic: direct target, relation/use kind, importance, canonical kind, artifact ID, then version. `manifest_hash` covers scope, Attempt/Task, purpose, policy version/watermark, Run state watermark, and every ordered entry's version ID, eligibility revision, representation, and representation hash.

### 4.6 Input, supersession, and lifecycle links

Every link row below carries `workspace_id` and `session_id` and references both endpoints with scoped composite FKs:

- `research_artifact_input_reference`: `consumer_version_id`, `input_version_id`, relation, nullable `manifest_id`, `explicitly_used`, purpose, ordinal. Both version endpoints and optional manifest are scoped FKs; self-reference is forbidden; unique `(workspace_id, session_id, consumer_version_id, input_version_id, relation)`.
- `research_artifact_supersession`: `successor_version_id`, `superseded_version_id`, reason, nullable `decision_id`, created time. Both versions and Decision are scoped FKs; self-reference and cycles are forbidden; unique scoped endpoint pair.
- `research_artifact_lifecycle_event`: scoped passport ID, old/new status, nullable Decision, actor facts, reason, created time. Passport and Decision are scoped FKs; the table is append-only.

The manifest version has `consumed` edges to each entry. Accepted outputs have `derived_from` edges to the manifest version and direct edges for explicit typed references. No D reference crosses a session.

### 4.7 Exact polymorphic class enforcement

PostgreSQL cannot express “passport `entity_kind` selects one of many domain tables” as a declarative FK. D therefore uses exact deferred constraint triggers rather than pretending a direct FK exists:

1. `research_artifact_passport_class_guard` is an `AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind CONSTRAINT TRIGGER`, `DEFERRABLE INITIALLY DEFERRED`, on passport. Its function `CASE`s over every registered kind and performs `SELECT 1` against that kind's domain table using `(workspace_id, session_id, id)`; no row or unknown kind raises SQLSTATE `23503` with constraint name `research_artifact_passport_class_guard`.
2. Each registered domain table gets a reciprocal `AFTER INSERT OR UPDATE OF id, workspace_id, session_id CONSTRAINT TRIGGER`, also deferred, which requires exactly one matching passport with the expected kind. Result Artifact and Context Manifest use this plus their direct scoped passport FK.
3. `research_artifact_passport_delete_guard` is a `BEFORE DELETE` trigger that checks the kind-selected domain table and rejects while the reciprocal row exists. Each domain table has a matching `BEFORE DELETE` guard that rejects while its passport exists. Lifecycle withdrawal, not deletion, is the production removal operation. Migration rollback drops the guards before D tables.
4. `research_artifact_version_producer_guard` is a deferred constraint trigger that, when both producer IDs exist, verifies scoped Attempt.task_id equals Task.id and that copied agent/model/provider/adapter facts equal the immutable Attempt facts when populated.
5. `research_result_attempt_projection_guard` is a deferred trigger that verifies scope, one-to-one identity, and equality of the canonical Result hash/body/request ID with non-null Attempt compatibility columns.

Integration tests force `SET CONSTRAINTS ALL IMMEDIATE` before commit to prove each valid and invalid case at the statement under test, then repeat at normal deferred commit. This is the executable enforcement strategy for class polymorphism; application checks are defense in depth only.

## 5. Entity coverage

D1 registers every current platform-selected Research artifact that can appear in a task Prompt, task-bound Session response, accepted Result, or provenance edge:

- Research Run/Session, Contract Revision, Method Decision, Question, Task, Attempt, Result Artifact;
- legacy `research_source`, durable Source Snapshot, Observation, Claim, Evidence Link, Report Revision;
- quality/citation Evaluation Decision and legacy Stage Evaluation;
- Research Message, Product Round Decision, Context Manifest, and Run Event;
- legacy graph node/edge whenever the stored row is itself selected (including as the source of a Thought Strategy representation). Durable canvas nodes/edges and Gate that are regenerated from Run entities are projections and do not receive duplicate passports.

Fleet/member identity is principal and routing metadata, not a Research artifact. It is frozen as a bounded manifest header and hashed into the Context Manifest, but individual Agent/User rows do not become Research passports. Thought Strategy is a graph-node representation, not a second canonical entity.

The registry remains extensible for all final-plan entities: Search Plan, Query Execution, Source Candidate, Screening Decision, Hypothesis, Branch, Insight, Insight Derivation, Integration Round, Integration Contribution, Dispute, Position, Deliberation/Turn, Team Formation/Membership, Divergence Pass, Capability Observation, Monitoring Cycle, Episode, Strategy Version, Promotion Decision, and Evaluation Defect. Their domain schemas and state machines remain owned by E–N.

## 6. Data-access lattice

The four names are policy profiles, not a misleading total sensitivity enum.

### 6.1 Normal chain

For ordinary Research execution:

`verified_only ⪯ redacted ⪯ raw`

A principal with `raw` clearance may receive all three normal profiles; `redacted` may receive redacted and verified-only artifacts; `verified_only` may receive only verified-only artifacts.

- `raw`: original unredacted or untrusted content. Verification does not lower it.
- `redacted`: an explicitly derived, policy-scanned representation whose disallowed fields/content were removed. It must have a `redacts` edge to its input.
- `verified_only`: a representation that is both safe for the ordinary synthesis surface and backed by accepted verification state. It must be generated from canonical verified facts; merely changing a label is forbidden.

An artifact produced from multiple inputs receives the least permissive normal profile required by those inputs. Consuming raw data therefore produces raw output unless an authorized redaction transformation creates a distinct version and edge. Consuming unverified data cannot produce `verified_only` without a verification operation and evidence.

### 6.2 Evaluation compartment

`evaluation_private` is incomparable with the normal chain. It is a compartment bit, not “more raw.” It includes hidden ground truth, grader expectations, private rubrics, withheld fault configuration, and Promotion evaluation material.

Only an internal evaluation principal or independently authorized grader purpose may read it. An evaluated Agent, its task-bound credential, ordinary Fleet member, Reporter, Integrator, Research Director, human session snapshot, canvas projection, logs, and realtime payload cannot receive it merely because they have raw clearance. A process needing both normal artifacts and hidden evaluation data must have two explicit grants; the evaluation-private taint propagates to its outputs unless an approved aggregate-release operation produces a separate normal artifact.

The existing `researcheval.Case.SubjectInput()` split remains, but persistence and context assembly make the same separation enforceable outside one Go call.

### 6.3 Authorization decision

Every selection evaluates:

`workspace + session + principal + active membership + task/attempt binding + purpose + normal clearance + evaluation compartment + artifact status + exact version + representation`

Deny is the default. Unknown access levels, purposes, entity kinds, future enum values, missing passports, mismatched tenant/session, stale/superseded versions not explicitly requested for audit, and missing provenance required by the task all fail closed.

## 7. Context assembly authorization and dispatch ownership

Introduce one internal `artifactContextModule`; no Handler, Prompt builder, or CLI route may independently list Research tables for a D-enabled Agent task.

There is one executable transaction owner: `PostgresStore.CreateDispatchIntent`. Outside the transaction, `artifactContextModule` may read candidates and, only after a non-authoritative coarse access filter, precompute bounded representation bytes, hashes, token costs, and a candidate ordered selection. Access-denied candidates carry identity/reason only and are never fetched into representation bytes. That work is an optimization only and carries expected `(version ID, passport current_version, eligibility_revision, lifecycle, access level, verification fact, content hash, representation hash)` values. It grants no authority and creates no Attempt.

Inside `CreateDispatchIntent`, using merged Chapter C operation label `txOpDispatchIntentCreate`:

1. call `s.beginResearchTx(ctx, txOpDispatchIntentCreate, ...)`; no direct `BeginTx` is allowed;
2. lock/re-read in this global order: Run/session row; session policy-state row; Task row; candidate passports by UUID; candidate versions by UUID; then kind-specific verification/contract/producer rows by `(kind, UUID)`;
3. re-run root traversal, access, lifecycle, freshness, verification, supersession, deterministic ordering, and budget selection against those locked rows;
4. require exact equality with the candidate selected/omitted sets and every expected fact. Insert entries through `INSERT ... SELECT` predicates over scoped passport/version facts and require the selected row count. CAS each selected passport with `UPDATE ... SET eligibility_revision = eligibility_revision WHERE workspace_id=? AND session_id=? AND id=? AND current_version=? AND eligibility_revision=? AND lifecycle_status=?`; any count mismatch aborts. CAS Run `state_version`, then reserve the final dispatch policy watermark with `UPDATE research_artifact_policy_state SET watermark = watermark + 1 WHERE workspace_id=? AND session_id=? AND watermark=? AND policy_version=? RETURNING watermark`; zero rows abort. Insert policy-mutation rows for the new Attempt/Manifest passports and store the returned final watermark in the manifest;
5. validate or regenerate each precomputed representation from the locked canonical version; its bytes and hash must agree. Build the manifest-derived `RunSnapshot` and version-selected V1–V5 Prompt inside this method using pure renderers only;
6. compute `manifest_hash` from the exact entry bytes/hashes and compute the dispatch request hash only after Prompt bytes and manifest identity are final;
7. atomically insert Attempt + Attempt passport/version, Context Manifest + passport/version, entries/omissions/edges, frozen dispatch outbox payload containing those exact Prompt bytes and manifest ID/hash, Task transition, and dispatch Event;
8. call `s.commitResearchTx(ctx, txOpDispatchIntentCreate, tx)`; no direct `Commit` is allowed.

All lifecycle/access/verification/supersession writers use the same Run → policy-state → passport/version lock order and increment both the affected passport `eligibility_revision` and session policy watermark. They therefore cannot pass between dispatch recheck and commit. An unrelated watermark advance makes dispatch retry and recompute rather than silently using stale policy.

Prompt and manifest cannot diverge: the renderer receives only the in-transaction manifest view built from the same `representation_bytes` inserted as entries; the outbox is encoded after that render; and persisted `prompt_hash`, request hash, and manifest hash bind all three. `CreateDispatchIntent` no longer accepts a caller-built context-bearing `DispatchRequest` as authority. Replay compares the stored hashes and returns the existing Attempt; it never re-renders. A failure before commit creates none of Attempt/manifest/outbox/Event, and an ambiguous post-commit error converges through Chapter C replay.

For `multica research session get`:

- human workspace principals keep the product snapshot filtered by human policy;
- a task-bound Agent on a D-enabled Attempt receives the frozen manifest projection for that Attempt, not a fresh whole-Run query;
- an Agent without valid task binding receives only a separately designed bounded Fleet overview;
- evaluation subject and grader use separate manifests and grants.

Historical non-D runs retain the existing live route as defined in section 9. Prompt builders remain pure and cannot query storage or widen a manifest.

## 8. Result-acceptance authorization

Preflight checks credential/Attempt/Inbox binding and resolves every existing-artifact reference into an exact scoped version. Local keys created within one envelope continue to resolve under existing V1–V5 schemas. Existing report Claim/Source references, answer Claim references, verification reuse, and canonical references must be in the Attempt manifest or a server-created version explicitly granted to that task. Unmanifested, newer, denied, cross-scope, or evaluation-private references fail closed.

`PostgresStore.AcceptResult` is authoritative and uses Chapter C label `txOpResultAccept`. It calls `s.beginResearchTx`/`s.commitResearchTx`; D adds no direct transaction boundary. Before any output insert it locks in this deterministic global order:

1. Run/session;
2. session policy-state watermark;
3. Task, then Attempt;
4. Context Manifest and its entries;
5. every referenced passport ordered by UUID;
6. every referenced version ordered by UUID;
7. kind-specific verification/contract/producer rows ordered by `(kind, UUID)`.

It then verifies manifest/Attempt/task/principal identity, Prompt/manifest hashes, Result schema version, and all scoped references. If the current policy watermark differs from the manifest watermark, it re-evaluates every reference under the locked current policy; withdrawal, lifecycle, access, verification, or supersession changes reject the Result. If the watermark advanced only for unrelated artifacts and all referenced eligibility revisions/facts still match, acceptance may proceed. Before writing outputs it reserves one new final watermark with the same compare-and-increment statement, inserts policy-mutation rows for every new/changed output passport, and records that returned value as `research_result_artifact.acceptance_policy_watermark`. Every referenced passport is CAS-checked on `(current_version, eligibility_revision, lifecycle_status)` and every version on immutable `(id, content_hash, access_level)` predicates before writes.

Only after those checks does the transaction create Result Artifact/passport/version, materialize produced domain rows and passports/versions, create scoped lineage/supersession/lifecycle rows, write the exact Attempt compatibility projection, Decision, and Event, and terminally transition the Attempt. Output access taint is derived from locked inputs; callers cannot choose it.

A preflight success followed by revocation or version change therefore returns typed conflict/forbidden and commits no output. Idempotent replay requires identical client request ID, canonical Result hash, manifest ID/hash, resolved input version set, and acceptance policy watermark semantics; changed lineage conflicts.

D adds no `input_refs` field to V1–V5 and does not freeze V6. Legacy explicit references are inferred from existing typed fields, and the manifest remains the complete available-input record. Chapter E may require explicit V6 references.

## 9. V1–V5 compatibility, explicitly redefined

Compatibility distinguishes immutable wire/content contracts from the old live-read behavior:

1. Existing V1–V5 Prompt renderer code, Result decoders/validators, canonical Result hashes, report schema, and version-selected Gate rubric remain unchanged. Stored Prompt and accepted Result bytes are never rewritten.
2. Historical Attempts/Runs created before D enablement keep their existing live task-bound `session get` behavior. D does not fabricate a manifest and claim it was frozen at their dispatch.
3. New D-enabled V1–V5 Attempts use the same renderer and Result/Gate versions, but context is frozen by manifest at dispatch. Their task-bound `session get` is manifest-derived and can intentionally differ from the later human live snapshot. Gate/reference authorization is evaluated against that frozen manifest plus the locked current revocation policy described in section 8.
4. “Prompt compatibility” means identical bytes for identical authorized manifest representations and unchanged golden fixtures, not a promise that a newly dispatched task sees bytes from a hypothetical later whole-Run query. Any context change legitimately changes Prompt bytes through the existing renderer.
5. Existing accepted Results are not re-parsed, re-gated, or reclassified. New Result Artifact rows are canonical only for new writes; Attempt columns remain exact rollback projections.
6. Old desktop/browser fields remain readable. Passport summaries are additive and schema-parsed with defaults; full passports are not injected into existing objects.
7. Runs with `run_initialized_at IS NULL` remain legacy execution. Historical durable runs without D enablement also retain historical live reads; D enablement is an explicit per-Run/Attempt fact, never inferred from current server version.
8. Rollout requires the exact legacy-admission shadow contract in section 10. A mismatch blocks D enablement, not historical execution.

Golden tests continue to pin V1–V5 Prompt SHA-256 values for fixed fixtures, Plan/Result hashes, future-field rejection, Gate behavior, stale-result behavior, report/evaluation rules, and canonical business state. Separate tests pin the intended historical-live versus D-enabled-frozen route split.

## 10. Migration, legacy admission, and shadow equivalence

Migration is additive, restartable, and never fabricates history.

### 10.1 Backfillable facts

Stable IDs/scope/kind/current stored content/stored timestamps; stored producer references; immutable Attempt attribution; historical stored Result hash only when it verifies under its historical canonicalizer; migration-time recomputed hashes; and explicitly stored goal/plan/orchestrator versions may be backfilled.

Historical inputs, actual read behavior, pre-M Strategy Version, unrecorded redaction/verification, acceptance time not proven by storage, missing producer identity, and nonexistent E–N lineage remain null/partial. No historical lifecycle event claims acceptance.

Old succeeded Attempts with a valid stored Result may receive a Result Artifact; `result_submitted_at` is source time and is acceptance time only when existing invariants prove it. Missing/malformed payloads create no Result/passport and are recorded in a bounded diagnostic ledger.

### 10.2 Exact admission for `registered + partial|unknown`

Backfilled passports use `lifecycle_status=registered`, `provenance_completeness=partial|unknown`, and migration-time `registered_at`. `registered` is not generally equivalent to `accepted`. The only ordinary-task exception is named policy `legacy-v1-v5-compat-v1`:

- it applies only to a V1–V5 task on a Run explicitly D-enabled after shadow success;
- the legacy row must have exact workspace/session identity, a recognized kind, a current immutable version, a recomputed/stored hash that verifies, a nonterminal domain state, and no withdrawn/rejected/stale/superseded fact;
- the artifact must be reachable by the same typed current `TaskContext` relationship that made it visible before D; text search or session-wide fallback is forbidden;
- `partial` and `unknown` refer only to producer/input provenance. If content identity, scope, current bytes, or access classification is unknown, no version is created and admission is denied;
- both may be admitted using the conservative access class derived by migration: `raw` unless an existing persisted redaction/verification invariant proves `redacted` or `verified_only`; neither can become `evaluation_private` by inference;
- admitted representation bytes must equal the legacy safe serializer bytes. Output lineage propagates the weaker completeness and cannot claim complete provenance;
- this exception is forbidden for V6+, evaluation grader material, cross-session import, and new artifacts created after D enforcement. New production artifacts must become `accepted` through their owner transaction.

Thus `registered+unknown` is not a wildcard: unknown producer history is tolerable for byte-identified legacy compatibility; unknown content/scope/access is not.

### 10.3 Shadow equivalence

For each prospective D-enabled V1–V5 dispatch, shadow mode runs legacy `TaskContext` and manifest selection over one database snapshot and compares, in deterministic order: family/kind, canonical domain ID, version content hash, chosen representation bytes/hash, and omission classification. It also renders both Prompts and compares bytes/hash. The expected equal set includes every legacy family enumerated in section 13.

There are only two acceptable outcomes: exact equality, or an explicit deny that the old path exposed data already forbidden by a pre-D invariant, with a named policy reason and a positive allowed control. Any unexplained omission, addition, reorder, representation change, missing passport, or `registered` admission failure blocks Run enablement. Production task-bound reads switch only per Run after this comparison passes; historical runs continue live behavior.

Down migration removes only D-owned tables/triggers/indexes/additive keys after disabling enforcement. It preserves Attempt compatibility columns and never attempts to reconstruct or “unaccept” history.

## 11. Status, supersession, revocation, and watermarking

Artifact and domain lifecycle remain separate. A running Task may have an accepted passport; a refuted Claim remains an accepted historical artifact with refuted domain status; superseded/stale content remains audit-readable; withdrawal removes future ordinary admission without deleting history.

Every transaction that can change context or acceptance eligibility—passport lifecycle, domain verification status, current version/access class, supersession, withdrawal, policy version/grant, or legacy-admission eligibility—locks Run then policy state then affected passports in UUID order, increments each affected `eligibility_revision`, and increments the session policy watermark once. Direct updates that omit this path are rejected at deferred-constraint time because no `research_artifact_policy_mutation` row matches the old/new eligibility revisions at the transaction's returned watermark. This is the shared TOCTOU fence used by sections 7 and 8.

Supersession requires a typed reason and Decision and performs a cycle check under locked scoped endpoints. Claim equivalence/refinement remains a later domain relationship; matching hashes never merge authorship or provenance.

## 12. Transaction ownership and Chapter C dependency

Chapter C foundation PR #2526 is merged and lands before D. D extends that foundation; it does not recreate or bypass it.

Only the module owning the business invariant writes passports. `CreateDispatchIntent` owns Attempt/manifest/Prompt/outbox atomicity; `AcceptResult` owns Result and produced artifacts; steering/replan/verification and E–N domain transactions own their lifecycle/lineage mutations. Projection remains post-commit and read-only.

All D write boundaries use the merged `PostgresStore.beginResearchTx` and `PostgresStore.commitResearchTx` methods with existing labels `txOpDispatchIntentCreate` and `txOpResultAccept` (and the relevant existing label for every other owner). No D code calls `s.pool.BeginTx`, `tx.Commit`, or introduces a nested transaction. Transaction-scoped artifact helpers accept `pgx.Tx` and cannot begin/commit. The Chapter C structural guard is expanded to every D-mutated file, and Chapter C before-/after-commit recovery tests count D rows as canonical children.

There is no public artifact CRUD API. Deferred class-pairing triggers prevent a required domain row from committing without its passport/version and prevent an orphan production passport.

## 13. Complete task-visible response policy and API implications

The current task-visible Session response is exhaustively classified below. D-enabled task routes may not silently add another table query; a new response family must update this table, passport registry/policy, and shadow test in the same change.

| Current family | Passport / manifest representation for D-enabled task | Removal / compatibility policy |
| --- | --- | --- |
| `session` | Research Run/Session version; bounded metadata plus exact goal/version/state fields selected for the task. | Family retained. Historical runs remain live; D-enabled bytes are frozen. |
| `fleet` | Not a Research passport; bounded authorized roster/principal header hashed into manifest. No secrets/config. | Family retained at compatibility shape; unauthorized members/config removed by existing membership policy. |
| legacy `research_source` (`sources`) | `legacy_research_source` passport/version under section 10 admission; bounded legacy source representation. | Retained for historical and V1–V5 compatibility; no silent substitution with Source Snapshot. V6 removal requires an explicit versioned contract. |
| graph `nodes` / `edges` | Stored graph rows use legacy graph passports when selected directly, including a node that supplies Thought Strategy. Durable canvas rows projected deterministically from manifested Run entities/versions are not second passports. | Family retained. D-enabled route cannot query unmanifested graph rows; historical route remains live. |
| `messages` | `research_message` passport/version; bounded message/card representation with principal and target checks. | Family retained only for messages selected into the manifest; later messages are absent from the frozen task view. |
| `product_rounds` | `product_round_decision` passport/version; bounded decision representation. | Family retained with manifest-selected rounds; no live later rounds. |
| `thought_strategies` | Representation of the manifested legacy graph-node version that owns the strategy payload; no duplicate strategy passport. | Family retained when its source node is manifested; no fallback query over all nodes. |
| `report` | Report Revision passport/version; authorized structured/Markdown representation. | Family retained; absent if no authorized version existed at dispatch. |
| `evals` / legacy Stage Evaluations | `legacy_stage_evaluation` passport/version; safe normal representation. Evaluation-private rubric/ground truth uses separate grader manifest and never this family. | Family retained for safe evals selected at dispatch. |
| durable `run` envelope | Envelope is a projection, not one opaque artifact. `Run`, Contract, Method, Questions, Tasks, Attempts, durable Source Snapshots, Observations, Claims, and Gate are handled as the rows below. | Top-level family retained for V1–V5 compatibility, assembled only from entries. |
| durable Run metadata | Run/Session version representation. | Retained and frozen. |
| durable Contract | Contract Revision passport/version with direct scoped version FK. | Retained and frozen. |
| durable Method | Method Decision passport/version. | Retained when selected; null remains null. |
| durable Questions | One passport/version per Question. | Retained ordered subset equal to shadow baseline. |
| durable Tasks | One passport/version per Task. | Retained ordered subset equal to shadow baseline, including direct target. |
| durable Attempts | One passport/version per Attempt; mutable runtime state is a bounded lifecycle projection tied to manifest watermark, not silently read later. | Retained frozen at dispatch for D-enabled task read. |
| durable Source Snapshots | One passport/version each; excerpt/full according to clearance. | Retained authorized subset. |
| durable Observations | One passport/version each. | Retained authorized subset. |
| durable Claims | One passport/version each. | Retained authorized subset. |
| durable Gate | Deterministic safe projection from manifested Evaluation/Decision versions and frozen gate policy version; no duplicate Gate passport. | Retained and frozen for D-enabled task view; later human Gate remains live outside task route. |

Human snapshots remain product-live and principal-filtered; only task-bound D-enabled reads are frozen. Old clients receive the same top-level families and fail-safe additive passport summaries. A separate authorized detail endpoint is preferred over embedding full passports.

Projection identity remains `(run_id, entity_kind, entity_id)` where entity ID is passport ID. Authorized detail may expose bounded schema/version/lifecycle/provenance/producer/access metadata and counts. Raw hashes, denied IDs, private existence, locators, and omission details are hidden. `projectRunV2Graph` remains a compatibility projection until N and cannot become passport authority.

## 14. Security properties

- Every passport/version/manifest/reference query includes `workspace_id` and `session_id`; composite foreign keys reject cross-boundary references even if application validation is bypassed.
- Task-bound credentials can read only their manifest. Active Fleet membership alone does not grant raw or evaluation-private access.
- Access checks occur before content fetch. Denied rows do not enter token budgeting, logs, metrics, projection payloads, or error strings.
- Hashes of private content are sensitive metadata because they can be dictionary-tested. Full hashes are not exposed outside authorized audit paths or logged.
- External source content remains untrusted data. `raw` never enters instruction sections; renderers use explicit data delimiters and existing Prompt-injection protections.
- Redaction and verification are transformations that produce new versions and edges; callers cannot lower access by submitting a different enum.
- Evaluation-private data uses separate purpose/grant checks and propagating taint. Workspace admin, Research Director, and raw clearance do not imply evaluation access.
- Model/provider/config provenance stores identifiers and irreversible fingerprints only, never credentials, environment variables, tokens, or raw tool output.
- Errors are bounded and state which reference/policy class failed without revealing denied content or hidden artifact existence to an unauthorized principal.
- No cross-workspace Capability Observation, Episode, or evaluation export can traverse an artifact reference; Chapter M must build privacy-preserving aggregates explicitly.

## 15. Exact acceptance tests

D is complete only when these executable tests pass:

1. migration fresh up, down/up, lint, and backfill reconciliation; no fabricated E–N rows;
2. scope-key DDL inspection proves every D version/result/context/entry/omission/input/supersession/lifecycle/policy-state/policy-mutation row has non-null workspace/session and both endpoint composite FKs;
3. direct FK tests reject cross-workspace and cross-session Result↔Attempt, manifest↔Attempt/Task, entry/omission↔manifest/version, input-reference endpoints, supersession endpoints, lifecycle↔passport/Decision, policy-mutation↔passport, and version↔passport/Task/Attempt/Contract; same-scope controls pass;
4. deferred class-trigger tests reject missing domain row, missing passport, wrong kind, wrong scope, orphan delete, producer Attempt/Task mismatch, and Result/Attempt projection mismatch under both `SET CONSTRAINTS ALL IMMEDIATE` and commit; valid controls pass;
5. version immutability and current-version deferred FK reject update/delete, missing current version, and invalid CAS and eligibility change without a policy-mutation ledger row; concurrent writers leave one current version and monotonic watermark;
6. legacy backfill distinguishes `legacy_stored` and `migration_recomputed`, skips malformed Results, and records partial/unknown counts without false acceptance time;
7. legacy admission table tests cover `registered+partial`, `registered+unknown-producer`, and denial for unknown scope/content/access/hash, V6, evaluation-private inference, stale/withdrawn/superseded, and post-D artifacts; every denial has an allowed control;
8. shadow equivalence fixture contains legacy source, graph nodes/edges, messages, product round, thought strategy, report, stage eval, and every durable Run subfamily; it compares ordered IDs/kinds, hashes, representation bytes, omissions, and Prompt hash. Removing any one family makes the test fail;
9. access matrix covers all normal clearances plus evaluation compartment; raw taint, authorized redaction, and verified-only transformation have positive/negative controls;
10. evaluated-subject serialization contains zero private IDs/hashes/metadata/content while grader receives the expected private version;
11. dispatch race pauses after out-of-transaction representation precompute, then mutates lifecycle, access/current version, verification, supersession, Run state, and policy watermark in separate subtests. `CreateDispatchIntent` must conflict/recompute and leave zero Attempt/manifest/outbox/Event from the stale candidate;
12. dispatch lock-order concurrency test runs inverse candidate UUID orders without deadlock and proves persisted entry order is canonical;
13. dispatch CAS test changes one selected passport eligibility revision and one representation byte/hash; each mismatch aborts all writes;
14. Prompt/manifest binding test reconstructs the Prompt solely from persisted ordered entry bytes and obtains the exact outbox Prompt bytes/hash; changing an entry, manifest hash, or outbox Prompt is detected. Replay never calls the renderer;
15. Chapter C `txOpDispatchIntentCreate` after-begin, before-commit, successful commit, and after-commit-unknown tests count Attempt passport/version, manifest passport/version, entries/omissions/edges, outbox, Task transition, and Event as one atomic unit;
16. historical task-bound `session get` remains live after later state mutation; D-enabled V1–V5 task-bound get remains byte-equivalent to its frozen manifest while the human snapshot advances;
17. all fixed-fixture V1–V5 Prompt/Result hashes, decoder rejection, Gate rubric, stale Result, report/evaluation, retry/cancel/recovery, and business-state tests remain unchanged; a new test proves D gate authorization uses frozen manifest/policy version without changing the V1–V5 rubric;
18. result-acceptance race pauses after preflight and mutates referenced lifecycle, version/access, verification, supersession, and policy watermark in separate subtests. Deterministic in-transaction locks/CAS reject revoked facts and commit no Result/domain/passport/edge/Decision/Event/Attempt transition;
19. unrelated policy-watermark advance reauthorizes all locked references and may accept while recording the newly reserved final acceptance watermark; an affected eligibility revision cannot;
20. result lock-order concurrency test submits references in opposite payload orders and proves UUID/kind lock normalization, no deadlock, one Result Artifact, and one terminal Attempt;
21. Result replay requires identical payload hash, manifest ID/hash, resolved version set, and lineage. Same request ID with changed lineage conflicts;
22. Chapter C `txOpResultAccept` fault matrix proves Result Artifact, compatibility projection, all outputs/passports/versions/links, Decision, Event, and terminal transition are all-or-none and converge after unknown commit;
23. human, historical task Agent, D-enabled task Agent, unbound Fleet Agent, evaluation subject, grader, projector, and cross-workspace principal each receive exactly the classified section 13 surface;
24. supersession/withdrawal increments passport eligibility and session watermark, excludes new context, blocks affected in-flight acceptance, preserves authorized audit, and never deletes history;
25. projection replay has stable IDs/hash; unknown kind/access degrades safely;
26. structural guard fails on any D direct `BeginTx`/`Commit` and passes only with `beginResearchTx`/`commitResearchTx` plus registered operation label.

Negative exposure tests always include a positive allowed artifact; an empty response is not evidence of isolation.

## 16. Phased D1–D3 rollout

### D1 — normalized foundation and honest backfill

- add passport, version, Result Artifact, context manifest/entry/omission, input reference, supersession, lifecycle, policy-state/mutation, and backfill-diagnostic tables;
- register canonicalization and entity-kind contracts;
- backfill only provable V1–V5 facts;
- write passports/versions transactionally for new artifacts while old reads remain unchanged;
- shadow-compare domain counts, hashes, producer facts, and legacy Result compatibility columns;
- no Agent authorization behavior changes yet.

Exit: every current canonical Research artifact type can be addressed by stable passport/version IDs, every projection/non-artifact family has the explicit section 13 policy, immutability and tenant constraints pass, and backfill reports no fabricated history.

### D2 — manifest-only context assembly and read authorization

- add `artifactContextModule` and clearance/purpose policy;
- precompute candidate representation bytes only as an optimization, then freeze each Attempt manifest under the locks/CAS in `CreateDispatchIntent`;
- render and persist Prompt/outbox bytes from that exact manifest inside the owning transaction;
- make D-enabled task-bound Agent `session get` use the exact manifest while historical runs retain live behavior;
- preserve the V1–V5 renderer and fixed-fixture hashes while applying the explicit section 9 frozen semantics;
- enforce evaluation subject/grader separation at context assembly;
- run shadow mode first: legacy whole-Run candidate set versus manifest authorized set, with rollout blocked by unexplained differences.

Exit: every D-enabled dispatched Attempt has one deterministic manifest; every platform-selected Research artifact byte supplied by Multica in its Prompt or task-bound Session response originates from an authorized frozen entry; no evaluation-private or unauthorized raw artifact reaches a subject. This claim does not cover arbitrary bytes independently introduced by a model, tool, external website, runtime, or user outside the platform-selected Research context.

### D3 — acceptance enforcement, lineage, and projection metadata

- make Result Artifact canonical and enforce manifest reference checks in preflight and `AcceptResult`;
- create output passports/versions/edges and propagate access taint atomically;
- enable lifecycle/supersession effects on new context selection;
- expose only bounded authorized passport metadata to Projection/API;
- complete failure-injection, race, security, legacy golden, and full Research PostgreSQL tests;
- update built-in Research Fleet Skill/source map and engineering-principle pointers in the implementation PR that changes behavior.

Exit: the Chapter D exit condition is met: the server can prove the origin, exact version, and authorization of every platform-selected Research artifact it supplies to an Agent and every canonical Research artifact output it accepts, and rejects unauthorized references before commit. It does not claim provenance for arbitrary model/tool inputs outside that boundary.

D1–D3 are rollout phases of one architecture, not reduced product variants. No phase may become a permanent alternate context or acceptance path for new or D-enabled Runs; only the explicit historical-live compatibility contract in section 9 remains.

## 17. Exact implementation file map

Expected implementation plan; line numbers must be refreshed against its execution base.

### Create

- `server/migrations/308_research_artifact_passport.up.sql` — normalized schema, constraints, triggers, indexes, and honest backfill.
- `server/migrations/308_research_artifact_passport.down.sql` — D-owned rollback preserving Attempt compatibility columns.
- `server/cmd/migrate/research_artifact_passport_migration_test.go` — fresh/up/down/up and backfill ledger tests.
- `server/internal/researchrun/artifact.go` — entity kinds, access profiles, lifecycle/eligibility revisions, passport/version/reference types, canonicalization registry.
- `server/internal/researchrun/artifact_policy.go` — access-lattice dominance, purpose, taint, representation, and deny-reason rules.
- `server/internal/researchrun/artifact_context.go` — context candidate traversal, deterministic ordering/budgeting, manifest construction, and view projection.
- `server/internal/researchrun/postgres_artifact.go` — transaction-scoped scoped-FK persistence, policy watermark/CAS helpers, manifest/reference/supersession writes, and authorized readers.
- `server/internal/researchrun/artifact_test.go` — canonicalization, access matrix, taint, and ordering tests.
- `server/internal/researchrun/artifact_integration_test.go` — composite FKs, deferred class triggers, immutability, policy watermark, manifests, refs, supersession, backfill, lock order, CAS, and fault recovery.

### Modify

- `server/internal/researchrun/transaction.go` and `transaction_guard_test.go` — reuse merged Chapter C labels/runner and extend structural/fault coverage; no new direct boundary.
- `server/internal/researchrun/types.go` — bounded passport summaries, candidate/manifest identity, policy watermark, and authorized context types; no V1–V5 Result schema mutation.
- `server/internal/researchrun/postgres.go` — keep human/historical live snapshots explicit and route D-enabled task context through persisted manifests.
- `server/internal/researchrun/postgres_tasks.go` — extend `CreateDispatchIntent` under `txOpDispatchIntentCreate` to lock/CAS selected facts and atomically create Attempt, manifest, Prompt/outbox, and Event.
- `server/internal/researchrun/execution.go` — precompute candidate representations only; pass candidate facts to `CreateDispatchIntent` and perform no authoritative Prompt render.
- `server/internal/researchrun/prompt.go` — pure manifest-derived rendering called by `CreateDispatchIntent`; V1–V5 builder bodies and fixed-fixture hashes remain byte-identical.
- `server/internal/researchrun/result_acceptance.go` — non-authoritative preflight plus lineage-aware replay input.
- `server/internal/researchrun/postgres_result.go` — use `txOpResultAccept`, lock policy/manifest/references in canonical order, CAS eligibility, and write Result/output lineage atomically.
- `server/internal/researchrun/postgres_evidence.go` — access-aware artifact readers and safe representations.
- `server/internal/researchrun/canonical_state.go` — add a separate passport/lineage hash surface without redefining historical V1–V5 business-state golden meaning.
- `server/internal/researchrun/result_acceptance_test.go`, `server/internal/researchrun/execution_test.go`, `server/internal/researchrun/engine_test.go`, `server/internal/researchrun/legacy_result_golden_test.go`, `server/internal/researchrun/orchestrator_golden_test.go`, and `server/internal/researchrun/postgres_integration_test.go` — legacy equivalence and D enforcement tests.
- `server/internal/handler/research_run_http.go` — typed authorization errors and manifest-bound task result submission.
- `server/internal/handler/research.go` — principal-aware Session snapshot selection and bounded optional passport summaries.
- `server/internal/handler/agent_research.go` — require active task binding for task-context detail; prevent the Agent alias route from widening access.
- `server/internal/handler/research_run_adapter.go` — include manifest ID/hash in the frozen Inbox context without changing Prompt prose.
- `server/internal/handler/research_run_graph.go` — safe passport identity/provenance projection only; no raw/private metadata.
- `server/internal/handler/research_run_http_test.go`, `server/internal/handler/research_run_adapter_integration_test.go`, and `server/internal/handler/research_run_graph_test.go` — credential, context, and projection authorization tests.
- `server/internal/researcheval/types.go`, `server/internal/researcheval/runner.go`, `server/internal/researcheval/eval_test.go`, and `server/internal/researcheval/autonomy_grader_test.go` — persisted evaluation-private context contract while retaining `SubjectInput` isolation.
- `server/pkg/db/queries/research.sql`, generated `server/pkg/db/generated/research.sql.go`, and generated `server/pkg/db/generated/models.go` — authorized lookup/projection queries generated through `make sqlc`.
- `server/cmd/multica/cmd_research.go` — no new V1–V5 syntax; ensure task-bound `session get` and errors reflect manifest authorization.
- `server/internal/service/builtin_skills/multica-research-fleet/SKILL.md` and `server/internal/service/builtin_skills/multica-research-fleet/references/research-fleet-source-map.md` — update only when D behavior ships, not in this design PR.
- `packages/core/types/research.ts` and `packages/core/research/schemas.ts` — additive, fail-safe passport summaries; unknown kinds/access values degrade generically.
- `packages/core/research/schemas.test.ts` — malformed/unknown passport metadata response parsing.
- `packages/views/research/components/research-node-detail.tsx` and `packages/views/research/components/research-node-detail.test.tsx` — authorized passport summary display and no-private-projection regressions.
- `docs/engineering-principles.md`, `docs/superpowers/plans/2026-08-05-autonomous-research-system.md`, and `docs/superpowers/specs/2026-08-03-research-run-backend-design.md` — implementation evidence, Chapter D status, and authoritative pointer updates after production behavior exists.

## 18. Explicit no-scope-reduction statement for E–N

This design is only Chapter D's provenance and authorization substrate. It does not remove, defer as optional, collapse, emulate with Prompt prose, or replace any E–N requirement.

In particular, it does not treat passports as Inquiry Graph, Search/Corpus lineage, Screening Decisions, Integration or Assimilation, recursive Insight Derivation, Dispute/Deliberation, Exploration Portfolio, Divergence Pass, dynamic Team Formation, full Report/Evaluation lineage, Monitoring, Episode/Strategy evolution, complete Projection Snapshot/Delta/Slice, migration, system evaluation, or production acceptance. It creates stable identities, exact versions, authorized input manifests, and lineage edges those chapters must use.

Chapter E still freezes the complete V6 Plan/Result/Prompt/Gate schema once across Inquiry, Corpus, Integration, Dispute, Report, and Evaluation. Chapters F–M still implement their full normalized domain entities and behavior. Chapter N still registers every required node kind, performs historical V1–V5 projection without fabricated history, proves 10,000-node pagination/replay, runs all security/fault/system evaluations, and gates the production default switch with rollback.

No D1–D3 compatibility mechanism may become a reduced V6 path. V6 is not enabled by this design or by Chapter D implementation alone.
