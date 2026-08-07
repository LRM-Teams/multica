# Research Artifact Passport and Data-Access Design

Status: approved for implementation planning; design only

Date: 2026-08-07

Scope: autonomous research plan Chapter D. This document does not implement production code, does not freeze the V6 Plan/Result/Prompt/Gate schema reserved for Chapter E, and does not change the default orchestrator version.

## 1. Decision

Use a normalized PostgreSQL artifact-passport model with four distinct concerns:

1. a stable passport identifies one canonical Research entity;
2. append-only versions bind an immutable schema version, canonical content hash, provenance, run versions, and access class to that entity;
3. a frozen context manifest records the exact artifact versions authorized for one Attempt;
4. typed input and supersession edges preserve derivation without copying domain content into the passport.

This is a class-table-inheritance design: for a domain row that already has a UUID, `research_artifact_passport.id` equals that canonical row ID. The domain row references the passport through its own UUID. A result currently embedded in `research_task_attempt.result` gains a first-class `research_result_artifact` row and a distinct UUID. Future E–N entities use the same convention from creation.

PostgreSQL remains the only canonical state store. No graph database, object-level ACL service, or second event store is introduced.

The user brief already selected this normalized architecture. No unresolved design decision requires user confirmation.

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

Cost: more joins and careful transaction ownership. This is accepted because context assembly and result acceptance are bounded backend operations, and PostgreSQL indexes can serve their access patterns without a second store.

## 4. Canonical identity and normalized schema

Names below are normative for implementation planning. The migration may use PostgreSQL domains or CHECK constraints rather than native enums so additive future values do not require blocking enum rewrites, but accepted values and fail-closed behavior remain as specified.

### 4.1 `research_artifact_passport`

One stable identity per canonical entity:

| Field | Contract |
| --- | --- |
| `id uuid primary key` | Canonical artifact ID. Equals the domain entity UUID for existing/future entity tables; Result Artifact receives its own UUID. |
| `workspace_id uuid not null` | Tenant boundary. |
| `session_id uuid not null` | Research Run boundary; current Run identity is the durable Research Session ID. |
| `entity_kind text not null` | Registered kind such as `task`, `attempt`, `result_artifact`, `source_snapshot`, `observation`, `claim`, `report_revision`, or `evaluation`; E–N adds its required kinds without changing the base model. |
| `current_version integer` | Nullable until the first verifiable version exists; thereafter it is a monotonically increasing query projection of the latest committed version. |
| `lifecycle_status text not null` | `registered | accepted | rejected | stale | superseded | withdrawn`. This is artifact admissibility, not a replacement for Task, Attempt, Claim, Dispute, or Report domain status. |
| `provenance_completeness text not null` | `complete | partial | unknown`; legacy backfill uses only what storage proves. |
| `source_created_at timestamptz` | Existing domain timestamp when one is stored. Null when unknown. |
| `registered_at timestamptz not null` | When the passport was created; for backfill this is migration time, not fabricated historical acceptance. |
| `accepted_at`, `superseded_at` | Write-once nullable facts, set only by the owning transaction. |

Required constraints:

- unique `(workspace_id, session_id, id)` for composite foreign keys;
- unique `(workspace_id, session_id, entity_kind, id)` for explicit kind identity;
- `current_version IS NULL` exactly when no verifiable version exists; otherwise it is at least 1 and names an existing version;
- `registered_at` records passport registration and may be later than proven historical source/acceptance times; when both acceptance and supersession are known, supersession cannot precede acceptance;
- a passport cannot move from a terminal status back to `accepted`;
- entity-kind registration is centralized in Go and constrained in SQL for all kinds implemented at that migration level.

The passport stores no Question text, source bytes, Claim prose, Report Markdown, rubric, or result body.

### 4.2 `research_artifact_version`

One append-only immutable metadata version:

