# MultiCA Graph Memory Scope and Compatibility Design

- Date: 2026-08-17
- Revision: 2026-08-31 — adds the research graph as the fourth named scope, per `2026-08-31-research-graph-memory-unification-spec.zh-CN.md`. The prohibition on workspace-wide graphs is narrowed from "any workspace-wide graph" to "implicit workspace-wide fallback"; the explicitly named research scope is the sole exception.
- Status: Approved design; implementation pending
- Supersedes: the replacement and workspace-wide storage assumptions in `2026-08-14-graph-memory-reviewer-design.zh-CN.md`
- Keeps: the existing graph data model, hybrid retrieval, explore, judge/reward, versioning, and backtesting primitives unless this document narrows them

## 1. Current-state conclusion

Graph memory is experimental and is not a lossless replacement for legacy memory. The current branch has five implementation gaps and one deliberate non-goal:

| Capability | Current state | Gap or contract |
| --- | --- | --- |
| Runtime injection | Incompatible | A successful graph recall replaces the complete scoped legacy snapshot; a miss or error leaves that complete snapshot active, including legacy project/channel/daily. |
| Workspace isolation | P0 risk | Daemon and server can resolve a root-level `memory_graph` shared across workspaces. |
| Profile parameters | Not wired | `explore_agents` and `explore_max_rounds` are persisted but not sent to runtime recall. |
| Operations and governance | Missing | Graph lacks equivalent operational outcomes for manual runs, status, audit, and mode-correct UI. |
| Consolidation and version lifecycle | Partially wired | Graph consolidation requires a second environment switch while legacy curation remains independently scheduled. |
| Existing-memory migration | Intentional non-goal | `MigrateLegacyMEMORY` has no production caller and must remain outside Graph activation. |

The first two implementation gaps are not fixed in the current branch. A later implementation plan may treat them as externally delivered prerequisites only after targeted tests prove the replacement and fallback code paths are gone.

## 2. Approved product boundary

Graph mode uses a hybrid memory model:

- User memory remains legacy: `users/<member-id>/USER.md` and `RELATIONSHIP.md`.
- Agent memory remains legacy: `memory/MEMORY.md` and `memory/STATE.md`.
- Project, channel, and daily memory use graph storage only.
- Research knowledge (conclusions/insights/findings exported from research sessions) uses graph storage only, in the workspace research graph. It enters through the research exporter's direct version writes, never through channel staging, and requires workspace Graph mode.
- A graph miss or graph failure never falls back to legacy project, channel, daily, workspace, or team memory.
- A task with neither project nor channel scope does not use graph memory.
- Existing legacy project, channel, and daily files are not migrated or backfilled. The first Graph activation starts from empty graphs.

Graph remains behind an Experimental switch. This design does not approve Graph as the default memory mode.

## 3. Physical graph topology

Every `(workspace_id, project_id)` pair has one physical project graph. It is shared only by agents authorized to participate in that workspace project:

```text
<workspaces-root>/<workspace-id>/memory_graph/
├── projects/<project-id>/
├── channels/<channel-id>/
└── research/<workspace-id>/
```

`projects/<project-id>` is the canonical project graph. `channels/<channel-id>` exists only for a standalone channel residency. `research/<workspace-id>` is the workspace research graph: exactly one per workspace, owned by the workspace itself, accumulating research-session knowledge with the session as provenance rather than a physical partition.

**Implicit** root-level and workspace-wide graph **fallbacks** are forbidden. The research graph is the sole sanctioned workspace-level graph: it is a **named scope**, resolved explicitly for research export, maintenance, and federated recall — never selected as a fallback when a project or channel target is unresolved, and never used by a task that has neither project nor channel scope. Each graph root contains immutable identity metadata:

```text
workspace_id
kind: project | channel | research
owner_id: <project-id | channel-id | workspace-id>
```

A store must verify this identity before reading or writing. Identity mismatch fails closed.

Project graphs are shared by all participating agents. Agent identity is provenance, not a physical partition.

## 4. Channel routing registry and lineage

PostgreSQL is the routing authority. Two structures record the current write target and immutable history:

