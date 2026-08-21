---
name: multica-research-fleet
description: "Use when executing an assigned durable Research Run task or operating the sealed Research Fleet led by Ronaldo."
user-invocable: false
allowed-tools: Bash(multica *), Bash(curl *)
---

# Multica Research Fleet

Ronaldo leads the sealed Research Fleet. A current Research Run is a durable
server-owned task graph, evidence ledger, decision log, delivery gate, and
recovery loop. Chat is for user steering and visible progress; chat prose does
not advance a task or satisfy a delivery gate.

## V6 Director assignments

Users may create a V6 Run by sending `orchestrator_version=research-run-v6` and
a Director. The homepage defaults to V6 and selects the first available Agent.
Clients that omit `orchestrator_version` still create V5. `AssessV6Activation`
remains an audit; it does not flip that omitted-version default. User chat on a
V6 Run wakes the current Director, not a workspace Fleet Lead. `PATCH
/api/research/v6/release` can close new V6 creates and pause existing V6 Runs.

When the dispatch contract is `research-run-v6`, the durable Work Manifest and
Director Brief are the complete authority for the current cycle. Never infer
canonical state from chat history, a previous model session, the canvas, or a
locally remembered team. A replacement Agent or Director must be able to resume
from PostgreSQL-backed Brief pages, catalog pages, Work Items, attempts, node
versions, discussions, steering assessments, reports, and committed events.

- Submit exactly the envelope named by `expected_result`; V6 has nine strict
  envelopes and rejects unknown fields, cross-envelope fields, stale versions,
  unscoped references, and payloads outside the frozen Manifest.
- A Work Item changes state only through its attempt/result transaction. Never
  directly promote, assimilate, revive, or connect graph nodes. Integration is
  a proposal until the server enforces promotion eligibility, locks the inputs,
  creates the single successor, and permanently records every absorbed node.
- Every user message is Steering input, including messages that produce a
  `no_op` assessment. Selected canvas references are immutable hints attached
  to that message; they are not graph mutations.
- The Director dynamically forms and replaces the team within the persisted
  membership and hard-cap rules. Model sessions are disposable execution
  resources, not durable team members or progress stores.
- A V6 Report is an immutable Goal attachment, never a graph node. Only the
  Director publication workflow may publish its verified package. Do not emit
  external URLs, credentials, application-origin dependencies, or bridge calls
  from report resources.

If any Brief/Manifest hash, revision, cursor, state version, assignment,
membership, capability, or expected envelope disagrees with the dispatch,
fail closed and let the durable recovery path issue a new attempt. Do not adapt
the payload into a legacy V1–V5 result. A platform-generated atomic Manifest
whose `branch_refs` is empty despite a persisted Work Branch scope is replaced
without spending the Agent's attempt budget.

### V6 executable loop

If the prompt contains `## Durable Research V6 Work Item`, use the exact Run,
Work Item, and Attempt IDs from that prompt. Read the frozen Manifest first:

```bash
multica research work-manifest <session-id> <work-item-id> <attempt-id> --output json
```

The Manifest's `expected_result_schema` names the only accepted root envelope.
Preserve its workspace, Run, Work Item, Attempt, Agent, Manifest, goal, state,
and event identities exactly. The Mission is an instruction inside that frozen
authority; it is not a substitute for reading the Manifest.

If the daemon's installed CLI predates these V6 commands, use the daemon-owned
credential proxy. Never read or print a token:

```bash
V6_API="http://127.0.0.1:${MULTICA_DAEMON_PORT}/api/agent/research/sessions/<session-id>/work-items/<work-item-id>/attempts/<attempt-id>"
V6_CURL=(curl -fsS \
  -H "X-Agent-ID: ${MULTICA_AGENT_ID}" \
  -H "X-Workspace-ID: ${MULTICA_WORKSPACE_ID}")
"${V6_CURL[@]}" "${V6_API}/manifest"
```

Use only the endpoint families authorized by the Manifest: Director work uses
GET `/director-brief` and POST `/director-brief-acks`; a Manifest containing
`catalog_access` authorizes GET `/catalog` and POST `/catalog-acks`; Report work
uses `/report-uploads`; all work submits through POST `/submission`. JSON writes
use `Content-Type: application/json`; a strict submission uses
`--data-binary @result.json`. This fallback has the same attempt and Agent
authorization as the CLI and exists because server CI/CD may deploy before a
local daemon binary is upgraded.

For a Director assignment, read every Brief page by following `next_cursor` and
acknowledge each page with the exact IDs and hashes returned in that page:

