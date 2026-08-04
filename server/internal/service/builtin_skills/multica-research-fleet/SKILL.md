---
name: multica-research-fleet
description: "Use when executing an assigned durable Research Run task or operating the sealed Research Fleet led by 罗纳尔多."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Research Fleet

罗纳尔多 leads the sealed Research Fleet. A current Research Run is a durable
server-owned task graph, evidence ledger, decision log, delivery gate, and
recovery loop. Chat is for user steering and visible progress; chat prose does
not advance a task or satisfy a delivery gate.

## Assigned Research Run task

If the prompt contains `## Durable Research Run task`, follow its task ID,
attempt ID, versions, objective, acceptance criteria, and result contract.

1. Read the current snapshot before work:

```bash
multica research session get <session-id> --output json
```

The snapshot's `run.contract`, `run.method`, `run.sources`,
`run.observations`, and `run.claims` are the canonical read model for contract
constraints, method, synthesis, verification, and audit. Source text is
represented by a bounded excerpt plus content hash; exact Observation quotes
were already checked against the immutable full snapshot at ingestion. V3 and
V4 non-plan tasks inherit the accepted Method for the current goal/plan
version. V4 also exposes the accepted Claim-level evidence standards.

2. Perform the assigned investigation according to `run.method`. Explore
beyond the first plausible answer. For V4, each Claim references an accepted
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
is rejected.

4. Do not call `graph-append`, `source-upsert`, `report-patch`,
`product-rounds/judgment`, or `stage-eval` for an assigned durable task. Those
legacy mutations are rejected for initialized runs. Do not claim completion in
chat before `task-result` succeeds.

## Result responsibilities

- `plan` / `replan`: required questions, an explicit decision question and
  method rationale, analysis methods, evidence requirements, inclusion and
  exclusion criteria, source and counterevidence strategies, stopping
  conditions, uncertainties, risks, and an acyclic dependency graph. V4 plans
  also define machine-checkable evidence standards for the planned Claim
  types: stable key, purpose, source traits, minimum independent sources,
  strength, directness, method fit, and counter-search requirement. Choose a
  method that fits the question; academic publication protocols apply only
  when the Research Contract requires them.
- `discover` / `deep_read`: source snapshots, exact observations, supported or
  disputed claims, and evidence-producing follow-up tasks where needed. A V4
  source declares evidence traits and each Claim declares its accepted evidence
  standard. A question-scoped result that increases coverage sets
  `answer_claim_key` to a Claim included in that result.
- `verify` / `counter_search`: independent corroboration, contradictory
  evidence, and explicit claim resolutions. Agreement without source evidence
  is not verification. Include the source, observation, claim, and evidence
  objects being verified in the result; stable content deduplicates against the
  ledger and upgrades verification state transactionally. V4 links score
  strength, directness, and method fit against the referenced standard.
- `synthesize`: only the `reporter` role. A structured report uses the full existing
  reader structure (outline, sections, citations, sources, gaps, conclusion),
  repeats every section and conclusion exactly in `content_md`, and links
  normalized Claim keys to section IDs with exact `anchor_quote` prose. Each
  linked section cites a stored source that verifiably supports that Claim. A
  V3/V4 report explains the applied Method, counterevidence, limitations,
  unresolved gaps, and decision consequence.
- `quality_gate` / `citation_audit`: independent evaluation of the latest report
  revision by a `validator` Agent other than the report author. Structured evaluations
  provide substantive findings for all seven score dimensions and enumerate
  every reviewed report Claim and section. Fail when any material claim is
  unsupported, stale, misquoted, omitted, hides unresolved contradiction, or
  departs from the accepted Method or, in V4, its evidence standards.

The server decides readiness, retries, timeouts, concurrency, replans,
diminishing information gain, and final delivery. Never manufacture a passing
evaluation to stop the run.

Every `required_capability` in a proposed task must exactly match an active
fleet role. When a real specialty is missing, the lead must hire it, optimize
its instructions, activate it, and only then submit or retry the task graph.

## Fleet administration

Only the lead may hire, optimize, activate, or archive members. These commands
remain available for an actual capability gap:

```bash
multica research hire --name "专利检索手" --role "patent_scout" \
  --instructions "..." --reason "缺少专利检索能力，现有来源无法验证权利要求"
multica research optimize <member-id> --instructions "..." --activate --reason "..."
multica research archive <member-id> --reason "..."
```

Fleet Agents never rewrite the authoritative user goal. User steering creates a
new goal and plan version; older results remain audit history and cannot satisfy
the current delivery gate.

Start with `references/playbooks/general.md`; domain adaptations live beside
it. See
`references/research-fleet-source-map.md` for source-traced interfaces.
