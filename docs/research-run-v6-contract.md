# Research Run V6 Ronaldo Director contract

Status: target contract frozen; user-facing V6 create is open. Omitted
`orchestrator_version` still defaults to V5.

Normative target schema:
[`contracts/research-run-v6-director.schema.json`](contracts/research-run-v6-director.schema.json),
SHA-256 `2ce8b8af85c9cec5e508fa1c6b01c6963d998899d09b99d33f8110aca3b59f88`.
Its `$id` is the final `research-run-v6.schema.json` identity.

The code-coupled [`contracts/research-run-v6.schema.json`](contracts/research-run-v6.schema.json)
and the Go-embedded schema are byte-identical to the normative target. ADR-0017
authorizes this in-place V6 replacement. V1–V5 remain immutable. Clients that
omit `orchestrator_version` still create V5. Explicit V6 + Director creates a V6
Run. `AssessV6Activation` is an audit of remaining evidence; it does not flip
that omitted-version default.

The product and development authority is
[`superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md).
This document fixes the machine-envelope and cross-object rules that are too
relational for JSON Schema alone.

Storage and implementation references:
[`research-run-v6-storage-contract.md`](research-run-v6-storage-contract.md),
[`research-run-v6-http-contract.md`](research-run-v6-http-contract.md) and
[`superpowers/plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md`](superpowers/plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md).

## Envelopes

The target schema has nine strict root envelopes. Every owned object rejects
unknown fields. Explicit `payload` and `scope` objects remain open only because
their named `payload_schema` or task schema receives a second validator.

| Envelope | Producer | Consumer | Purpose |
| --- | --- | --- | --- |
| `director_brief` | Context Compiler | Ronaldo | Frozen Research Brief + Control Brief for one Director Cycle |
| `director_action_proposal` | Ronaldo | Director Module | Versioned generic actions; no fixed team roles or task taxonomy |
| `work_manifest` | Context Compiler | assigned Agent | Frozen Goal, Branch, mission, protocol, authorized artifacts and expected schema |
| `atomic_result_submission` | Research Agent | Result Acceptance Module | Immutable Result S and Match candidates |
| `discussion_turn_submission` | participating Steward | Discussion Module | User-visible turn, structured contribution and vote |
| `integration_submission` | joining/integrating Agent | Knowledge Graph Module | Promotion, assimilation or XXL merge proposal |
| `report_package_submission` | Report Agent | Report Module | Immutable self-contained HTML package manifest and exact research inputs |
| `projection_snapshot` | Projection Module | Web/Desktop | One pinned, paginated graph Slice |
| `projection_delta` | Projection Module | Web/Desktop | Event-sequenced changes after a pinned Snapshot |

Examples are stored in [`research/fixtures/`](research/fixtures/):

- `director-brief-v6.example.json`
- `director-action-v6.example.json`
- `director-no-op-v6.example.json`
- `work-manifest-v6.example.json`
- `director-work-manifest-v6.example.json`（证明非 Research Task 的通用 Work Item 不要求 `task_id`）
- `atomic-result-v6.example.json`
- `discussion-turn-v6.example.json`
- `integration-v6.example.json`
- `report-package-v6.example.json`
- `projection-snapshot-v6.example.json`
- `projection-delta-v6.example.json`

## Cross-object invariants

### 1. Scope, identity and replay

Every referenced entity, version, Agent, Work Item and artifact belongs to the
same workspace and Run. A task-scoped credential can submit only for its bound
Attempt and Agent. `client_request_id` plus canonical payload hash is the replay
identity: same ID and canonical hash return the committed result; same ID with a
different canonical hash fails closed.

All writes carry expected state versions or frozen input hashes. Server-side
transactions repeat identity, scope and version checks even when an HTTP or
runtime Adapter already checked them.

#### Hash profile

All JSON hashes use SHA-256 over RFC 8785 JSON Canonicalization Scheme bytes and
are encoded as lowercase `sha256:<64 hex>`. For a self-hashed envelope, remove
only the declared self-hash field (`manifest_hash` or `content_hash`) before
canonicalization. `client_request_id` remains included, so a replay is
byte-semantic rather than a deduplication guess.

A Director Brief page hash canonicalizes that page envelope after removing
`brief_hash` and `page.page_hash`. `brief_hash` canonicalizes the ordered
`(page_key, page_hash)` manifest plus workspace, Run, Director generation, state
version, event watermark and Goal version. Every page in the Brief carries that
same set hash.

Set hashes (`input_set_hash`, candidate-set and Branch-scope hashes) canonicalize
an array sorted by `(kind, id, version_id, content_hash)`; duplicate identities
are rejected before hashing. A Projection hash canonicalizes the complete logical
Snapshot with nodes, edges and density bins sorted by stable ID, independent of
pagination. A Delta's previous/new hashes refer to the complete states before and
after its `event_sequence`.

Resource `content_hash` hashes the exact uploaded bytes. `package_hash` hashes a
JCS manifest containing `document_resource_id` and resources sorted by normalized
path, with each resource's path, MIME, byte size, content hash and role. The final
compiled HTML receives its own raw-byte `document_content_hash`; CSP script/style
hashes are computed from exact inline bytes and converted to CSP's base64 syntax
at response time. The Schema file hash shown above hashes the file's raw bytes,
not JCS.

### 2. Director identity and Brief

A Run has one active `research_director_assignment`. An Action Proposal is valid
only when assignment ID, generation, Brief ID/hash, Run state version and event
watermark still match. Only the user can replace the Director. The Director may
propose any Research action, but cannot replace itself or bypass server
mechanical invariants.

The Research Brief contains each Branch's fresh Frontier summaries and terminal
aggregate summaries, never absorbed-child full text or terminal-node detail.
Persisted `open_questions` from a fresh Result or Insight are carried into that
node's bounded Frontier `brief_summary`, so the next Director cycle can create or
reassign follow-up Work, create an Agent when capacity or capability requires it,
or record why the question does not justify more research. The Control Brief
contains current team, Work Item, Discussion, Dispute, Report, steering and
failure facts. A failed Work Item's bounded `summary` includes its
mission, attempt count and budget, latest Attempt state, failure class and
diagnostics, plus its terminal reason. Page review watermarks are durable; model
sessions are disposable.

For a `v6_result_node_accepted` event with non-empty `open_questions`, a covering
Director cycle counts as consuming the event only when its persisted Frontier
summary contains the open-question block. Reconciliation creates one fresh
repair cycle for historical Briefs that contain the Result node but omitted that
block.

One `director_brief` envelope is one bounded page. All pages share Brief ID/hash,
state version and event watermark. Ronaldo acknowledges a page only after
processing it. An Action Proposal is accepted only when its
`reviewed_page_count` equals the frozen page count and the durable acknowledgments
cover every page hash in the Brief manifest. The Context Compiler may carry an
acknowledgment forward only when page hash and Director generation are unchanged;
changed/new pages require a new acknowledgment.

### 3. Action Proposal execution

Each action has a stable ID, idempotency key, expected state version and named
payload schema. Dependencies form a DAG within the Proposal. The server first
validates the complete DAG and all mechanical preconditions, then executes each
transactional action in dependency order. A rejected prerequisite prevents its
dependents; it cannot silently execute a semantically incomplete suffix.

The action vocabulary describes server capabilities, not a predefined research
workflow. `create_task` and `create_work_item` accept Director-authored types and
second-stage payload Schemas, so adding a research method or Agent role does not
require a new orchestrator version. Unknown platform verbs still fail closed;
Ronaldo's semantic authority does not make unimplemented server operations real.

Atomic research dispatch has one exact mechanical mapping: the outer Action uses
`kind=create_work_item` and `payload_schema=work.create.v1`; its payload uses
`kind=research` and `expected_result_schema_id=atomic_result_submission`.
Names from any of those other layers are not Action-kind aliases, and the
Director must not discover the vocabulary through repeated rejected submissions.

The Director plans, staffs, assigns and integrates; it never executes atomic
research Work itself. Atomic Work uses `atomic_result_submission`, a non-empty
payload schema ID other than `no_op.v1`, and an exact non-empty
`payload.task_specific_schema`. A contract-rejected assignment with idle
run-scoped workers must be corrected and retried rather than followed by
`no_op`. A failed run-scoped Agent Work with no active Agent Work is likewise
not a valid `no_op` state: the Director must retry or reassign it.

Ronaldo cannot pause the whole Run. A failed Work Item is a recovery input: the
Director must retry it, reassign it, create replacement Work, or report the
failure to the user. Only the authenticated user Stop operation or the V6 release
maintenance control may move an active Run to `paused`. The frozen schema keeps
the `pause_run` shape token, but the Director execution authorization rejects it.
User Stop is reversible: active non-Director Work returns to `ready`, its current
Attempt is cancelled without consuming an attempt budget, and Resume may dispatch
it again. Delete archives the Run and its canonical facts; it never physically
deletes V6 Work, Result, Insight, evidence, Discussion or Report history.

When Ronaldo decides that no state change is useful, the Proposal contains one
`no_op` action with its reason and no semantic dependents; `no_op` cannot coexist
with another action. An empty or missing action list is invalid. While a Run is
active, `no_op` is also invalid when no non-Director member exists and no Agent
creation is pending, or when idle members remain unassigned after a rejected
Work contract, or when failed Agent Work remains and no Agent Work is active.

External Agent creation, Inbox dispatch, storage upload and notification use
durable outbox intents. Database facts commit before an Adapter call. Unknown
external outcomes reconcile by idempotency key.

### 4. Dynamic team

A new V6 Run starts with only its Director membership. Active membership count
is Run-scoped, includes the Director, and cannot exceed 50. Below 20, creation
needs no extra capacity justification beyond the Director Decision. At 20–49,
the Decision records a capability, parallelism or independence reason. Fifty
rejects creation mechanically.

One run-scoped Agent may own at most one active Work Item at a time. Dispatch
admits only an `idle` membership; `working` is not spare capacity. Independent
research dimensions therefore use distinct Agents instead of accumulating
multiple ready/running Work Items behind one Agent and presenting that queue as
parallel execution.

A standard V6 Run first creates at least three run-scoped Agents and at least
three child Branches under the root Branch, capped by `max_parallel_tasks`.
Staffing/Branch creation and Work creation happen in separate Director cycles
because Agent creation is asynchronous and generated Branch IDs are only
authoritative after commit. The next cycle dispatches at least three independent
atomic research directions. Each atomic Work binds to exactly one existing,
non-root child Branch and one distinct Agent. Each unresolved open question on a
frontier node requires its own follow-up Work and distinct Agent, up to the same
parallel cap. The server preflights the complete Proposal before applying any
action and rejects root-only or missing Branch references, duplicate/busy
assignees, understaffed first rounds and uncovered open questions. A rejected
Proposal is isolated to its Run and cannot block the global Director-Proposal
queue.

Agent archive is soft. Task, Attempt, Result, Discussion, Steward and Report
attribution cannot be deleted or nulled by later Agent lifecycle changes.

### 5. Work and Result acceptance

Research, Match, Discussion, Integration, Director, Report and Review are typed
domain objects advanced through one persistent Work Item execution envelope.
Lease expiry transfers execution ownership; it does not duplicate a committed
domain result.

Every Agent/Director submission repeats `work_item_id`, `attempt_id`,
`manifest_id` and `manifest_hash`; Agent-produced submissions also repeat the
assigned `agent_id`. The authenticated principal and stored Work Item remain
authoritative. A body field cannot grant authority, and any identity/hash
mismatch fails before domain mutation.

A Work Manifest binds immutable protocol version, Director-authored mission,
Goal version, Branch state versions, artifact versions, representations and
expected result schema. The Agent cannot cite platform-supplied material absent
from that Manifest. For Director-created Work, the persisted Work-to-Branch
associations are the scope authority; dispatch resolves those associations to
the current Branch versions and freezes them as `branch_refs`. The Agent copies
those references exactly and never substitutes the Run state version.

V6 dispatch uses `research-v6-context-v1`, distinct from the V1–V5 legacy
compatibility policy. It admits only current `registered | accepted` artifacts
with complete provenance; evaluation-private artifacts remain compartmented.
The policy version is bound into the Manifest, grant, hash and acceptance
verification.

For content-producing work, `catalog_access` authorizes a pinned, paginated Run-
wide catalog of every fresh nonterminal node at the Agent's current tier and
catalog summaries of higher candidates in the named Branches. It does not grant
full content. Selecting a candidate creates Match/Discussion work whose new
Manifest grants the exact brief/full representations required for comparison.
Every reviewed page and watermark is persisted so restart does not silently skip
or reread the catalog as if it were new.

An accepted Atomic Result creates one immutable Result S, three persisted content
layers, evidence relationships, Branch bindings, Result state and event. Match or
Integration failure happens after Result acceptance and cannot roll it back.

### 6. Tier computation

The server computes output tier; Agent output is a proposal.

- `S + S -> M`
- `M + M -> L`
- `L + L -> XL`
- `XL + XL -> XXL`
- `XXL + XXL -> XXL`

Promotion requires at least two fresh same-tier inputs. Lower-tier supplements
may affect content but cannot satisfy that count. Promotion cannot skip a tier.
Assimilation requires an existing higher node plus at least one related fresh
input and preserves the higher node's tier.

Every output stores catalog, brief and full content, semantic gain, exact input
versions, Branch bindings and the producing Discussion/Integration. Empty
restatement or absence of semantic gain records a Match Decision instead of an
Insight.

### 7. Single-successor Absorption

Each accepted content version has at most one direct canonical successor. The
Absorption table owns a unique input-version slot. An Integration transaction
locks all slots in deterministic UUID order, validates freshness and commits
the successor, Derivations, Absorptions, Frontier changes, Steward, Event and
outbox atomically.

The first concurrent Integration to commit wins. Later contenders become
`stale_input` and must restart from the committed successor. Absorbed nodes leave
the default Projection and every Frontier immediately, but their immutable rows,
Passport Versions and Derivations remain expandable forever.

An absorbed node never automatically returns. If later evidence refutes it,
dependent Insights become `challenged`; a new Review Work Item decides repair,
replacement or terminal handling.

### 8. Branch and XXL

A Branch Frontier is an antichain of fresh, unabsorbed nodes, not a single row.
One Branch may have several incomparable M/L/XL nodes but at most one current
valid XXL. One Insight, including XXL, may bind to multiple Branches. Shared
content and evidence are counted once by canonical identity.

Branch split, merge, parent change and terminal state require a Director action.
Text similarity, UI clustering and layout cannot mutate Branch structure.

When the Frontier contains at least two eligible fresh nodes at the same tier
and no Discussion/Integration is active, the next Proposal must start
convergence. It may not add unrelated atomic Work or `no_op` around the eligible
pair. Accepted Integration promotes S→M→L→XL→XXL; absorbed inputs remain
canonical history but leave the default visible Frontier.

### 9. Match and Discussion

A Match Decision is pinned to Goal version, Branch scope hash and exact node
versions. An unchanged combination is not compared again. Input, Goal or scope
changes invalidate the old Decision.

One exact scope/version combination has at most one active Discussion. Its
inputs, participants and event watermark are frozen. All relevant Stewards
participate in one multi-party Discussion. All accept permits Integration; all
reject records a Match Decision; mixed or uncertain votes escalate to Ronaldo.
Evidence conflict also opens a Dispute.

A Dispute has at least two evidence-linked Positions. Director adjudication is
principal-fenced to the identity version frozen at dispatch, assesses every
canonical Position exactly once, and cites a Claim or Source Snapshot from that
Attempt's sealed context for every assessment. Director authority alone is not
resolution evidence. Every Integration-created Position carries a closed
`conflict_basis`. Logical, unit, version and same-Source interpretation conflicts
include normalized facts that the server recomputes; an Agent label cannot
establish their kind. Scope, method and semantic candidates instead carry an
explicit reason and resolved Claim references. Position passports retain typed
lineage to all cited Claims and Source Snapshots.

On creation, each Position receives an independent verification Task; a method
conflict also receives a Methodologist Task, and every Dispute gets a
distinguishing-evidence search. Position authors are hard-excluded from routing.
Each review Attempt Manifest contains only the Run/Contract/Method foundation
plus the assigned Dispute, Position, Claims and cited evidence; unrelated
positions and private evaluation artifacts are frozen as omissions rather than
exposed to the reviewer.

Turns are persisted before the next round and are user-visible. Hidden
chain-of-thought, provider token streams, credentials and unbounded tool logs are
not Discussion content. Changed input makes the Discussion `stale_input` without
deleting previous turns.

The last joining Agent whose Integration is accepted becomes successor Steward.
Only immediate unavailability permits Ronaldo to assign a replacement.

### 10. Terminal records

Execution, conclusion, integration and termination are separate state axes.
`reason_code` is a stable UI/analytics enum; `reason_detail` is Director-authored
explanation. Terminal unabsorbed nodes remain permanently user-visible, but do
not enter matching, Discussion, promotion, Director research content or Report
inputs.

For failed Work projection, a missing Work terminal reason falls back to the
latest Attempt failure class and diagnostics. Platform failure classes normalize
to `resource_failure`, while the exact bounded class and diagnostic remain in
`reason_detail`; the UI must not replace them with a generic terminal-state label.

High-level termination cancels active descendant Work, Match, Discussion and
Integration. Completed descendants remain historical. Low unabsorbed failure
does not invalidate an accepted higher node.

### 11. Steering and Goal revision

Every user message creates one Steering Assessment, including no-op. The raw
message and selected node revisions are preserved. Ronaldo decides whether to
revise the Goal and which Branches, nodes, Work Items and Agents are affected;
the server does not infer semantics from keywords.

Applying an Assessment uses expected Goal and Branch versions. Historical
Assessments and Decisions are append-only. A later correction creates a new
Assessment; it does not rewrite the mistaken one.

### 12. Director failure

Offline, stopped, repeated failure or provider quota exhaustion moves the Run to
`awaiting_director` and emits a user notification. Accepted in-flight facts may
finish committing, but no new top-level adjudication, team formation or Report
publication proceeds. No automatic successor exists.

### 13. Report

A Report is an immutable Goal attachment, never a graph node. Its input set is
the current XXL of every Branch that has one and the maximal Frontier inputs of
Branches without XXL, deduplicated by node version. Material lower inputs cannot
be silently omitted; they must first be absorbed, excluded, terminated or listed
as unresolved gaps.

Report package upload creates a draft. Mechanical validation checks exact inputs,
resource hashes, self-containment, citations, size limits and sandbox policy.
Only Ronaldo can record `published`; `needs_research`, `needs_revision` and
`technical_failure` create follow-up work without deleting the draft.

The Report Work Item freezes a Branch-aware input snapshot and its hash. Package
submission repeats that hash and the deduplicated node versions. The server
rejects stale, missing, extra or role-mismatched inputs rather than letting the
Report Agent choose a more convenient subset.

Uploaded package resources are immutable build inputs. Validation compiles them
into one final HTML document with scripts/styles inline and images/fonts embedded
as data; the published page serves only that document and performs no subresource
request. The server derives storage keys from the bound upload session rather
than trusting an Agent-supplied arbitrary object key. Script and style hashes in
the submission must match the compiled bytes and the response CSP.

The HTML response and embedding iframe both enforce sandboxing. Script execution
is allowed; same-origin access, storage, forms, popups, downloads, top navigation,
workers, nested frames, external network and a generic parent bridge are not.

### 14. Projection

Projection is rebuilt from canonical tables and Events. Report never appears in
the graph node enum. Default Snapshot includes Goal, Branch Frontier M+, terminal
high nodes, active Work S, unabsorbed Result S, and no absorbed node. A terminal
research Work S that already produced a Result S is represented by one
non-canonical `collapsed_path`. Director cycle Work is an operational scheduler
record, not a research graph node: it is excluded from default Snapshots,
canonical projection pages, expanded Slices and Deltas. Director state remains
inspectable through Brief, chat and activity surfaces. Expansion reads one
Derivation layer at a time.

Work S branch scope comes from the assigned `research_v6_work_item_branch` rows,
not from a future Result. The presentation-only work-activity read model resolves
the latest Attempt, assigned Agent, Inbox Task and durable progress for a selected
Work S. That same read model reads a bounded process history directly from
persisted Runner Activity entries, filtered to the Attempt's exact Inbox Task
`started_at` / `completed_at` lifecycle window; dispatch time is not an
execution boundary. The existing `agent:activity` Query-cache
write path overlays new live entries. Matching `task:running`, progress and
terminal events refetch the selected activity read model, and the work Projection
revision includes the latest Attempt, Inbox Task lifecycle and durable progress
timestamps for reconnect recovery. Neither activity nor canvas coordinates
become canonical Research state.

`collapsed_path` and density bins are explicitly non-canonical. Snapshot pages
share one `snapshot_id + through_event_sequence`; Delta sequence is continuous.
All pages share one `projection_hash`. Each Delta binds the previous and new
projection hashes; duplicate Delta is idempotent, while a sequence or hash gap
forces Snapshot reload.

## Version and rollout rules

1. The target schema hash above is frozen by this documentation phase.
2. Implementation slice 0 replaced the old code-coupled V6 schema and its hash
   test in one commit before explicit user V6 create opened.
3. V1–V5 decoders reject all V6 envelopes and do not receive V6 optional fields.
4. Historical V1–V5 Run rows remain on their pinned orchestrator version.
5. Changing the omitted-version default to V6 applies only to new Runs and
   requires an exercised V5 rollback. Explicit V6 create is independently
   controlled by the workspace release policy.
6. After implementation slice 0 freezes the target under the canonical filename,
   an incompatible hash change requires V7.

## Activation evidence

`AssessV6Activation` is the audit of remaining evidence. It does not enable or
disable explicit user V6 create, and it must not flip the omitted-version default
to V6 until every item has a stable evidence ID and revision:

- canonical migrations and up/down/up recovery;
- strict schema and negative fixtures for all nine envelopes;
- Work Item dispatch/retry/cancel/reconcile;
- Director Brief bounds and session rotation;
- dynamic team 20/50 behavior and Director failure;
- promotion, assimilation, single-successor race and challenge repair;
- Discussion, Dispute and stale input;
- Steering Goal/Branch cascade;
- Snapshot/Slice/Delta and large-graph performance;
- Web/Desktop graph accessibility and semantic zoom;
- HTML Report package, CSP sandbox and Electron negative tests;
- V1–V5 prompt and behavior goldens;
- a rehearsed rollback to V5 for new-Run default selection.