| Field | Contract |
| --- | --- |
| `id uuid primary key` | Stable version reference used by manifests and edges. |
| `workspace_id`, `session_id`, `artifact_id` | Composite FK to the passport, preventing cross-tenant/session attachment. |
| `version integer not null` | Monotonic per passport; unique `(artifact_id, version)`. |
| `schema_name text not null` | Stable schema family, e.g. `research-task`, `research-result-v5`, `research-report-structured-v1`. |
| `schema_version integer not null` | Entity-content schema version, independent from orchestrator version. |
| `canonicalization_version text not null` | Initially `research-artifact-c14n-v1`. |
| `content_hash text not null` | `sha256:<64 lowercase hex>` over the registered kind's canonical immutable content. |
| `access_level text not null` | One of the four access profiles in section 6. |
| `goal_version`, `plan_version` | Exact Run versions when known; nullable only for artifacts for which they are not applicable or provably unknown. |
| `contract_revision_id` | Nullable FK to `research_contract_revision`; null means not applicable/unknown, never “current by implication.” |
| `strategy_version_id` | Nullable until Chapter M creates canonical Strategy Versions. D does not invent one for V1–V5. |
| `produced_by_task_id`, `produced_by_attempt_id` | Nullable exact provenance; use only proven references. |
| `produced_by_agent_id uuid` | Immutable attribution value, deliberately not deletion-cascaded through `agent`. |
| `model`, `provider`, `execution_adapter` | Frozen execution facts copied from the producing Attempt when known. |
| `hash_origin text not null` | `production | migration_recomputed | legacy_stored`; distinguishes production hashing from a backfill observation. |
| `created_at timestamptz not null` | Version registration time. |

An immutable trigger rejects update or delete of every version field. Corrections create a new version; they never edit a hash or provenance claim. `research_artifact_passport.current_version` advances with compare-and-set in the same transaction that inserts the version.

Canonicalization is per registered kind and excludes mutable lifecycle projections such as Task status or Claim verification status. A semantic content edit creates a new version; a pure lifecycle transition does not. Canonical JSON uses sorted object keys, exact array order where order is semantic, normalized UUID/RFC3339 forms, and no locale-dependent formatting. Source bytes are hashed before excerpting. The registry has test vectors for every kind, so two implementations cannot silently choose different hash inputs.

### 4.3 `research_result_artifact`

A first-class Result Artifact is required because Attempt and Result are distinct completion/projection entities:

- `id uuid primary key` and FK to a passport of kind `result_artifact`;
- `attempt_id uuid not null unique`;
- `orchestrator_version text not null` and `result_schema_version integer not null`;
- `result jsonb not null`, bounded by existing Run config;
- `client_request_id text not null` and the existing canonical result hash;
- `accepted_at timestamptz`, nullable only for a legacy stored Result whose acceptance time cannot be proven; new production Results require it.

The new row becomes the canonical Result body. During the schema rollback window, `research_task_attempt.result`, `result_hash`, and `client_request_id` remain a compatibility projection written in the same transaction and checked for equality. New code reads the Result Artifact. The compatibility columns are not a second authority and are not dropped in D; contract removal belongs to a later expand/contract migration after old-server rollback support expires.

### 4.4 `research_artifact_context_manifest` and entries

One manifest is frozen for each Attempt before its dispatch outbox is committed:

| Field | Contract |
| --- | --- |
| `id uuid primary key` | Context identity. The manifest itself receives a passport/version of kind `context_manifest`. |
| `attempt_id uuid not null unique` | One effective task context per Attempt. Retry creates a new Attempt and manifest. |
| `task_id`, `workspace_id`, `session_id` | Composite scope and query keys. |
| `purpose text not null` | `research_execution | evaluation_subject | evaluation_grader | projection`. |
| `policy_version text not null` | Initially `research-artifact-access-v1`. |
| `through_state_version bigint not null` | Canonical Run watermark used for selection. |
| `normal_clearance text not null` | `verified_only | redacted | raw`. |
| `evaluation_compartment boolean not null` | Separate capability; not implied by raw clearance. |
| `manifest_hash text not null` | Hash of ordered entry version IDs, representations, purpose, policy, and watermark. |
| `created_at timestamptz not null` | Frozen with Attempt. |