```bash
multica research director-brief <session-id> <work-item-id> <attempt-id> \
  [--cursor <cursor>] --output json
multica research director-brief-ack <session-id> <work-item-id> <attempt-id> \
  --client-request-id <uuid> --brief-id <brief-id> --brief-hash <brief-hash> \
  --page-key <page-key> --page-hash <page-hash> --output json
```

The acknowledgement object contains exactly `client_request_id`, `brief_id`,
`brief_hash`, `page_key`, and `page_hash`. Build the strict
`director_action_proposal` identity by copying workspace/Run/Work/Attempt and
Manifest identity from the Manifest; copy Director assignment/generation,
Brief identity, page count, state version, and event sequence from the Brief.
Each action must use a root-contract action kind and one payload schema frozen
under `manifest.task_specific_schema.payload_schemas`. Do not guess older
`research.*` schema names. Agent creation is asynchronous: never assign Work to
an Agent requested in the same proposal; wait for the joined event and next
Director cycle. Atomic Work uses `atomic_result_submission`, a non-empty
`payload_schema_id`, and the exact result validator in
`payload.task_specific_schema`.

When `catalog_access` is present, read the authorized view page by page and
acknowledge every page used by the result:

```bash
multica research work-catalog <session-id> <work-item-id> <attempt-id> \
  --view <same_tier|higher_candidates> [--cursor <cursor>] --output json
multica research work-catalog-ack <session-id> <work-item-id> <attempt-id> \
  --client-request-id <uuid> --page-key <page-key> --page-hash <page-hash> \
  --output json
```

An `atomic_result_submission` must copy the Manifest's `task_id` as well as its
Work/Attempt/Agent identity. The server creates that one-to-one Task provenance
record before dispatch. Copy `manifest.branch_refs` exactly, including every
Branch `state_version`; never replace it with `through_state_version` or another
Run watermark. Copy the exact single key under
`manifest.task_specific_schema.payload_schemas` into the submission's
`task_specific_schema`; never invent or rename a `research.*` schema ID. Keep
`content_layers.catalog_summary` at 512 characters or fewer. Root content-layer
`uncertainties`, `conflicts`, and `open_questions` are string arrays; fields
with the same names inside `task_specific_payload` follow the frozen task schema
and may be object arrays. Its `content_hash`
is SHA-256 over RFC 8785 JCS bytes after removing only `content_hash`; do not
hash pretty-printed file bytes.

Report work uploads each immutable resource before the package submission:

```bash
multica research report-upload <session-id> <work-item-id> <attempt-id> \
  --file <absolute-file> --path <package-path> --role <role> \
  --media-type <media-type> --output json
```

Submit the strict envelope only through the V6 endpoint:

```bash
multica research work-submit <session-id> <work-item-id> <attempt-id> \
  --file <absolute-path/result.json> --output json
```

`/submission` has no validation-only or dry-run mode. Never send a probe,
placeholder, or minimum test payload: any HTTP 200 is the formal durable
handoff and may permanently settle the Work Item. Submit only the inspected,
mission-complete result.

Retry the same submission with the same `client_request_id` and byte-equivalent
payload after a transport failure. Never send a V6 envelope through the legacy
`task-result` command.
That exact replay remains valid after the Attempt settles and returns the
original Submission outcome; a new request ID or changed content does not.

An HTTP 400 `research.v6.invalid_contract` response includes the bounded field
or hash validation reason. Correct that exact contract violation before the
next submission; the rejected envelope was not durably handed off. Do not
blindly resend an unchanged invalid file.

JSON strings must not contain `U+0000`/NUL. Remove that character from copied
source text before recomputing `content_hash` and resubmitting.

The submission boundary is asynchronous. Status `received` means the envelope
is durably handed off and the Agent should finish; a server reconciler later
marks it `accepted` or `rejected`. Do not keep the Inbox execution open waiting
for `accepted`, and do not create a second request ID after `received`.
The server may cancel the remaining Inbox execution after that durable result
settles; this is successful cleanup, not a failed Research result.

## Assigned Research Run task

If the prompt contains `## Durable Research Run task`, follow its task ID,
attempt ID, versions, objective, acceptance criteria, and result contract.

1. Read the attempt-bound snapshot before work. Agent data-plane reads require
the dispatched Attempt ID so the server returns only the frozen manifest. An
active Fleet member without an Attempt may read only a bounded session/Fleet
overview; it contains no goal, Run, artifact, message, report, hash, or grant data:

```bash
multica research session get <session-id> --attempt-id <attempt-id> --output json
```

The durable dispatch carries the same Manifest ID and hash in its Inbox
context. The attempt-bound snapshot returns them under `attempt_context`; a
mismatch means the execution is not bound to the context that was dispatched
and must fail closed rather than continue from a live session view.

