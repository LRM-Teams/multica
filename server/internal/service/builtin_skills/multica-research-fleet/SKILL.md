---
name: multica-research-fleet
description: "Use when executing an assigned durable Research Run task or operating the sealed Research Fleet led by Ronaldo."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Research Fleet

Ronaldo leads the sealed Research Fleet. A current Research Run is a durable
server-owned task graph, evidence ledger, decision log, delivery gate, and
recovery loop. Chat is for user steering and visible progress; chat prose does
not advance a task or satisfy a delivery gate.

## Assigned Research Run task

If the prompt contains `## Durable Research Run task`, follow its task ID,
attempt ID, versions, objective, acceptance criteria, and result contract.

1. Read the attempt-bound snapshot before work. Agent data-plane reads require
the dispatched Attempt ID so the server returns only the frozen manifest; an
unscoped live-session read is rejected:

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

2. Perform the assigned investigation according to `run.method`. Explore
beyond the first plausible answer. For V4/V5, each Claim references an accepted
`evidence_standard_key`; every Source Snapshot records evidence traits and every
Evidence Link records directness and method fit. Evaluate those fields against
the Claim, not a universal source hierarchy. Preserve retrieved source text in
each source snapshot. Every quoted observation must be an exact substring of
that snapshot. Execute required counter-search and record uncertainty.

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
authorized attempt; the stale result fails closed.

4. Do not call `graph-append`, `source-upsert`, `report-patch`,
`product-rounds/judgment`, or `stage-eval` for an assigned durable task. Those
legacy mutations are rejected for initialized runs. Do not claim completion in
chat before `task-result` succeeds.

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
  linked section cites a stored source that verifiably supports that Claim. A
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