`research_artifact_context_entry` contains `manifest_id`, stable `ordinal`, `artifact_version_id`, `representation` (`metadata | excerpt | full`), `use_kind` (`direct_target | prerequisite | evidence | method | contract | evaluation_material`), and a bounded machine reason. Unique `(manifest_id, artifact_version_id, representation, use_kind)` prevents accidental duplicates. Composite foreign keys prevent cross-workspace/session entries.

Ordering is deterministic: direct target first, then relation/use kind, importance, canonical artifact kind, artifact ID, and version. Token-budget omissions are recorded in a separate bounded `research_artifact_context_omission` row containing candidate version ID and reason (`access_denied | stale | superseded | duplicate | token_budget | irrelevant`). It never stores denied content.

### 4.5 `research_artifact_input_reference`

The derivation graph references immutable versions, not mutable entities:

- `consumer_version_id` and `input_version_id`;
- `relation` in `consumed | derived_from | verifies | redacts | evaluates | summarizes | cites`;
- `manifest_id` when the input came from an Attempt context;
- `explicitly_used boolean`, false for context-available lineage and true when the Result explicitly names or structurally depends on the input;
- bounded `purpose` and deterministic `ordinal`;
- unique `(consumer_version_id, input_version_id, relation)`;
- self-reference forbidden;
- both versions must share workspace and session unless a future, separately authorized evaluation import contract is designed. D permits no cross-session reference.

The manifest's own artifact version has `consumed` edges to every entry. Accepted output artifacts have `derived_from` edges to the manifest version and direct edges for explicit references. Thus the server can prove both what was available and what the Result claimed to use without duplicating every available input edge onto every output.

### 4.6 `research_artifact_supersession` and lifecycle history

Supersession is many-to-many and must not be a nullable “previous ID” column:

- `successor_version_id`, `superseded_version_id`, `reason`, `decision_id`, `created_at`;
- unique pair and self-reference prohibition;
- same workspace/session enforcement;
- a cycle check in the owning transaction;
- old content/version remains immutable and readable to authorized audit paths.

`research_artifact_lifecycle_event` is append-only and records old/new status, actor, Decision, reason, and timestamp. The passport's `lifecycle_status` and timestamps are a transactionally maintained projection of this history. Domain statuses remain in their domain tables.

## 5. Entity coverage

D1 registers at minimum the current canonical entities:

- Research Task;
- Research Attempt;
- Result Artifact;
- Source Snapshot;
- Observation;
- Claim;
- Report Revision;
- quality/citation Evaluation represented by the relevant `research_decision`, plus legacy `research_stage_eval` as `legacy_stage_evaluation`;
- Context Manifest.

Question, Contract Revision, Method Decision, Evidence Link, Decision, and Run Event are registered in D when they can enter a Task context or provenance edge. Registering them now avoids a false claim that a Context Manifest covers the complete prompt while Contract/Method inputs remain outside it.

The registry is deliberately extensible for all final-plan entities: Search Plan, Query Execution, Source Candidate, Screening Decision, Hypothesis, Branch, Insight, Insight Derivation, Integration Round, Integration Contribution, Dispute, Position, Deliberation/Turn, Team Formation/Membership, Divergence Pass, Capability Observation, Monitoring Cycle, Episode, Strategy Version, Promotion Decision, and Evaluation Defect. Their domain schemas and state machines remain owned by E–N.

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

## 7. Context assembly authorization

Introduce one internal `artifactContextModule`; no Handler, Prompt builder, or CLI route may independently list Research tables for an Agent task.

For dispatch:

1. lock Run and Task using the existing CreateDispatchIntent transaction order;
2. compute the task principal, purpose, clearance, evaluation compartment, state watermark, and allowed graph roots;
3. select candidate passport versions by typed domain relationships, never by summary-text search;
4. apply workspace/session, lifecycle, freshness, verification, supersession, and access policy;
5. choose safe representation and deterministic order within token/item limits;
6. write the Context Manifest, entries, omissions, manifest passport/version, and input edges;
7. render a `RunSnapshot` view only from manifest entries;
8. build the immutable version-selected Prompt;
9. commit Attempt, manifest, frozen Prompt/outbox request, and Run Event atomically.

