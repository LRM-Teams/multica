---
status: accepted
---

# Skill evolution ledger authority, transactions, and package boundaries

This ADR freezes the Gate G1 decisions for the skill-evolution milestone
(spec `docs/superpowers/specs/2026-09-02-graph-memory-layered-skill-graph-navigation-spec.zh-CN.md`
§12, plan `docs/superpowers/plans/2026-09-02-graph-memory-spreadsheet-skill-evolution-implementation-plan.zh-CN.md`).
It is based on the G0 writer/reader inventory taken on branch
`feat/graph-memory-skill-evolution-20260902` at `80b4dbf9b` (highest
migration `490_universal_dag_legacy_backfill`).

## Decision 1 — Source of truth per plane; reuse, not duplication

Pattern, SkillCandidate, AssertionManifest, EvaluationRun, Approval,
Deployment, and Rollback live in **new append-only DB ledger tables**
(migration slice 492–494). The existing daemon-reported
`evolution_unit_submission` / `shared_evolution_unit` pipeline stays
untouched: its trust model is free-form daemon self-reporting with an LLM
reviewer, while the new ledgers are contract-validated, hash-pinned, and
fail-closed. No new writer may read from or write into
`evolution_unit_submission`.

Skill artifact, binding, and grant authority is unchanged and terminal:
`skill`, `skill_file`, `agent_skill`, `skill.grant_level`, and
`skill_promotion` (migrations 008/137/261) remain the only tables whose
writes change what agents execute. The final activation transaction is the
sole bridge from an accepted candidate into those tables.

Graph `NodeRole=pattern|skill` nodes are scope-safe, versioned projections
of ledger records. They are never transactional authority for a candidate
decision, active artifact pointer, grant, or binding, and a Graph hit
grants nothing.

## Decision 2 — Artifact versioning reuses the proven immutable pattern

The new candidate-artifact tables copy the shape already proven by
`shared_evolution_unit_version` (migration 127): INSERT-only rows,
`UNIQUE(unit_id, version)`, an `expectedCurrentVersionID` CAS on the
current-version pointer, and row locks taken with `SELECT ... FOR UPDATE`
exactly as `EvolutionVersionService.lockSkillUnit` and
`ApplySkillVersionRollback` do today. We do not hang candidate artifacts
off `shared_evolution_unit` itself: a candidate is pre-promotion and must
not create shared-unit rows before approval.

The approval/deployment/rollback ledger copies the append-only audit shape
of `skill_promotion` (261): INSERT and SELECT only, with
`actor_type`/`actor_id` distinguishing member vs agent actors.

## Decision 3 — Final activation transaction, lock order, CAS, outbox

Final activation runs in a single DB transaction that re-validates, in
this order: source/ACL/retraction closure, candidate revision/status and
artifact/diff hashes, manifest + EvaluationRun + gate-policy hashes,
approval revision/hash, and binding/grant precondition. Any drift yields
`stale`, never an automatic rebase. The transaction then atomically writes
the candidate decision, the new artifact/version pointer, the promotion
audit row, and one outbox entry.

Lock order is fixed: workspace row → skill/current-version pointer
(`FOR UPDATE`) → candidate row → outbox insert. All writers in this
milestone take locks in that order.

Provider materialization and Graph projection converge through idempotent
outbox rows (the `graph_memory_projection_outbox` and DAG publish-outbox
patterns with lease + bounded retry), never inside the activation
transaction. Requests repeat-safe by `idempotency key + payload hash`:
same key and payload returns the recorded decision; same key with a
different payload is rejected.

## Decision 4 — Evolution key and single active run

The evolution key is `workspace_id + target_agent_id + task_type +
environment_major_version`. A partial unique index enforces at most one
active mutation run per key (active = non-terminal statuses). Runs are
curator-created with a pinned input set in the first milestone; scheduled
triggers stay out of scope until pattern-quality, cost, and contamination
metrics stabilize.

## Decision 5 — MemoryRef stays closed; SkillEvolutionRef stays internal

The public `MemoryRef` keeps exactly two kinds (`graph_node`,
`staging_atom`) and gains none in this milestone. Pattern/Candidate/
Manifest/EvaluationRun/Approval references use the internal
capability-scoped `skillevolution.SkillEvolutionRef`, which task-recall
APIs cannot resolve. A Pattern Graph projection is reachable through
`MemoryRef(kind=graph_node)`, and the resolver additionally checks node
role and caller purpose before returning content.

## Decision 6 — Pattern projection lifecycle and retraction closure

A pattern projection's identity is `pattern_id + revision` projected into
`NodeRole=pattern`; updates ship as new revisions, never in-place content
overwrites. Projections are written by the projection outbox, not by the
Consolidator's TTT path, so ledger and Graph never contend on one
transaction. Retraction treats pattern nodes as provenance consumers of
their trajectory sources: the existing `MemoryRetractionService`
fence + quarantine flow (source guard, retraction registry, quarantined
pending recompute) extends to them, and reads fail closed on fenced
sources. Deletion keeps at most a minimal audit hash/reason with no
recoverable body.

## Decision 7 — Evaluator package seam and spreadsheet adapter

`server/internal/skillevolution/` holds the plane-agnostic domain
contracts, state machines, gates, and orchestrator with no storage or
provider imports. `server/internal/skillevolution/spreadsheet/` holds the
spreadsheet domain adapter (manifest, canonical diff, assertions). No
XLSX/formula dependency is added before Gate G2 freezes the pinned
runtime, dependency allowlist, and sandbox envelope. Service-layer files
(`server/internal/service/skill_evolution*.go`) own ACL checks, cross-domain
coordination, and outbox reconciliation; handlers
(`server/internal/handler/skill_evolution*.go`) are capability-gated HTTP
only; SQL stays in `server/pkg/db/queries/skill_evolution.sql`.

## Decision 8 — Rollout, writer epochs, fail-closed compatibility

Every new writer and reader is gated by
`service.SkillEvolutionFeatureGates` (default off, fail-closed, dependency
chain normalized). New tables are additive and nullable so old images run
on the new schema; each migration ships `.up.sql/.down.sql` and passes
fresh/up/down/up. Ledger `down` migrations are for pre-enable environments
only; production rollback disables writers/readers and keeps audit rows.
Plain recall and search responses stay byte-stable while gates are off.
Known inventory gaps that later slices must close before their phases:
`task_message`, `interaction_dag_segment`, and `pi_provider_call` currently
have no retention sweep, and v1 graph retrieval is rank-then-filter (the
eligible-before-rank rework is Phase 1).