```text
graph_memory_channel_route
- workspace_id
- channel_id
- routing_mode: standalone | project_lineage
- current_graph_kind: project | channel
- current_graph_owner_id
- generation
- created_at
- updated_at

graph_memory_channel_lineage
- workspace_id
- channel_id
- generation
- graph_kind
- graph_owner_id
- valid_from
- valid_to
```

The resolver locks the channel and route rows in one transaction.

### 4.1 First graph use

- If the channel is unbound, it enters `standalone` mode and writes to its channel graph permanently.
- If the channel is project-bound, it enters `project_lineage` mode and writes to that project graph.

### 4.2 Later project binding

A standalone channel keeps its channel graph as the write target after a project is bound. Recall reads the current project graph plus the standalone channel graph. No history moves.

### 4.3 Project reassignment

For a project-lineage channel moved from Project A to Project B:

- Close the A lineage row.
- Append a B lineage row and make B the write target.
- Keep historical channel-scoped nodes in A.
- Recall may read A only with `visibility=channel` and an exact channel ID match.
- Recall must never expose Project A's project-visible nodes through the moved channel.

### 4.4 Project unbinding and stale tasks

If a project-lineage channel is unbound, append a channel-graph lineage generation and make that channel graph the write target while it is unbound. The route remains `project_lineage`; a later bind closes the channel-graph generation and opens the new project generation. This temporary channel generation does not acquire the permanent behavior of a channel whose first graph use was standalone.

The server's current channel binding and registry generation are authoritative. If a claimed task carries a stale project ID after a bind or unbind:

- Do not grant project-visible access from the stale task field.
- Resolve the current write target from the registry.
- Permit only exact-channel history that the current lineage authorizes.
- Record a stale-scope diagnostic and fail closed if the route cannot be reconciled deterministically.

The project-binding write path updates the registry eagerly. The resolver also repairs drift transactionally so nonstandard binding paths cannot leave stale routing.

## 5. Node scope and provenance

Nodes and staging segments add these fields:

```text
visibility: project | channel | research
channel_id: <uuid|null>
source_agent_ids: [<uuid>...]
source_channel_ids: [<uuid>...]
source_task_ids: [<uuid>...]
source_session_id: <uuid|null>   # research-exported nodes only
```

Rules:

- `visibility=channel` requires a channel ID.
- A standalone channel graph permits only exact-channel visibility.
- A project graph permits project and channel visibility.
- A channel-origin segment defaults to channel visibility.
- A project task without a channel defaults to project visibility.
- A research graph permits only `visibility=research` nodes (no channel ID); research provenance carries the exporting agent IDs and the source session ID. Research nodes never migrate into project/channel graphs and vice versa; merges are valid only within one physical graph.
- Provenance is monotonic: consolidation may merge source IDs but may not remove them.
- Stored edges may cross visibility scopes, but every retrieval and traversal step reapplies the caller's graph view. A visible edge cannot reveal a hidden node.

Promotion does not mutate the source node from channel to project visibility. Consolidation creates or updates a separate project-visible node and links it with `derived_from` or `evidence_for`. Other channels can read the promoted statement but cannot traverse into hidden source material.

## 6. Daily nodes

Daily identity is stable and graph-local:

```text
project graph:
daily:<agent-id>:<project-id>:<channel-id|none>:<YYYY-MM-DD>

standalone channel graph:
daily:<agent-id>:none:<channel-id>:<YYYY-MM-DD>
```

- There is at most one daily node per agent, graph scope, and date.
- The date uses the workspace memory-profile timezone; a missing or invalid timezone uses `memorycuration.DefaultTimezone` (`Asia/Shanghai`).
- Channel daily nodes are channel-visible; project-only daily nodes are project-visible.
- During the day, the memory agent merges concise outcomes, decisions, risks, and next actions.
- Each update creates a lightweight graph version; readers never observe in-place mutation.
- `DailyUpdater` is the seal owner. After local midnight plus a fixed ten-minute grace period, it seals the prior date through `GraphMutationCoordinator` using a compare-and-swap on `sealed_at` and the source watermark.
- Update and seal serialize under the same graph mutation lease. An update that loses the seal race must not reopen or mutate the sealed node.
- A source event arriving after seal is appended to the current open daily node with `late_for_date=<original-local-date>` and original event provenance; it never rewrites the sealed node.
- Daily nodes are never deleted.
- The research graph has no daily nodes: its temporal model is research-session provenance (`source_session_id`), not daily sealing.
- Routine recall applies recency filters, while explicit historical lookup and allowed graph traversal may access older daily nodes.
- A channel residency change creates a new daily identity in the new graph. Old daily nodes stay in the historical graph.

