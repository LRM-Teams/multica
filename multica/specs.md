  Implementation Plan — Multi-Agent DAG RL Training in Multica (Cloud-only v1)
  
  [Consolidated: turn-granularity DAG · ForkableEnvironment/Daytona · sandbox model A]
  
  Problem Statement: Multica's collaborating agents form a graph (delegation/mention/completion). Train RL over it by (a) branching from a (task_id, seq) point with step-N
  fidelity, (b) judging success with a verifier agent instead of self-reported done, (c) backing a hybrid reward through the graph with explicit per-agent/per-step credit.
  Cloud-only, behind a ForkableEnvironment abstraction.
  
  Locked requirements:
  
  - Branch capture = pause the run at the decision point before snapshot.
  - Issue fork = activity-log reconstruction at seq, fallback to current values.
  - DAG node = one LLM output (turn); task_id/issue_id are grouping tags.
  - Environment = a save/restore-capable sandbox running daemon+agent together (model A); v1 provider = Daytona, behind ForkableEnvironment, swappable.
  - task_message.seq (written by the in-sandbox daemon) remains the branch boundary.
  
  Background (verified):
  
  - Node (tree_store.py) already = one turn (node_id, parent_node_id, episode_id, turn_idx, task_id, one outcome_reward). Sequential edges = parent_node_id. checkpoint.py
   serializes every field explicitly (deserialize uses .get → backward-compatible).
  - TreeAdvantageComputer: per-query GRPO, terminal-only; not graph-aware.
  - customized_grouped_workflow.py: select_branch_candidate filters on bool(node.branch_sandbox_id) (must accept cloud-env nodes);
  build_branch_task/_prepare_branch_task/_cleanup_branch are the Daytona+Supabase branch path; bind_sandbox_to_task/copy_messages_to_task/truncate_messages_before_turn
   already exist.
  - db_service/sandbox.py: clone_sandbox (Daytona clone/_experimental_fork/create_snapshot) + delete/concurrency semaphores — the v1 provider basis.
  - Multica: daemon (in-sandbox, model A) spawns the agent CLI, parses stdout, and POSTs task_message batches → ReportTaskMessages → CreateTaskMessage (this is the seq
  source). Agent reaches Multica HTTP API via MULTICA_TOKEN and the local daemon via MULTICA_DAEMON_PORT (multica repo checkout). execenv.Prepare is destructive (RemoveAll,
  task-id-keyed) → branch must use Reuse. Pi state lives in workdir/.pi/ + session JSONL at ~/.multica/pi-sessions/ (discarded on branch); Codex uses per-task codex-home/.
  issue.acceptance_criteria = verifier rubric; issue.parent_issue_id = delegation; activity_log/ListActivitiesForIssue = entity reconstruction. Existing pause.py = global
  weight-update pause, not per-run → new decision-point hook needed.
  
  Architecture:
  
  flowchart TD
    subgraph Sandbox[Save/restore sandbox - model A]
      DM[daemon: execenv + spawn agent + stdout→task_message]
      AG[agent CLI: codex/pi, LLM via proxy gateway]
      DM --> AG
    end
    subgraph AReaL[AReaL Python]
      DAG[Turn-granularity DAG store]
      SEL[Branch-candidate selection]
      PAUSE[Per-run decision-point pause]
      ENV[ForkableEnvironment → Daytona provider]
      VER[Verifier agent]
      CREDIT[per-node + per-task_id credit]
      BACKUP[graph-aware hybrid backup + advantage]
    end
    subgraph Multica[Multica Go]
      TMSG[task_message seq ← daemon]
      FORK[ForkIssueSubtree + activity-log reconstruct]
      API[POST/DELETE /api/issues/:id/fork]
    end
    subgraph Bridge[db_bridge]
      SESS[rl_start/set_reward/end_session]
      BR[agent_start_branch]
    end
    AG --> TMSG
    AG --> SESS
    SEL --> PAUSE --> ENV
    SEL --> FORK
    ENV --> BR
    FORK --> BR
    VER --> CREDIT --> BACKUP
    SESS --> VER
    API --> FORK
  
  Branch = pause run at (task_id, seq) → ForkableEnvironment.snapshot(sandbox) (Daytona clone) → ForkIssueSubtree → agent_start_branch bound to forked sandbox + forked
  issue, replay messages ≤ seq, drop PriorSessionID; in the forked sandbox the daemon resumes via execenv.Reuse on the inherited workdir.
  
  Task Breakdown (TDD, incremental):
  
  Phase 0 — Abstraction + turn-DAG contract
  
  - Task 1: Turn-granularity DAG model (Python). Nodes = turns (keyed node_id, tagged task_id/issue_id/seq); edges typed sequential|delegation|mention|completion; traversal
  + group_by_task_id. Sequential from parent_node_id; cross-run edges supplied by callers (emitting tool-use turn → child run's first turn). Tests: 2-run graph, descendants
  cross run boundary, group reconstructs runs in seq order. Demo: print turn-descendants + per-task_id grouping.
  - Task 2: ForkableEnvironment + Daytona CloudSandboxProvider. snapshot/fork/restore/cleanup protocol; Daytona impl reusing clone_sandbox + semaphores; provider behind a
  factory (swappable). Sandbox = whole machine (daemon+agent). Tests: fake provider contract; mock Daytona (snapshot→id, fork→id, cleanup→delete). Demo:
  snapshot→fork→cleanup on a mock returns ids and tears down.
  
  Phase 1 — Multica issue fork (Go; follow multica/CLAUDE.md)
  
  - Task 3: Migration + sqlc. forked_from_issue_id, forked_at_seq, forked_at_task_id on issue; partial index; CreateForkedIssue/ListForkLineage/DeleteForkedSubtree. Tests:
  migration up/down, query round-trip. Demo: insert + query a forked row by lineage.
  - Task 4: ForkIssueSubtree + activity-log reconstruction. internal/service/issue_fork.go: deep-copy reachable subtree, cut append-only children/comments at seq,
  reconstruct mutable fields (status/title) from activity_log at seq, fallback to current. Transactional; first verify activity-log coverage and document gaps. Tests:
  comment cut at seq, reconstructed status, unlogged-field fallback. Demo: forked issue carries step-N status, not latest.
  - Task 5: Fork handlers. POST /api/issues/{id}/fork (task_id,seq) + DELETE. Thin transport over Task 4; same auth/workspace authz as existing issue routes — flag any
  unauthenticated path. Tests: create/delete/400/cross-workspace 403-404. Demo: curl fork → id; delete removes subtree.
  
  Phase 2 — Agentic Verifier (replaces Tasks 6-8 in the existing plan)
  
  - Task 1: {agent_run → session_id} map. Objective: expose a deterministic mapping from each DAG AgentRunNode (keyed by
  task_id/issue_id/node_id) to its RL session_id, sourced from the db_bridge rl_start_session records. Guidance: add a session_id field to
  AgentRunNode (backward-compatible default None) and a dag.session_map() helper; populate at rl_start_session time. Tests: 2-run DAG yields
  correct map; node without a session maps to None; round-trips through checkpoint serialize/deserialize. Demo: print {node_id: session_id} for a
  2-run graph.
  - Task 2: pi verifier slash-command extensions. Objective: implement two pi extension commands — /rl/set_reward (POST to gateway /rl/set_reward
   with {session_id, reward}) and /export_trajectories (POST to gateway /export_trajectories with {session_id}) — plus read-only access to
  Multica task_message transcripts. Guidance: TypeScript extension under the verifier agent's .pi/extensions/; gateway base URL + admin key from
  env; never route through areal/.... Tests (TS): command registered, builds correct request body, surfaces HTTP errors; mock gateway asserts
  payload. Demo: invoke /rl/set_reward session=S reward=0.8 against a mock gateway and see the recorded call.
  - Task 3: AgenticVerifier Python driver. Objective: a Python class that, given a finished task's DAG + session_map + acceptance_criteria,
  launches the pi verifier agent (fixed judge model), feeds it the per-agent transcripts, and lets it set per-session rewards. Returns a
  structured VerifierRun record (per-session reward + rationale) for logging/auditing. Guidance: subprocess/sandbox launch mirroring the existing
  agent-CLI dispatch; deterministic temp; safe fallback (log + neutral reward) on agent error. Tests: with a fake pi agent that emits canned
  /rl/set_reward calls, the driver records per-session rewards; error path yields neutral fallback without crashing. Demo: run the driver on a
  sample 2-agent task → per-session reward map printed.
  - Task 4: Harvest + replace constant reward. Objective: at task finalize, run AgenticVerifier, then have the training loop call
  /export_trajectories per session to pull reward-stamped trajectories; remove the constant set_reward(1.0) and delete the old
  CompositeVerifier/LLMJudgeVerifier/CreditAssigner path. Guidance: enforce reward-before-export ordering; idempotent harvest; structured logging
  of fork/verify counts. Tests: finalizer sets verifier rewards (not 1.0); export is called only after set_reward; export-before-reward
  raises/handled. Demo: a finished task produces trajectories whose recorded reward equals the verifier's per-agent verdict.
  
  Phase 3 — Generative Critic + GAE (replaces GRPO for DAG runs)
  
  - Task 5: Critic-agent observation builder. Objective: on sub-task-agent completion at (task_id, seq), read task_message rows ≤ seq and produce
  a filtered critic observation bound to the node (node_id, seq). Guidance: pure function build_critic_observation(messages, node) ->
  CriticObservation; the filter selects only critic-relevant fields (no leakage of the actor's full hidden context beyond what the critic should
  see). Tests: observation contains only filtered fields; cut at seq (no future messages); attaches correct node_id/seq. Demo: print the filtered
  observation for a sub-task completion node.
  - Task 6: Generative critic model (shared backbone + value head). Objective: a critic that shares the actor backbone and adds a value head,
  scored over the critic observation to emit V_node (and optionally a generative rationale). Guidance: reuse PPOCritic.compute_values infra;
  configure the critic engine to share/init from the actor backbone per [5]; expose compute_value(observation) -> float. Flag GPU-dependent tests
  as skipped when hardware is unavailable (per CLAUDE.md). Tests: value-head forward returns a scalar per observation; shared-backbone init loads
  actor weights; deterministic shape/wiring tests run CPU-only with a tiny model. Demo: critic returns V_node for a sample filtered observation.
  - Task 7: Per-node value store + per-task_id GAE. Objective: store V_node on each AgentRunNode; compute GAE over the per-task_id ordered node
  sequence using terminal verifier reward (entering at the terminal node) + per-node process signals as step rewards and V_node as the value
  baseline; produce a node-level advantage and return. Guidance: adapt the GAE recurrence from actor.py to node granularity (node sequence
  instead of token index); reuse discount/gae_lambda config. Tests: a hand-computed 3-node chain with known rewards + values yields expected
  per-node advantages/returns; single-node reduces to reward - value. Demo: print node advantages/returns for a 2-run graph.
  - Task 8: Broadcast node-advantage to actor tokens + critic returns. Objective: map each node-level advantage onto all actor tokens of that
  turn (via loss_mask), and emit per-node critic returns as the value-regression target. Guidance: produce the advantages/returns tensors in the
  shape PPOActor/PPOCritic expect; preserve existing reward scaling/normalization hooks. Tests: token broadcast covers exactly the turn's masked
  tokens; critic returns equal GAE returns at node granularity. Demo: a turn's tokens all carry the node's advantage; critic target equals the
  node return.
  - Task 9: Wire critic + GAE into the trainer; remove GRPO from DAG path. Objective: set advantage_mode=GAE for DAG runs, instantiate
  PPOTrainer.critic (the generative critic), run critic compute_values→GAE→actor update→critic ppo_update each step, and remove
  TreeAdvantageComputer from the DAG flow. Guidance: follow the existing _create_train_engine/train override pattern in CustomizedPPOTrainer;
  checkpoint the critic (the _save_hf "critic" branch already exists). Tests: a smoke training step over a 2-run DAG runs actor+critic updates
  without GRPO; critic checkpoint written. Demo: one training step over a branched multi-agent DAG with critic-predicted values, GAE advantages,
  and verifier-driven terminal reward.
  - Task 10: End-to-end validation (group_size=2). Objective: full loop — rollout with online critic-agent value prediction → task finish →
  verifier agent sets per-session rewards → harvest → per-task_id GAE → actor+critic train step → clean teardown. Guidance: cloud-only,
  group_size=2 first, then a scale check; assert no orphaned sandboxes/sessions; capture verifier/critic call counts and cost. Tests: e2e smoke
  at group_size=2; resource-leak assertion. Demo: a complete training step over a branched multi-agent turn-graph with agentic-verifier reward,
  generative-critic GAE advantage, and clean teardown.
  
  Phase 4 — Integration + lazy branching + e2e
  
  - Task 11: Bridge/session wiring (model A). In-sandbox agent dispatch injects custom_env (proxy_base_url/proxy_api_key) so LLM routes via db_bridge; each run = an RL
  session (rl_start_session/rl_end_session); daemon keeps writing task_message.seq. Tests: integration — run opens/closes a session, areal/... routed through bridge stub,
  seq rows produced. Demo: one in-sandbox run end-to-end with session + seq capture.
  - Task 12: Verifier-driven reward. Replace constant set_reward(1.0): at finalize call verifier (6/7), emit via rl_set_reward, populate Task 8 credit; safe fallback
  (log+neutral) on verifier error. Tests: finalizer sets verifier reward not 1.0; error path safe. Demo: recorded reward = verifier outcome.
  - Task 13: Lazy branch on selection (decision-point pause). New per-run pause hook quiesces the run at (task_id, seq) (global pause.py only coarse fallback); then
  snapshot → ForkIssueSubtree → agent_start_branch bound to forked sandbox+issue, replay ≤ seq, drop PriorSessionID; in the forked sandbox bind via execenv.Reuse, never
  Prepare; concurrency semaphore on forks; document fidelity choice. Tests: selection → snapshot→fork→branch-start order (mocks); pause ⇒ snapshot at frontier seq; fork
  failure → scratch fallback. Demo: high-entropy node → branch run from forked sandbox+issue at step N.
  - Task 14: Branch cleanup. Extend _cleanup_branch: delete forked sandbox (ForkableEnvironment.cleanup/Daytona delete) + forked issue subtree (DELETE
  /api/issues/{id}/fork); clear node branch flags; idempotent, best-effort logging. Tests: both deletes called; partial failure non-fatal. Demo: forked sandbox + issue
  subtree gone after branch.
  - Task 15: End-to-end validation. Cloud-only group_size=2: branch → verify → per-node/per-task_id credit → graph backup → train step; then scale check. Assert no orphaned
  sandboxes/issues; capture fork count/cost. Tests: e2e smoke at group_size=2; resource-leak assertion. Demo: full training step over a branched multi-agent turn-graph with
  verifier-driven, graph-distributed reward and clean teardown.