A failure in steps 2–8 creates no Attempt or outbox. A process crash after commit replays the same frozen request and manifest hash.

For `multica research session get`:

- a human workspace principal receives the existing product snapshot filtered through its human policy and safe representations;
- a task-bound Agent credential receives the exact active Attempt manifest view, not a fresh whole-Run query;
- an Agent credential without a valid task binding receives only an explicitly designed Fleet overview, never raw Task context;
- an evaluation subject cannot access the grader route or evaluation-private entries;
- an evaluation grader gets only the grader manifest, which is separate from the subject manifest.

Prompt builders remain pure renderers. They cannot query storage or widen a manifest. V1–V5 Prompt bytes remain unchanged; their broad “complete session” command resolves to the manifest-equivalent view described in section 9.

## 8. Result-acceptance authorization

Authorization is enforced twice for defense in depth, with the transaction check authoritative.

Before decoding/materialization, `resultAcceptanceModule` loads the Attempt manifest and checks credential/Attempt/Inbox binding. It resolves every existing-artifact reference in the versioned envelope into an artifact version:

- local Source/Observation/Claim keys created in the same envelope may reference each other according to the existing schema;
- report Claim/Source references, answer Claim references, verification reuse, and any canonical existing references must be present in the manifest or be a server-created artifact explicitly granted to that task;
- the submitted payload cannot name an entity outside workspace/session, a newer unmanifested version, a denied representation, or evaluation-private data;
- a content hash supplied in a future V6 reference must match the manifest version exactly.

Inside `PostgresStore.AcceptResult`, after locking Run/Task/Attempt and before any domain insert:

1. re-read and lock the manifest;
2. verify `manifest_hash`, Attempt, task, principal, state/version policy, and all resolved references;
3. validate output access-level taint and any redaction/verification transformation;
4. create the Result Artifact and passport/version;
5. materialize Source/Observation/Claim/Report/Evaluation rows with their passports/versions and input edges;
6. write lifecycle/supersession facts, Attempt compatibility projection, Decision, and Event;
7. commit all or none.

A reference that passed preflight but changed or was revoked before the transaction returns a typed conflict/forbidden result and writes no partial artifact. Idempotent replay requires the same `client_request_id`, canonical result hash, manifest ID/hash, and resolved input version set. Same request ID with different lineage conflicts.

D does not add an `input_refs` field to V1–V5 envelopes and does not freeze V6. For legacy versions, explicit references are inferred from their existing typed fields, and the server records the manifest as the complete available input. Chapter E may require explicit V6 input references using this model.

## 9. V1–V5 compatibility

The following are immutable compatibility requirements:

1. Existing orchestrator selection, Result decoders, validators, materialization semantics, Gate rules, report reader schema, and complete Prompt hashes do not change.
2. `taskPromptModule` receives a manifest-derived `RunSnapshot` with the same field ordering and content that the current whole-Run loader would expose to that V1–V5 task, subject only to denying data that was never valid for that historical protocol. Initial V1–V5 policy grants the same normal current-session ledger surface, so D creates no new behavior regression.
3. The task-bound `session get` route returns the same manifest-derived view used to build the Prompt. It cannot show a later or broader state than dispatch input.
4. Existing V1–V5 accepted Results are not re-parsed, re-gated, or reclassified under future rules.
5. The new Result Artifact row is canonical for new writes. Existing Attempt result columns remain an exact compatibility projection during the rollback window.
6. Old desktop/browser response fields remain readable. Passport summaries are additive and schema-parsed with defaults; raw passport bodies are not injected into existing response objects.
7. Runs with `run_initialized_at IS NULL` remain legacy execution. Their existing routes are not silently converted into durable Research Run semantics.
8. New D behavior is enabled only after shadow equivalence proves that each V1–V5 context and accepted Result resolves to the same domain artifacts. A mismatch fails rollout, not the historical Run.

Golden tests must continue to pin all V1–V5 Prompt SHA-256 values, Plan/Result canonical hashes, future-field rejection, Gate behavior, stale-result behavior, report/evaluation rules, and canonical business-state behavior.