## 7. Server-authoritative graph data plane

A shared project graph cannot live independently on each agent machine. The server is the graph data-plane authority:

- Daemon does not open graph directories.
- Daemon calls an authenticated internal recall endpoint.
- Server validates the task, resolves registry targets, performs scoped retrieval/explore, and returns bounded injection content.
- Server owns ingest, query logs, judge write-back, daily updates, consolidation, version switching, and GC.
- Multiple server replicas require a shared graph volume. PostgreSQL coordinates mutations but does not replicate graph files.

The task or message projection carries effective profile values and graph context:

```text
GraphMemoryContext
- memory_type
- explore_agents
- explore_max_rounds
- write_target
- recall_targets[]
  - graph_kind: project | channel | research
  - graph_owner_id
  - access: project_and_channel | project_only | exact_channel | research
  - channel_id
  - lineage_generation
```

The server remains the authority even if this context is included for diagnostics. On every recall or ingest, it reloads or verifies the effective workspace profile, canonical task scope, current channel binding, route generation, project authorization, and graph identity. Incoming context is never accepted as authorization, path selection, profile override, or write-target selection; forged or stale context fails closed or is replaced only by a deterministic server-current resolution.

## 8. Scoped recall and runtime composition

The scoped recall matrix is:

| Task scope | Recall targets |
| --- | --- |
| Project only | Current project graph, project-visible nodes only; plus the workspace research graph (research-visible nodes) |
| Project + current project-lineage channel | Current project graph, project-visible plus exact current channel (historical graphs exact-channel only); plus the workspace research graph |
| Project + standalone channel | Current project graph project-visible only; standalone graph exact-channel; plus the workspace research graph |
| Channel only | Current route write-target graph plus historical lineage, exact-channel only; plus the workspace research graph |
| Research session (Director) | Workspace research graph; plus the session's bound project graph (project-visible nodes only, and only if the session creator participates in that project) |
| Neither project nor channel | No graph recall (the research graph is not a fallback for unscoped tasks) |

`ScopedRecallCoordinator` first runs filtered hybrid retrieval over all targets. It starts explore only for graphs with eligible hits. Explore tools enforce the same graph view for every expansion.

References are graph-qualified, for example:

```text
project:<project-id>/node:<node-id>
channel:<channel-id>/node:<node-id>
research:<workspace-id>/node:<node-id>
```

Current project and current channel results rank before historical lineage. The combined memory remains within the existing 16 KiB execution-memory budget; existing user/agent caps remain, graph uses the remaining budget, and historical graph results are trimmed first.

Graph-mode legacy assembly is an explicit source whitelist:

- Include user preferences and relationship files.
- Include agent `MEMORY.md` and `STATE.md`.
- Include server memories explicitly classified as user or agent.
- Exclude project, channel, daily, workspace, and team sources.
- Exclude legacy daily by source, even if its current scope label is `agent`.

Graph miss, Pi unavailability, registry failure, identity mismatch, or corruption does not fail the business task. The task proceeds with legacy user/agent only. It never falls back to legacy project/channel/daily.

## 9. Ingest, daily, consolidation, and concurrency

### 9.1 Ingest

The server loads the canonical task by `agent_run_id` and derives workspace, project, channel, agent, visibility, and lineage. It never trusts caller-provided scope.

Staging writes use a temporary file and atomic rename. A repeated segment ID is idempotent only when scope and provenance match; conflicting scope fails closed.

### 9.2 Daily and consolidation

Daily updates and consolidation operate on one physical graph at a time. Project graphs can create project or channel nodes. Standalone graphs can create only exact-channel nodes.

The research graph is an exception: it has no staging and no daily nodes. Knowledge enters only through the research exporter's direct version writes (terminal epistemic states, idempotent by node UUID + content hash). Its LLM treatment is a maintenance round - the same consolidation machinery with no staging to fold, operating on the bounded working set (usage/exploration/supervision signals plus neighborhoods plus recently imported research nodes and their similarity top-K). Maintenance applies merge/update/delete only to working-set nodes, under the same candidate gates and op-log audit as consolidation.

