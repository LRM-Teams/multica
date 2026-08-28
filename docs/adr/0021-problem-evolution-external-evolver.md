---
status: accepted
spec: docs/problem-evolution-two-mode-spec.zh-CN.md
---

# Problem evolution keeps its algorithm out of tree and behind a process contract

## Context

Multica is adding a solve-oriented self-evolution capability with two modes:
evolving the answer for one problem, and generating a task-specific harness for
one problem (JIT-inspired) with reward-only feedback. The evolution algorithm
already exists outside this repository and is still changing weekly.

Three repository facts shaped the decision:

- The `evolution_*` table and package namespace is already owned by the
  skill/memory evolution domain (`evolution_unit_submission`,
  `shared_evolution_unit`, `evolution_model_eval_run`,
  `server/internal/service/evolution.go`, `packages/views/evolution/`).
- The daemon is already a process launcher: every provider CLI in
  `server/pkg/agent/` is an `exec.CommandContext` subprocess, and memory
  curation already models "server-owned run intent, claimed over heartbeat,
  executed locally, structured result reported back".
- Untrusted execution has a home that is not the daemon: the sandbox node
  gateway (`sandbox_node` / `sandbox_job` with `exec`, connector
  `server/cmd/multica/cmd_sandboxd.go`) runs commands inside containers with
  per-instance limits and job tokens.

## Decision

1. Persistence, code, and routes use the `problem_evolution` /
   `problem-evolution` prefix. The existing `evolution_*` namespace is not
   reused.
2. The evolution algorithm is not ported to Go and does not live in this
   repository. It is invoked as an external executable with a fixed process
   contract: `input.json` in, NDJSON events on stdout, artifacts in a
   daemon-owned workdir, meaning carried by exit codes.
3. Responsibilities split three ways. The Go server owns run state, evaluator
   contract freezing, event `seq` allocation, `graph_version`, permissions,
   quota, and audit. A thin daemon capability (`problem_evolution_v1`) owns
   claiming, process lifecycle, event forwarding, and artifact upload. The
   external program owns the algorithm and never touches the database or the
   Multica HTTP API.
4. Hidden answers reach only the verifier. The evolver invokes evaluation
   through a command indirection declared in `input.json`, so answer material
   never enters the evolver's environment, arguments, or workdir.
5. User-supplied verifier code runs only on sandbox nodes, never on the daemon
   and never in the server process. The first phase ships built-in
   deterministic evaluators only.
6. The Research graph code is not refactored to serve this capability. Problem
   evolution builds its own thin `@xyflow/react` canvas; shared graph
   primitives are extracted only once a third consumer exists.

## Consequences

- The external algorithm repository and the Multica implementation can proceed
  in parallel; the process contract is the only coupling point.
- Replaying a run requires pinning the external program version; runs without a
  version identifier are reported as not exactly replayable.
- Reward-only feedback needs a bandwidth budget, not only a field allowlist:
  per-round exact pass counts are themselves a probing channel, so the default
  projection is bucketed.
- A hidden-answer secret subsystem does not exist yet (`agent_credential` and
  `server/internal/secretscoped/filter.go` are not one) and is a prerequisite
  for mode B rather than part of it.