## 10. Migration and backfill: no fabricated history

Migration is additive and restartable.

### 10.1 Facts that may be backfilled

- stable IDs, workspace, session, entity kind, current stored content, and stored timestamps;
- Task/Attempt/Report producer references that existing columns prove;
- Agent/model/provider/adapter from immutable Attempt fields where present;
- stored Result hash only when it matches the current stored Result under the historical canonicalizer;
- a recomputed hash of current stored content with `hash_origin=migration_recomputed`;
- goal/plan/orchestrator versions explicitly stored on the relevant rows.

### 10.2 Facts that must remain null/partial

- historical inputs that were not recorded;
- whether an old Agent actually read every artifact that happened to be visible;
- Strategy Version before Chapter M;
- historical redaction or verification transformations not represented in storage;
- an acceptance timestamp when only entity creation/update time exists;
- producer Attempt/Agent/model when no existing foreign key or immutable attribution proves it;
- Search Plan, Query, Screening, Dispute, Integration, or other E–N lineage that did not exist.

Backfilled passports use `lifecycle_status=registered`, `provenance_completeness=partial|unknown`, `registered_at` equal to migration time, and an exact `source_created_at` only when stored. They do not synthesize lifecycle events claiming prior acceptance. A version computed from current storage says `migration_recomputed`; it is an observation of migration-time state, not a claim that the same bytes existed at entity creation.

For old Attempts with stored Results, create one `research_result_artifact`; use stored `result_submitted_at` as source time when present, but do not call it acceptance time unless the Attempt is succeeded and existing invariants prove acceptance. Missing or malformed legacy payloads produce no Result Artifact or passport. A diagnostic backfill ledger row records the skipped Attempt and reason; migration continues, and D enforcement excludes the absent artifact from new task context rather than creating an orphan identity.

No historical Inquiry, Search, Dispute, Integration, Team, Divergence, Monitoring, Episode, or evaluation-private rows are invented. Chapter N must preserve the same rule in historical projection.

Down migration removes only D-owned tables, triggers, indexes, and additive references. It restores old code's ability to read Attempt compatibility columns. It does not attempt to “unaccept” Results or reconstruct history that never existed.

## 11. Status, supersession, and revocation

Artifact lifecycle and domain lifecycle are separate:

- a running Task can have an `accepted` passport while its domain status is `running`;
- a refuted Claim remains an accepted historical artifact whose domain status is `refuted`;
- a new Claim/Report/Insight version may supersede an old version without deleting it;
- access withdrawal can mark a passport `withdrawn`; derived artifacts are recomputed for taint/staleness and no longer enter new contexts;
- a superseded/stale artifact is excluded from ordinary context but available to an authorized audit purpose.

Supersession requires a typed reason and Decision. Claim equivalence/refinement remains a domain relationship in later chapters; it is not inferred from matching hashes. Content deduplication may point multiple discovery events to one version but never merges authorship or deletes provenance.

## 12. Transaction ownership

Only the Module that owns the business invariant writes passports:

- plan/result acceptance transaction: Task, Question/Method inputs, Result Artifact, Source Snapshot, Observation, Claim, Report, Evaluation passports and versions;
- `CreateDispatchIntent`: Attempt passport/version, Context Manifest, entries/omissions, input edges, frozen Prompt/outbox, and dispatch Event;
- verification/quality/citation acceptance: verification-derived versions and `verifies` edges;
- steering/replan/integration transactions: lifecycle, stale, and supersession facts for affected artifacts;
- Corpus, Inquiry, Integration, Dispute, Exploration, Capability, Monitoring, and Evolution transactions in E–N: their own domain row and passport/version in the same transaction;
- Projection: read-only consumption after commit.

There is no public artifact CRUD API. Internal helpers accept an existing `pgx.Tx`; they never begin or commit a transaction. A domain row cannot commit without its required passport/version once enforcement is enabled. Conversely, a passport for a new production artifact cannot commit without its matching domain row. Deferred constraint checks and per-kind integration tests enforce this class-table pairing.

## 13. Projection and API implications