`merge_node` (added by the unification spec) is valid only within one physical graph: input nodes must exist in the same graph and share its scope. Cross-graph merges are structurally rejected.

Promotion is an explicit validated operation. Candidate backtests are scope-aware. Any cross-channel visibility leak is a hard candidate failure and cannot be offset by aggregate quality.

### 9.3 Mutation coordination

A `GraphMutationCoordinator` serializes daily updates, consolidation, version switching, and GC per graph using a PostgreSQL advisory lock keyed by graph identity.

Recall pins one graph version for the whole request and does not hold a write lock. Embedding cache files use content-hash names and atomic creation. Query and judge records are written server-side rather than appended by daemon processes.

## 10. Profile, scheduling, and governance parity

Workspace profile values are effective runtime inputs:

- `memory_type`
- `explore_agents`
- `explore_max_rounds`

Environment values are defaults only when no workspace profile exists.

Graph jobs are registered without a second process-level environment switch, but remain inert unless the workspace is in Graph mode and the scoped ingest/daily/consolidation acceptance gates have passed. Removing `MULTICA_GRAPH_CONSOLIDATION_ENABLED` must not activate an incomplete writer.

Legacy and Graph schedulers are scope-exclusive:

- Legacy workspaces keep current legacy curation.
- Graph workspaces do not run the existing scheduled or manual legacy L1–L4, self-review, or team-curation pipeline because those stages use legacy daily/project/channel evidence and can repopulate forbidden scopes.
- Legacy user/agent files remain readable, agent-writable, and portable-sync managed. Automated user/agent-only curation is not claimed by this design; introducing one requires a separate scope-safe contract that cannot write legacy project/channel/daily.
- Graph project/channel/daily operations cannot race with legacy operations over the same semantic scope.
- The graph scheduler rechecks workspace mode and scoped-feature readiness immediately before acquiring its mutation lease.

Graph governance must provide equivalent operational outcomes, not fake L1/L2/L3/L4 names:

- Manual consolidation trigger
- Run status and failure details
- Version and current-pointer status
- Query/judge/backtest audit
- Channel lineage inspection
- Health and storage diagnostics
- Retry semantics

Evolution Center renders mode-correct controls. In Graph mode it must not expose actionable legacy project/channel/daily curation. A legacy-only endpoint either has a documented graph counterpart or returns a stable not-applicable response.

## 11. Migration policy

Existing-memory migration is intentionally out of scope:

- Do not call `MigrateLegacyMEMORY` in production.
- Do not auto-backfill legacy files.
- First Graph activation starts with empty physical graphs.
- Preserve old files on disk, but never read them as Graph project/channel/daily fallback.
- Switching Graph to Legacy preserves graph data but stops graph recall and writes.
- Re-enabling Graph resumes the preserved graph.
- The UI and API require explicit administrator confirmation of the empty-start and no-fallback behavior.

The existing migration helper may remain as unused experimental code, but it is not part of the activation path or compatibility promise.

## 12. Error and security contracts

Stable error codes include:

```text
graph_scope_unresolved
graph_identity_mismatch
graph_store_unavailable
graph_recall_failed
graph_profile_invalid
graph_lineage_conflict
graph_mutation_busy
graph_version_corrupt
```

Graph target paths are built only from validated UUIDs and checked to remain under the canonical workspace graph root. Directory identity mismatch fails closed. Logs, metrics, and health events record graph identity and error code but not memory content or credentials.

A partially written candidate is never current. The current pointer changes only after schema, identity, scope, and backtest validation.

## 13. Experimental rollout gates

Graph stays Experimental until all gates pass.

### P0: required before Graph activation for any user workspace, including Experimental

Developer-only tests may use an explicit test harness, but no workspace profile may activate Graph until all P0 evidence is green.

1. Runtime composition keeps legacy user/agent and no longer replaces the full snapshot.
2. Root/workspace graph fallback is removed.
3. Project/channel physical isolation and immutable graph identity are enforced.
4. Registry and channel lineage are authoritative.
5. Cross-channel retrieval and traversal filters fail closed.
6. Server-side shared recall replaces daemon-local graph stores.
7. Graph failure never restores legacy project/channel/daily.