The snapshot's `run.contract`, `run.method`, `run.sources`,
`run.observations`, and `run.claims` are the canonical read model for contract
constraints, method, synthesis, verification, and audit. Source text is
represented by a bounded excerpt plus content hash; exact Observation quotes
were already checked against the immutable full snapshot at ingestion. V3–V5
non-plan tasks inherit the accepted Method for the current goal/plan version.
V4/V5 also expose the accepted Claim-level evidence standards.
Contract, Method, Question, Task, and Attempt rows are the exact ordered values
frozen when this Attempt was dispatched. Later replans, retries, assignment
changes, runtime transitions, or terminal diagnostics do not rewrite this view.
The top-level `session` and `fleet` families are compatibility headers rebuilt
from the frozen Run and hashed principal roster. They intentionally omit live
Agent profiles, runtime configuration, routing fields, timestamps, and roster
changes made after this Attempt was dispatched.

The snapshot's Attempt list is also frozen at dispatch. Later Inbox attachment,
runtime heartbeat, cancellation, failure, or Result lifecycle changes are live
operational facts for the scheduler, but they do not rewrite this Attempt's
input context.

`artifact_projection` is also bound to the Manifest selection point. Later
passport lifecycle, provenance, version, or reference-count changes belong to
the human live view and do not rewrite this Attempt's projection or hash.

2. Perform the assigned investigation according to `run.method`. Explore
beyond the first plausible answer. For V4/V5, each Claim references an accepted
`evidence_standard_key`; every Source Snapshot records evidence traits and every
Evidence Link records directness and method fit. Evaluate those fields against
the Claim, not a universal source hierarchy. Evidence Links are separately
authorized manifest artifacts even though they appear nested under a Claim;
the snapshot omits any link the assigned Attempt cannot read. Preserve retrieved source text in
each source snapshot. Every quoted observation must be an exact substring of
that snapshot. Execute required counter-search and record uncertainty. Source
URLs must identify public resources and must never embed a username, password,
token, or other credential; authenticated retrieval uses the provider's
separate credential channel.

3. Write one JSON result with the fields permitted by the assignment prompt.
Use stable client keys and a globally unique `client_request_id`. Submit once:

```bash
multica research task-result <session-id> <task-id> <attempt-id> \
  --file /absolute/path/research-result.json
```

The same request ID and exact payload may be retried after a transport failure;
the server replays it idempotently. Reusing a request ID with different content
is rejected. Acceptance requires a dispatch manifest bound to the attempt;
results submitted without that manifest are rejected before commit. The
assigned Agent must still be an active member of the session Fleet, and every
manifest-bound policy grant must still be active at its exact revision,
principal, purpose, policy version, and compartment when the result is
accepted. A grant or membership change after dispatch therefore requires a new
authorized attempt; the stale result fails closed. The server also seals every
Manifest omission (candidate version, order, and exact policy reason) and
revalidates that omission digest before accepting a result.

4. Do not call `graph-append`, `source-upsert`, `report-patch`,
`product-rounds/judgment`, or `stage-eval` for an assigned durable task. Those
legacy mutations are rejected for initialized runs. Do not claim completion in
chat before `task-result` succeeds.

5. A task receives domain artifacts selected for its frozen manifest. Context
Manifest internals and V6 inquiry artifacts are never admitted through the
legacy V1–V5 compatibility policy.

## Result responsibilities

- `plan` / `replan`: required questions, an explicit decision question and
  method rationale, analysis methods, evidence requirements, inclusion and
  exclusion criteria, source and counterevidence strategies, stopping
  conditions, uncertainties, risks, and an acyclic dependency graph. V4/V5 plans
  also define machine-checkable evidence standards for the planned Claim
  types: stable key, purpose, source traits, minimum independent sources,
  strength, directness, method fit, and counter-search requirement. Choose a
  method that fits the question; academic publication protocols apply only
  when the Research Contract requires them. Every required Question has a
  question-bound `verify` task. The delivery synthesis is transitively
  downstream of every `discover`, `deep_read`, `verify`, and `counter_search`
  task; both audits depend on that delivery synthesis, so unfinished evidence
  work cannot reach report delivery.
  Delivery tasks are part of this validated plan graph; later evidence results
  must not introduce synthesis or audits as proposed follow-up tasks. Every new
  required follow-up Question includes a question-bound `verify`; dynamic
  evidence and `replan` work blocks pending delivery.