Chapter D exposes safe metadata needed by Chapter N but does not replace the current graph:

- canonical Projection identity remains `(run_id, entity_kind, entity_id)`; `entity_id` is the passport ID;
- node detail may include schema/version, lifecycle, provenance completeness, safe hash prefix, producer IDs, access profile name, and input/supersession counts only after caller authorization;
- raw content hash, raw locator, denied input IDs, evaluation-private existence, and omission detail are not broadcast to unauthorized clients;
- `input_reference` and `supersession` can project as typed edges in N;
- repeated projection uses artifact version IDs and deterministic hashes, so replay cannot create duplicate nodes;
- unknown future entity kinds and access profiles render as generic/hidden rather than crashing an old client;
- existing `projectRunV2Graph` remains a compatibility projection until N delivers the full Snapshot/Delta contract. It must not become a second authority for passport state.

The existing Session snapshot gains optional, bounded passport summaries only when needed. A separate authorized artifact-detail endpoint is preferable to adding full passports to every list row.

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

## 15. Acceptance tests

D is complete only when all of the following are executable and passing:

1. fresh database up, down/up replay, and migration lint for `308_research_artifact_passport`;
2. backfill count reconciliation by entity kind, with zero invented E–N rows and explicit partial/unknown provenance counts;
3. stored legacy Result hash match versus `migration_recomputed` distinction, including malformed/missing legacy payloads;
4. version immutability: changing schema, hash, provenance, version, access, or Run-version fields fails at PostgreSQL;
5. entity/passport class pairing: missing passport, wrong kind, orphan passport, and mismatched workspace/session all fail; valid controls pass;
6. same-session input edge succeeds; self, cross-session, cross-workspace, missing-version, and supersession-cycle edges fail;
7. access matrix covers all normal clearances plus evaluation compartment, with positive controls for each permitted path;
8. raw input taints output raw; only an authorized redaction transform creates redacted; only verified inputs/operation create verified-only;
9. evaluated subject serialization and task context contain zero evaluation-private IDs, hashes, metadata, or content; authorized grader context receives the expected private version;
10. Context Manifest selection is deterministic under repeated execution and records access-denied, stale, duplicate, and token-budget omissions without denied content;
11. Attempt, manifest, entries, frozen Prompt/outbox, and Event commit atomically under injected failure before begin, before commit, successful commit, and ambiguous commit recovery;
12. result preflight and in-transaction recheck both reject an unmanifested, revoked, wrong-version, or cross-tenant reference before any domain output commits;
13. idempotent replay requires identical payload hash and manifest lineage; same request ID with changed input versions fails conflict;
14. accepted Result, Result Artifact, all produced domain rows, versions, edges, Decision, Event, and Attempt terminal transition commit atomically under the Chapter C commit-fault matrix;
15. all V1–V5 full Prompt hashes, Result golden hashes, new-field rejection, Gate, stale result, report, evaluation, retry/cancel/recovery, and business canonical-state tests remain unchanged;
16. a V1–V5 Prompt context and its task-bound `session get` response resolve to the same ordered manifest versions and preserve prior visible semantics;
17. human snapshot, task Agent snapshot, unbound Fleet Agent, evaluation subject, evaluation grader, projector, and cross-workspace principal each receive exactly their authorized surface;
18. superseding or withdrawing an input excludes it from new ordinary contexts, preserves authorized audit access, and marks/recomputes affected derived artifacts without deleting history;
19. passport-derived Projection replay has stable node/edge IDs and content hash, and an unknown future kind/access value degrades safely;
20. race tests prove one current version, one Attempt manifest, one Result Artifact per Attempt, and no duplicate input/supersession edge under concurrent workers.

Negative “must not expose” tests require positive controls proving the intended allowed artifact remains visible; an empty response is not a passing isolation test.

## 16. Phased D1–D3 rollout

### D1 — normalized foundation and honest backfill

- add passport, version, Result Artifact, context manifest/entry/omission, input reference, supersession, lifecycle, and backfill-diagnostic tables;
- register canonicalization and entity-kind contracts;
- backfill only provable V1–V5 facts;
- write passports/versions transactionally for new artifacts while old reads remain unchanged;
- shadow-compare domain counts, hashes, producer facts, and legacy Result compatibility columns;
- no Agent authorization behavior changes yet.