### P1: required before a broader controlled pilot

1. Profile explore parameters affect every recall.
2. Graph mode automatically activates graph jobs without a second environment switch.
3. Legacy scheduling is mode- and scope-aware.
4. Graph manual operation, status, audit, lineage, and health APIs exist.
5. Evolution Center is mode-correct.
6. Metrics and alerts cover profile drift, routing failures, store health, mutation contention, and failed consolidation.

No part of this document authorizes Graph as the default mode.

## 14. Acceptance tests

1. Workspace and project physical graphs are isolated.
2. Only agents authorized for the workspace project can read its project graph; participating agents share project-visible nodes with complete provenance.
3. Channel A cannot retrieve Channel B's channel-visible nodes.
4. Promotion exposes only the project statement, not hidden source nodes.
5. An initially unbound channel stays standalone through bind, unbind, and rebind transitions.
6. A Project A to B reassignment writes new segments to B and reads A only with exact-channel access.
7. A project-lineage Project A to unbound to Project B sequence uses a temporary channel generation, then writes to B without making that generation permanently standalone.
8. Repeated and concurrent route resolution produces one active generation.
9. A task carrying stale project scope after channel reassignment receives no stale project-visible access.
10. Forged or stale `GraphMemoryContext` cannot select a graph, expand authorization, or override effective profile values.
11. An unscoped task never creates or queries a graph.
12. Graph miss and all graph failures exclude legacy project/channel/daily.
13. Legacy user/agent remain available; legacy daily remains excluded.
14. Daily nodes are isolated by agent/scope/date, use the workspace timezone, seal immutably, and are never deleted.
15. Concurrent update/seal produces one sealed node; a late event is recorded in the current open daily with original-date provenance and never mutates the sealed node.
16. Profile values reach Explorer; environment values are defaults only.
17. Concurrent mutation switches one valid version; recall remains version-pinned.
18. No daemon or server path resolves root-level or workspace-wide graph fallback.
19. Graph mode does not execute existing legacy L1–L4, self-review, or team curation.
20. Graph jobs remain inert until scoped writer acceptance gates pass, even after the second environment registration gate is removed.
21. Legacy workspace behavior remains unchanged.
22. UI mode switching clearly states empty start and no fallback.
23. The research graph resolves only through explicit research-scope paths (export, maintenance, federated recall, Director recall); an unresolved project/channel target or an unscoped task never falls back to it.
24. Research graph identity (`kind=research`, `owner_id=workspace`) fails closed like other graphs; research nodes never appear in project/channel graphs and vice versa; cross-graph `merge_node` is rejected.
25. Channel and project recall federate the workspace research graph alongside their existing targets; results stay graph-qualified and research-visible nodes are never exposed as project/channel content.
26. Research Director recall reads the workspace research graph, and the bound project graph only when the session is project-bound and the creator participates in that project; the project graph is not read for unbound sessions.

## 15. Implementation sequencing

1. Run an external-prerequisite checkpoint for runtime composition and physical isolation. If either is not proven by targeted tests on the current branch, record it as blocking; remaining modules may be developed only behind a disabled Graph activation path.
2. Add registry schema, resolver, complete bind/unbind/rebind lineage transitions, and immutable graph identity metadata, with targeted tests.
3. Add scoped node/segment fields, authorization-aware graph views, and retrieval/traversal leak tests.
4. Move recall to the server, recompute profile and scope authority per request, and compose bounded runtime memory, with forged/stale-context tests.
5. Run the complete P0 evidence checkpoint. No user workspace, including an Experimental workspace, can activate Graph and no later work may be called rollout-complete until every P0 test is independently green.
6. Implement scoped ingest, daily update/seal behavior, consolidation, and mutation coordination while graph jobs remain inert; validate each writer before registration changes.
7. Wire profile parameters end to end and prove environment values are defaults only.
8. Make graph job registration independent of `MULTICA_GRAPH_CONSOLIDATION_ENABLED`, add readiness checks, and make legacy scheduling mode-aware only after step 6 is accepted.
9. Add governance APIs, status, audit, lineage, health, and mode-correct Evolution Center controls, with API and UI tests.
10. Run targeted checks after every step, concurrency checks after mutation work, integration checks after server recall and scheduling, and the full regression suite before any controlled pilot.