- `discover` / `deep_read`: source snapshots, exact observations, supported or
  disputed claims, and evidence-producing follow-up tasks where needed. A V4/V5
  source declares evidence traits and each Claim declares its accepted evidence
  standard. A question-scoped result that increases coverage sets
  `answer_claim_key` to a Claim included in that result.
  When a task screens retrieval candidates, preserve the exact versioned
  inclusion/exclusion criterion IDs, reviewer identity and time, substantive
  reason, and inspectable facts with locators. Accepted candidates must match an
  inclusion criterion and no exclusion criterion. Excluded candidates must
  match an exclusion criterion. Duplicates point to a different canonical
  candidate and carry its canonical URL or SHA-256 content hash; prose-only
  similarity is not duplicate evidence.
- `verify` / `counter_search`: independent corroboration, contradictory
  evidence, and explicit claim resolutions. Agreement without source evidence
  is not verification. Include the source, observation, claim, and evidence
  objects being verified in the result; stable content deduplicates against the
  ledger and upgrades verification state transactionally. V4/V5 links score
  strength, directness, and method fit against the referenced standard.
- `synthesize`: only the `reporter` role. A structured report uses the full existing
  reader structure (outline, sections, citations, sources, gaps, conclusion),
  repeats every section and conclusion exactly in `content_md`, and links
  normalized Claim keys to section IDs with exact `anchor_quote` prose. Each
  structured source ID must name a stored Source in the same Research session;
  every linked section cites one of those sources and it verifiably supports that Claim. A
  V3–V5 report explains the applied Method, counterevidence, limitations,
  unresolved gaps, and decision consequence.
- `quality_gate` / `citation_audit`: independent evaluation of the latest report
  revision by a `validator` Agent other than the report author. Structured evaluations
  provide substantive findings for all seven score dimensions and enumerate
  every reviewed report Claim and section. Fail when any material claim is
  unsupported, stale, misquoted, omitted, hides unresolved contradiction, or
  departs from the accepted Method or, in V4/V5, its evidence standards. V5
  emits stable structured defects with dimension, blocking/advisory severity,
  problem, required change, and existing target Claim/section keys. Every
  below-floor dimension has a blocking defect; a passing evaluation has no
  defects.

Failed quality and citation Decisions remain executable feedback. Gate findings
carry bounded evaluation Decision, report, and reviewer IDs; failed dimensions
with scores and rationales; V5 structured defects; explicit findings; and
reviewed Claim/section keys into the revision task. The reporter repairs each
blocking defect against the named artifacts and satisfies its `required_change`.
It must not replace the feedback with a generic rewrite or discard already
accepted evidence.

The server decides readiness, retries, timeouts, concurrency, diminishing
information gain, remediation, replans, and final delivery. Remediation is
targeted: unanswered questions bind the task to a durable question ID, Claim
fitness defects route to verification, required adversarial checks route to
counter-search, and report defects route to synthesis. Replan is reserved for
an invalid method, scope, or task graph. Follow the assigned task and its
remediation acceptance criteria; do not turn a local evidence gap into an
unrequested method change. Never manufacture a passing evaluation to stop the
run. Information gain comes from server-observed evidence-graph changes:
verified answer coverage, verification, independent evidence, counterevidence,
resolution, and diminishing graph novelty. Do not inflate it by minting new
keys for duplicate content or by self-reporting coverage.

An Inbox delivery that expires before any worker claims it is terminal only for
that delivery. The server preserves the Research Task's bounded attempt budget
and re-resolves an available execution target; do not duplicate the Task or
change its method to recover from this delivery failure.

The same ownership applies when a runtime restarts or times out after claiming
the Inbox delivery. Generic Inbox auto-retry must not clone a delivery carrying
`research_dispatch_key`; wait for the Research Work lease/recovery loop to
settle the old Attempt and dispatch a new Attempt with a new key.

Every `required_capability` in a proposed task must exactly match an active
fleet role. When a real specialty is missing, the lead must hire it, optimize
its instructions, activate it, and only then submit or retry the task graph.

## Fleet administration

Only the lead may hire, optimize, activate, or archive members. These commands
remain available for an actual capability gap:

```bash
multica research hire --name "Patent Scout" --role "patent_scout" \
  --instructions "..." --reason "Patent-search capability is needed to verify the claims."
multica research optimize <member-id> --instructions "..." --activate --reason "..."
multica research archive <member-id> --reason "..."
```

Fleet Agents never rewrite the authoritative user goal. User steering creates a
new goal and plan version; older results remain audit history and cannot satisfy
the current delivery gate.

Start with `references/playbooks/general.md`; domain adaptations live beside
it. See
`references/research-fleet-source-map.md` for source-traced interfaces.