Exit: every current artifact type can be addressed by stable passport/version IDs, immutability and tenant constraints pass, and backfill reports no fabricated history.

### D2 — manifest-only context assembly and read authorization

- add `artifactContextModule` and clearance/purpose policy;
- freeze each Attempt manifest in `CreateDispatchIntent` and render Prompt snapshots only from it;
- make task-bound Agent `session get` use the exact manifest;
- preserve byte-exact V1–V5 Prompt rendering and equivalent ledger visibility;
- enforce evaluation subject/grader separation at context assembly;
- run shadow mode first: legacy whole-Run candidate set versus manifest authorized set, with rollout blocked by unexplained differences.

Exit: every dispatched Attempt has one deterministic manifest; all dynamic Research artifact bytes in Agent context originate from authorized entries; no evaluation-private or unauthorized raw artifact reaches a subject.

### D3 — acceptance enforcement, lineage, and projection metadata

- make Result Artifact canonical and enforce manifest reference checks in preflight and `AcceptResult`;
- create output passports/versions/edges and propagate access taint atomically;
- enable lifecycle/supersession effects on new context selection;
- expose only bounded authorized passport metadata to Projection/API;
- complete failure-injection, race, security, legacy golden, and full Research PostgreSQL tests;
- update built-in Research Fleet Skill/source map and engineering-principle pointers in the implementation PR that changes behavior.

Exit: the Chapter D exit condition is met: the server can prove every Agent input/output's origin, exact version, and authorization, and rejects unauthorized references before commit.

D1–D3 are rollout phases of one architecture, not reduced product variants. No phase may become a permanent alternate context or acceptance path.

## 17. Exact implementation file map

Expected implementation plan; line numbers must be refreshed against its execution base.

### Create

- `server/migrations/308_research_artifact_passport.up.sql` — normalized schema, constraints, triggers, indexes, and honest backfill.
- `server/migrations/308_research_artifact_passport.down.sql` — D-owned rollback preserving Attempt compatibility columns.
- `server/cmd/migrate/research_artifact_passport_migration_test.go` — fresh/up/down/up and backfill ledger tests.
- `server/internal/researchrun/artifact.go` — entity kinds, access profiles, lifecycle, passport/version/reference types, canonicalization registry.
- `server/internal/researchrun/artifact_policy.go` — access-lattice dominance, purpose, taint, representation, and deny-reason rules.
- `server/internal/researchrun/artifact_context.go` — context candidate traversal, deterministic ordering/budgeting, manifest construction, and view projection.
- `server/internal/researchrun/postgres_artifact.go` — transaction-scoped passport/version/manifest/reference/supersession persistence and authorized readers.
- `server/internal/researchrun/artifact_test.go` — canonicalization, access matrix, taint, and ordering tests.
- `server/internal/researchrun/artifact_integration_test.go` — PostgreSQL immutability, tenant isolation, manifests, refs, supersession, backfill, concurrency, and fault recovery.

### Modify

- `server/internal/researchrun/types.go` — bounded passport summaries, manifest identity, and authorized context types; no V1–V5 contract mutation.
- `server/internal/researchrun/postgres.go` — replace broad `TaskContext` assembly with manifest-backed context and keep human/audit snapshot operations explicit.
- `server/internal/researchrun/postgres_tasks.go` — create Attempt passport and Context Manifest in the existing dispatch-intent transaction.
- `server/internal/researchrun/execution.go` — request a frozen authorized context before Prompt render; carry manifest hash in immutable dispatch semantics.
- `server/internal/researchrun/prompt.go` — consume manifest-derived snapshots only; V1–V5 builder bodies and hashes remain byte-identical.
- `server/internal/researchrun/result_acceptance.go` — preflight manifest/reference authorization and lineage-aware replay input.
- `server/internal/researchrun/postgres_result.go` — authoritative in-transaction authorization; Result Artifact and produced-artifact passport/version/edge writes.
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
