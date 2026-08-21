# Research Run V6 storage contract

Status: target storage design frozen; no migration implemented.

Working-tree base inspected: `dev@d460f3ee0b91`. At documentation freeze,
`origin/dev@d0db8c57fc31` is 19 commits ahead and contains migration numbers through
386, including two files that use number 385. The working tree also has an
unfinished merge/rebase. Implementation must first resolve that state, update to
the then-current `origin/dev`, audit the final migration ledger, and allocate
fresh numbers. The symbolic `N` sequence below is authoritative; numeric examples
from the inspected tree are not reservations.

Authority: [`superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md)
and [`research-run-v6-contract.md`](research-run-v6-contract.md). Transport and
Report-origin behavior is fixed by
[`research-run-v6-http-contract.md`](research-run-v6-http-contract.md).

## 1. Storage boundary

PostgreSQL owns canonical identity, state, versions, summaries, provenance,
relationships, decisions, audit and recovery. Existing Artifact Passport,
Artifact Version, Context Manifest, Run Event and outbox mechanisms remain in
force.

Content and Decision objects that enter an Agent/Director Manifest or are cited
by another Research object receive Artifact Passports/Versions. Memberships,
leases, current pointers, page acknowledgments and Projection bookkeeping remain
operational rows; they do not receive fake Research Artifact identities. Frozen
Brief/catalog page bytes are bound by Context Manifest entries, page hashes and
the Artifact Versions of their inputs.

Large immutable Report package bytes use the project's existing durable object
storage Adapter. PostgreSQL stores the storage generation/key, byte hash, size,
MIME, package relationship and lifecycle. The published document is a compiled,
self-contained HTML blob; runtime rendering makes no subresource network request.
Losing the blob makes the Report technically unavailable and triggers repair; it
does not permit regenerating bytes under the old revision identity.

Source package resources may be stored separately for reproducibility, but the
published renderer never serves them as runtime dependencies. The validator
inlines verified scripts/styles and embeds verified images/fonts into the final
document before computing the document and package hashes.

`research_graph_node` and `research_graph_edge` remain V1–V5 compatibility
projections. No V6 write path may use them as canonical progress.

## 2. Existing tables retained

| Table family | V6 use |
| --- | --- |
| `research_session` | Run root, orchestrator version, Goal/state/event watermarks and lifecycle |
| `research_contract_revision` | immutable user-intent Goal revisions |
| `research_task` | typed Research task attached to a generic Work Item |
| `research_task_attempt` | V1–V5 only after V6 generic Attempt is added |
| `research_result_artifact` | strict submitted bytes and Artifact Passport for Result S |
| Source/Observation/Claim/Evidence | evidence facts and independence counting |
| `research_branch` | Branch identity and lifecycle |
| `research_insight` | stable Insight family identity |
| `research_integration_round` | accepted Integration identity |
| `research_insight_derivation` | compatibility source relationships; V6 rows also point to exact versions |
| `research_dispute*` | evidence disputes |
| `research_report` | stable Report revision envelope |
| `research_run_event` | committed event sequence |
| Artifact Passport/Version/Manifest tables | artifact class, version, access and frozen representations |

V1–V5 rows and constraints remain readable. New V6 columns are additive or use
conditional validation keyed by `research_session.orchestrator_version`.

## 3. Existing-table changes

### 3.1 `research_session`

| Change | Contract |
| --- | --- |
| `fleet_id` | Drop `NOT NULL`; legacy rows retain the FK, V6 rows leave it null |
| status check | Add `awaiting_director`; preserve all existing values |
| `director_state_version BIGINT NOT NULL DEFAULT 0` | CAS watermark for assignment/Cycle changes |
| `current_director_assignment_id UUID NULL` | Deferred scoped FK to active assignment |
| `v6_projection_version BIGINT NOT NULL DEFAULT 0` | Projection rebuild generation; not graph truth |

The current assignment pointer is a convenience pointer backed by the assignment
ledger. It cannot be updated without a matching assignment event in the same
transaction.

### 3.2 `research_task`

| Change | Contract |
| --- | --- |
| `kind` check | Add `custom`; existing V1–V5 values unchanged |
| `task_type TEXT NOT NULL DEFAULT ''` | Director-authored stable type for V6 |
| `task_schema_id TEXT NOT NULL DEFAULT ''` | Validator for `task_payload` |
| `task_payload JSONB NOT NULL DEFAULT '{}'` | kind-specific task configuration |
| `required_capabilities JSONB NOT NULL DEFAULT '[]'` | zero or more capabilities; no fixed role |
| `expected_result_schema_id TEXT NOT NULL DEFAULT ''` | second-stage Result validator |
| `work_item_id UUID NULL` | V6 Work Item owning execution |

For V6, `kind='custom'`, `task_type`, `task_schema_id`,
`expected_result_schema_id` and `work_item_id` are required. `plan_version`
remains populated with `1` as a legacy storage compatibility value and has no V6
planning semantics. Goal revision and Branch targets are authoritative.

### 3.3 `research_branch`

Add:

- `goal_version INTEGER NOT NULL` for V6 rows;
- `scope JSONB NOT NULL DEFAULT '{}'`;
- `state_version BIGINT NOT NULL DEFAULT 0`;
- `reason_code TEXT NOT NULL DEFAULT ''`;
- `reason_detail TEXT NOT NULL DEFAULT ''`;
- `created_by_director_cycle_id UUID NULL`;
- `created_by_attempt_id UUID NULL`;
- `current_xxl_version_id UUID NULL`.

Deferred validation requires termination reason when terminal, validates creator
provenance, and proves `current_xxl_version_id` is a fresh XXL bound to this
Branch. Multiple Branches may reference the same XXL version.

### 3.4 `research_insight` and `research_insight_derivation`

`research_insight` becomes the stable family/root row. Existing title/summary
remain legacy projection fields. Add `current_version_id UUID NULL` for V6.

`research_insight_derivation` adds exact `insight_version_id`,
`input_artifact_version_id`, `integration_round_id` and `input_tier`; legacy
kind/entity columns remain readable. V6 uniqueness is exact output version +
input Artifact Version.

### 3.5 `research_report`

Add:

- `status`: `draft | published | needs_research | needs_revision | technical_failure`;
- `parent_report_id` and `parent_revision`;
- `title`, `summary`, `plain_text`;
- `package_hash`, `document_content_hash`, `document_storage_key`,
  `document_storage_generation`, `document_byte_size`;
- `input_snapshot_hash` for the exact Branch-aware, deduplicated Report input set;
- `csp_script_hashes JSONB`, `csp_style_hashes JSONB`;
- `input_event_sequence`, `published_at`;
- `reviewed_by_director_assignment_id`.

`content_md` and `structured` remain for legacy reports. V6 requires the HTML
package fields and may store a Markdown fallback, but Web/Desktop prefer the
published HTML document.

### 3.6 `research_integration_round` and dispute compatibility

`research_integration_round` adds `work_item_attempt_id`, `goal_version`,
`branch_scope_hash`, `input_set_hash`, `mode=promotion|assimilation|xxl_merge`,
`status`, `discussion_id` and `output_insight_version_id`. These fields bind the
existing integration identity to the generic Work Item and exact versioned
inputs; no V6 Integration is inferred from prose or legacy graph rows.

Existing Dispute/Position/Deliberation rows add nullable V6 Discussion and Work
Item Attempt references. V1–V5 rows remain valid. Evidence conflict facts stay in
the Dispute tables; the generic Discussion stores participants, visible turns
and votes without duplicating those facts.

## 4. New tables

All tables include `id UUID PRIMARY KEY`, `workspace_id`, `session_id`,
`created_at`, and scoped uniqueness/FKs unless a row is a pure join table. Agent
attribution UUIDs are immutable audit values and are not nulled by Agent deletion.

### 4.1 Director control

#### `research_director_assignment`

| Column | Type / rule |
| --- | --- |
| `director_agent_id` | UUID, validated as same-workspace Agent when assigned |
| `generation` | integer >=1; unique per Run |
| `status` | `active | unavailable | replaced | ended` |
| `assigned_by_user_id` | user FK; only a user creates/replaces assignment |
| `reason` | non-empty text |
| `started_at`, `ended_at` | ended null only while active |

Unique partial index: one `active` assignment per Run.

#### `research_director_cycle`

| Column | Type / rule |
| --- | --- |
| `director_assignment_id`, `director_generation` | exact active identity |
| `work_item_id`, `work_item_attempt_id` | persistent Director execution identity |
| `trigger_from_sequence`, `trigger_through_sequence` | monotonic committed Event range |
| `brief_id`, `brief_hash` | frozen Director Brief identity; dispatch Manifest is stored on the Work Item Attempt |
| `model_session_ref` | opaque provider/session reference; no credential |
| `status` | `pending | running | applied | partially_rejected | failed | stale` |
| `proposal`, `proposal_hash` | strict Action Proposal and hash |
| `execution_result` | per-action outcomes |
| `reviewed_through_sequence` | durable watermark |
| `failure_class`, `diagnostics` | bounded strings |
| `started_at`, `completed_at` | lifecycle timestamps |

Unique `(session_id, director_assignment_id, trigger_through_sequence,
brief_hash)` prevents duplicate Cycles.

#### `research_director_brief_page`

| Column | Type / rule |
| --- | --- |
| `director_cycle_id` | owning Cycle |
| `page_kind` | `overview | research | control | terminal_summary` |
| `brief_id`, `brief_hash` | frozen ordered page-set identity |
| `page_key`, `ordinal` | stable pagination identity |
| `through_event_sequence` | page watermark |
| `content_bytes`, `content_hash` | exact model-visible representation/page hash |
| `review_request_id`, `reviewed_at`, `inherited_review_from_page_id` | explicit idempotent acknowledgment or same-hash/same-Director carry-forward |

Unique page key per Cycle; total bytes are limited by Context Compiler policy.

#### `research_steering_assessment`

| Column | Type / rule |
| --- | --- |
| `research_message_id` | exactly one Assessment per user message |
| `director_cycle_id` | deciding Cycle |
| `goal_version_before`, `goal_version_after` | after may equal before for no-op |
| `selected_refs`, `affected_refs` | exact IDs/versions |
| `assessment_kind` | `no_op | local_change | goal_revision | full_reassessment` |
| `interpretation`, `reason` | persisted Director explanation |
| `actions` | accepted action IDs |

Unique user message ID proves every message was assessed once.

### 4.2 Team and Work Items

#### `research_team_membership`

| Column | Type / rule |
| --- | --- |
| `agent_id` | immutable Agent attribution |
| `formation_decision_id`, `director_cycle_id` | creation provenance |
| `membership_generation` | increments when the same Agent rejoins |
| `mission_prompt`, `mission_hash`, `mission_revision` | exact Director mission |
| `model_config`, `tool_config`, `permission_config` | non-secret configuration |
| `state` | `idle | working | offline | retiring | archived | failed` |
| `joined_at`, `left_at`, `terminal_reason` | lifecycle |

One active membership per Run+Agent. A transaction locking the Run row counts
active memberships and rejects the 51st. At count 20–49, formation Decision must
contain an allowed justification class.

#### `research_work_item`

| Column | Type / rule |
| --- | --- |
| `kind` | `research | match | discussion | integration | director | report | review` |
| `target_kind`, `target_id` | typed domain object advanced by this item |
| `client_key`, `idempotency_key` | unique within current Goal/target identity |
| `goal_version`, `input_state_version`, `input_event_sequence` | frozen control versions |
| `created_by_director_cycle_id` | Director provenance for semantic work; null only for server recovery/control items |
| `assigned_agent_id` | nullable until assignment |
| `status` | `pending | ready | dispatching | running | awaiting_input | succeeded | failed | cancelled | stale` |
| `priority` | 0..1 |
| `max_attempts`, `attempt_count` | bounded retry policy |
| `lease_token`, `lease_expires_at` | scheduler ownership |
| `payload_schema_id`, `payload` | second-stage validated work configuration |
| `terminal_reason_code`, `terminal_reason_detail` | explicit ending |
| lifecycle timestamps | ready/start/complete/cancel |

Unique `(session_id, goal_version, idempotency_key)`. One active lease per row.
Director `work.create.v1.kind` is the semantic task kind (`deep_read`, `verify`,
and future task methods). It is retained as `payload.task_kind`; the persisted
Work Item scheduling kind is derived from `expected_result_schema_id`
(`research`, `discussion`, `integration`, or `report`) and must never store an
open-ended semantic task kind in the constrained `kind` column.

#### `research_work_item_attempt`

This is the single V6 execution Attempt table; existing
`research_task_attempt` remains V1–V5 only.

| Column | Type / rule |
| --- | --- |
| `work_item_id`, `attempt_number` | unique attempt identity |
| `assigned_agent_id`, `membership_id` | immutable execution attribution |
| `inbox_task_id`, `dispatch_key` | canonical runtime identity |
| `manifest_id`, `manifest_hash` | frozen Work Manifest |
| `status` | `dispatching | running | succeeded | failed | cancelled | lost` |
| `result_kind`, `result_entity_id`, `result_artifact_id` | generic domain outcome; Artifact ID is populated for Atomic Result |
| `result_hash`, `client_request_id` | canonical replay identity |
| `failure_class`, `diagnostics` | bounded failure facts |
| dispatch/start/result/complete/cancel timestamps | recovery |

Unique active attempt partial index per Work Item; unique dispatch key, Inbox
Task and client request ID. Unknown commit reconciliation checks Result Artifact
and Event before creating another Attempt.

#### `research_work_catalog_page`

`work_item_attempt_id`, `catalog_view=same_tier|higher_candidates`, authorized
tier/Branch scope, `through_event_sequence`, page key, ordinal, content hash,
next cursor and `reviewed_at`. Unique Attempt + pinned catalog + page key. Page
bytes may be rebuilt from canonical nodes, but the hash and review watermark are
durable so an interrupted Agent resumes without losing traversal progress.

### 4.3 Content graph

#### `research_result_node`

One-to-one with `research_result_artifact` and its accepted Artifact Version.

Columns: `result_artifact_id`, `artifact_version_id`, `work_item_attempt_id`,
`catalog_summary`, `brief_summary`, `objective`, `conclusion`, `content`, `scope`,
`uncertainties`, `conflicts`, `open_questions`, `conclusion_state`,
`integration_state`, `reason_code`, `reason_detail`, `content_hash`,
`accepted_at`.

Tier is always S and is not stored as a mutable field.

#### `research_insight_version`

| Column | Type / rule |
| --- | --- |
| `insight_id`, `revision` | stable family + immutable revision |
| `artifact_version_id` | Passport Version identity |
| `tier` | `M | L | XL | XXL` |
| content layers | same columns as Result Node |
| `scope`, uncertainty/conflict/open-question arrays | persisted, not generated on read |
| `status` | `accepted | challenged | refuted | invalid | superseded | terminal` |
| `integration_round_id`, `discussion_id` | producing provenance |
| `content_hash` | immutable bytes hash |
| `superseded_by_version_id` | nullable successor revision |

Unique `(insight_id, revision)`, Artifact Version and content hash identity.

#### `research_node_branch`

`node_artifact_version_id`, `branch_id`, `bound_by_decision_id`, `bound_at`.
Unique node-version + Branch. It expresses scope reuse, not Branch-local
absorption.

#### `research_node_absorption`

| Column | Type / rule |
| --- | --- |
| `input_artifact_version_id` | unique globally within Run |
| `successor_insight_version_id` | accepted output |
| `integration_round_id`, `discussion_id` | provenance |
| `relation` | `promotion | assimilation | xxl_merge` |
| `absorbed_at` | commit time |

Unique input Artifact Version is the single-successor constraint. Trigger rejects
cross-Run input/output and any cycle. Inputs are never deleted.

#### `research_branch_frontier`

`branch_id`, `node_artifact_version_id`, `tier`, `added_by_event_sequence`,
`removed_by_event_sequence`, `removal_reason`.

An active row has null removal sequence. Unique active Branch+node. This is a
transactionally maintained current index and must be rebuildable from accepted
nodes, Branch bindings and Absorptions.

#### `research_node_steward_assignment`

`node_artifact_version_id`, `agent_id`, `membership_id`, `generation`, `status`,
`assigned_by_decision_id`, `assigned_at`, `released_at`, `reason`.

Unique active Steward per fresh node version. Absorbed or terminal nodes cannot
retain active Stewardship.

#### `research_match_decision`

`candidate_set_hash`, exact `input_artifact_version_ids`, `goal_version`,
`branch_scope_hash`, `decision`, `reason_code`, `reason_detail`, `decided_by`,
`director_cycle_id`, `invalidated_at`, `invalidated_reason`.

Unique non-invalidated exact candidate/Goal/scope identity. Reasons:
`unrelated | no_semantic_gain | duplicate | blocked_by_scope |
insufficient_evidence`.

### 4.4 Discussion

#### `research_discussion`

`kind=match|promotion|assimilation|dispute`, `scope_hash`, `input_set_hash`,
`goal_version`, `branch_scope_hash`, `through_event_sequence`, `revision`,
`status=active|consensus_accept|consensus_reject|uncertain|escalated|stale_input|completed`,
`director_assignment_id`, `stale_reason`, timestamps.

Unique active exact scope/input/version identity.

#### `research_discussion_input`

Discussion, node Artifact Version, ordinal, tier, content hash. Immutable after
Discussion starts.

#### `research_discussion_participant`

Discussion, Agent, membership, Steward assignment, joined ordinal, state and
absence reason. Participant set is immutable for one Discussion revision except
the explicit Ronaldo escalation participant.

#### `research_discussion_turn`

Discussion revision, round, ordinal, Agent, `work_item_attempt_id`, Manifest ID/
hash, visible message, contribution JSON, evidence refs, payload hash and
timestamp. Unique client request ID; append-only.

#### `research_discussion_vote`

Discussion revision, Agent, vote=`accept|reject|uncertain`, reason, turn ID and
timestamp. The latest vote per participant/revision is current; old votes remain.

Existing Dispute/Position/Deliberation rows remain evidence-specific objects.
A dispute Discussion references them rather than duplicating evidence positions.

### 4.5 Report

#### `research_report_input`

Report ID/revision, Branch ID, node Artifact Version, input role
`branch_xxl|branch_maximum|unresolved_gap`, ordinal and content hash. Unique Report
revision + node version deduplicates shared XXL.

#### `research_report_resource`

Report ID/revision, resource UUID, path, role, MIME, byte size, content hash,
object-storage key/generation, upload status and created time. Path is normalized,
relative, traversal-free and unique per Report revision.

#### `research_report_review`

Report ID/revision, Director assignment/generation, Director Cycle, input state
version, decision=`published|needs_research|needs_revision|technical_failure`,
reason, `render_artifact_version_id`, bounded render diagnostics, follow-up Work
Item refs and timestamp. Append-only. One published review per Report revision.

### 4.6 Projection bookkeeping

Canonical entities already emit `research_run_event`. Add only bookkeeping that
cannot be derived cheaply:

- `research_projection_snapshot`: snapshot ID, Run, through sequence, generation,
  expiry, hash;
- `research_projection_slice`: snapshot, slice key, cursor, node/edge/density
  counts, payload hash and bytes/storage key;
- existing projection outbox/delta ledger extended with V6 payload kind.

Density bins and `collapsed_path` never receive Artifact Passports and never
enter canonical state.

## 5. Required indexes and guards

At minimum:

- one active Director per Run;
- one active membership per Run+Agent and active-membership count guard;
- due/claim index on Work Item `(status, lease_expires_at, priority desc)`;
- one active Work Attempt per Work Item;
- Result Artifact/Attempt one-to-one;
- Insight family+revision and current pointer;
- unique input Artifact Version in Absorption;
- active Branch Frontier by Branch+tier and reverse node lookup;
- one active Steward per fresh node;
- exact Match candidate/Goal/scope identity;
- one active Discussion per scope/input/revision;
- Discussion turn order and client request uniqueness;
- Report revision, status and latest published lookup;
- Report input reverse lookup and resource path uniqueness;
- Director Brief pages by Cycle/page kind/ordinal;
- Steering Assessment unique message ID;
- all workspace/session composite FKs and reverse indexes needed by cascades.

Database triggers or deferred constraint triggers enforce polymorphic same-Run
references, current XXL tier/binding, Absorption acyclicity, terminal reason,
Steward freshness, participant Steward identity, published Report input freshness
and Report resource completeness. Go validation is an early rejection path, not
a substitute.

## 6. Lock order

All transaction code uses deterministic UUID ordering after the Run root lock.

1. `research_session` Run row.
2. Director assignment / Goal revision rows when affected.
3. Branch rows sorted by UUID.
4. input Artifact Version and Absorption-slot rows sorted by UUID.
5. Work Item / Discussion / Integration / Report target row.
6. output entity rows.
7. Event sequence, outbox and projection bookkeeping.

Team creation locks the Run before counting membership. Integration locks every
input slot before output creation. Report publish locks the Run, Report revision,
Branch inputs and current versions before accepting the Review. No Adapter call
occurs while holding a database transaction.

## 7. Transaction operation registry

Implementation registers at least these stable operation labels with the
existing recovery matrix:

- `v6_director_assign`
- `v6_director_cycle_commit`
- `v6_steering_assessment_apply`
- `v6_team_membership_create`
- `v6_team_membership_archive`
- `v6_work_item_create`
- `v6_work_item_claim`
- `v6_work_item_settle`
- `v6_work_item_cancel`
- `v6_result_accept`
- `v6_match_decide`
- `v6_discussion_open`
- `v6_discussion_append_turn`
- `v6_discussion_close`
- `v6_integration_commit`
- `v6_steward_transfer`
- `v6_branch_change`
- `v6_report_draft_create`
- `v6_report_review`
- `v6_report_publish`
- `v6_projection_ack`

Every label needs successful commit, before-commit rollback/retry,
after-commit-unknown/reconcile, duplicate-same-payload and duplicate-different-
payload tests.

## 8. Migration slices

Let `N` be the first unallocated migration number after updating to the current
`origin/dev` and resolving duplicate-number policy. Each migration has up/down/up
tests and remains compatible with the previous application image.

1. `N+0_research_v6_director_control`: session compatibility, Director,
   Steering, Team and Brief tables.
2. `N+1_research_v6_work_items`: Work Item, V6 Attempt and Task extension.
3. `N+2_research_v6_content_versions`: Result Node, Insight Version, Branch
   binding/frontier, Steward and exact Derivation versioning.
4. `N+3_research_v6_absorption`: Absorption, Match Decision, DAG/single-successor
   guards and indexes.
5. `N+4_research_v6_discussion`: Discussion/input/participant/turn/vote and
   Dispute links.
6. `N+5_research_v6_report_package`: HTML Report fields, inputs, resources,
   reviews and package guards.
7. `N+6_research_v6_projection`: Snapshot/Slice/Delta bookkeeping.
8. `N+7_research_v6_artifact_classes`: Passport kinds, access profiles,
   relationship ledgers and backfill diagnostics for new content/Decision
   entities that enter Manifests or cross-object references.
9. `N+8_research_v6_activation_guards`: deferred cross-table guards, final
   indexes and activation evidence metadata.

Migrations create no V6 Run and do not change the default orchestrator. Backfill
only creates compatibility metadata that can be proven from existing rows; it
must not invent Director, Steward, Discussion, Absorption or Report history.

## 9. Retention and deletion

- Canonical Result, Insight Version, Absorption, Discussion Turn, Decision,
  Steering, Report revision and attribution rows are append-only.
- Terminal and absorbed nodes remain indefinitely unless the entire Workspace is
  deleted under existing Workspace deletion policy.
- Projection Slice payloads and expired deltas may be pruned after a newer
  reconstructible Snapshot exists.
- Report drafts and published blobs are permanent by default. A future explicit
  withdrawal creates lifecycle metadata and denies rendering; it does not reuse
  or rewrite the revision.
- Secrets, raw provider token streams and hidden model reasoning never enter
  these tables.
