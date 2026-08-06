# Memory Curator Profile Spec

Written: 2026-07-14
Updated: 2026-07-14
Status: implemented in feature branch `feat/user-curator-profiles`, pending review and merge
Target branch: `LRM-Teams/multica` `dev`

## What This Feature Does

Memory Curator Profile lets each user choose how Multica should maintain Agent memory.

A user can open the Self-evolution / Evolution Center page and save:

- which Pi Runtime should execute memory curation;
- which user-owned Agent should act as the Curator Agent;
- whether to curate all Agents owned by the user or only selected Agents;
- whether runs are automatic;
- which hour of day automatic runs should start;
- which curation mode to use;
- whether a manual run should be dry-run only;
- which L1-L4 stage to run manually;
- what confidence threshold is required before model-assisted decisions can change memory.

The feature moves memory curation away from a server-local file job and toward a user-owned Runtime job. The Server stores the intent and governance rules. The configured daemon Runtime executes the work against the Agent memory files. The Curator Agent can help propose semantic decisions, but deterministic Go code still decides what is allowed to change.

## Why This Exists

Before this work, memory curation was mostly a hard-coded local pipeline. It depended on server-side assumptions and selected an online Pi Runtime implicitly. That made it hard for a user to know which Runtime was responsible, which Agent was doing review, and which Agents were being evolved.

This feature makes those choices explicit:

- no saved profile means no user memory curation runs;
- an invalid Runtime or Curator Agent binding fails closed;
- an offline but valid Runtime creates a waiting run instead of silently executing somewhere else;
- manual and scheduled runs use the same durable queue and daemon execution path.

## Main Concepts

## Curator Profile

A Curator Profile is one saved configuration per user per workspace.

It stores the user's selected Runtime, Curator Agent, target scope, schedule, mode, confidence threshold, and current config version. When a run is created, the important values are copied onto the run as a snapshot, so later profile edits do not silently change queued or running work.

## Runtime

The Runtime is the user's Pi Runtime that owns execution. It must be visible to the user and must advertise memory-curation support before it can claim a run.

If the Runtime is offline, the Server keeps a `waiting_runtime` run. When the Runtime heartbeats again with memory-curation capability, it can claim the run.

## Curator Agent

The Curator Agent is a user-owned Agent selected in the profile. It is used to supply reviewer instructions and optional model override for L3 semantic review.

The Curator Agent does not directly get permission to rewrite memory files. It proposes or classifies; the Go memory curation engine validates routes, confidence, sensitivity, profile mode, file mutations, audit records, and queue state.

## Target Agents

The profile supports two target scopes:

- `owned_all`: evolve every active Agent owned by the current user in the workspace;
- `selected`: evolve only the saved selected Agents, which must also be owned by the user.

Manual runs use the saved profile targets, not unsaved form edits.

## L1-L4 Stages

The feature keeps the existing staged memory model and makes it profile-backed.

- L1 records daily evidence and activity into Agent memory daily files.
- L2 extracts candidate memories from daily records into review queues.
- L3 asks the Curator Agent reviewer to classify high-confidence candidates and then applies only allowed decisions.
- L4 publishes validated shareable memory or skill candidates into the sync queue.

Users can manually run a single stage or all stages. Automatic runs schedule L1-L4 in order using staged hourly offsets.

## Modes

The profile mode controls how far the system may go without human review.

- `observe`: run as dry-run. The system can inspect and produce results, but should not write memory changes.
- `review`: collect and review proposals, but defer model decisions that would mutate memory.
- `auto_safe`: allow only conservative high-confidence memory promotions.
- `auto`: allow validated model routes that pass deterministic checks.

The mode is enforced in the Go engine, not only in the UI.

## Dry-run

Dry-run runs the same execution path but avoids writing file mutations. It is available for manual runs and is also used by observe mode.

Dry-run is important because users can inspect what the system would do before allowing automatic memory changes.

## How It Works

## Server

The Server owns configuration, validation, queueing, and audit state.

It adds a Curator Profile API:

- `GET /api/workspaces/{workspaceId}/memory-curation/profile`
- `PUT /api/workspaces/{workspaceId}/memory-curation/profile`

It validates:

- the user is a workspace member;
- the Runtime is visible to the user;
- the Curator Agent is owned by the user;
- selected target Agents are owned by the user;
- mode, timezone, schedule hour, model override, and confidence threshold are valid.

Manual run creation now creates a durable daemon-owned run intent from the saved profile instead of directly running server-local files:

- `POST /api/workspaces/{workspaceId}/memory-curation/runs`

If the saved Runtime or Curator Agent is no longer valid, the Server returns a conflict and asks the user to choose them again. If the Runtime is valid but offline, the run is saved as `waiting_runtime`.

## Scheduler

The Scheduler reads enabled Curator Profiles and creates scheduled run intents.

It uses each profile's timezone and schedule hour:

- L1 starts at the configured hour;
- L2 starts one hour later;
- L3 starts two hours later;
- L4 starts three hours later.

The Scheduler copies profile values onto each run, including Runtime, Curator Agent, mode, confidence threshold, config version, and target Agent IDs. Scheduled runs are deduplicated per profile, stage, and date.

## Daemon

The daemon is the only component that executes profile-backed memory curation against local Agent memory roots.

It advertises memory-curation support in heartbeat. The Server only lets capable runtimes claim memory-curation runs. A daemon also reports its active memory-curation run during heartbeat, allowing the Server to renew the lease.

The daemon runs at most one memory-curation job per Runtime at a time. When it finishes, it reports the result back to:

- `/api/daemon/runtimes/{runtimeId}/memory-curation/{runId}/result`

Result reports must match the runtime, run ID, and claim token. This prevents a stale worker or duplicate claim from finishing a newer run.

## Claim And Lease Safety

Run claiming is fenced by claim tokens and active leases.

A claim records:

- runtime ID;
- claim token;
- claimed timestamp;
- running status;
- attempt count.

The Server rejects result callbacks with the wrong runtime, wrong token, or non-running status. Heartbeats extend the active lease for the same runtime and run. Stale queued or running work can be reclaimed safely.

## Curator Agent And L3 Review

L3 review is the model-assisted stage. The reviewer receives bounded untrusted candidate text and must return strict JSON. It can choose routes like memory, skill, split, or discard, and must classify sensitivity as `none`, `sensitive`, or `unknown`.

The engine only applies a model decision when all checks pass:

- candidate is eligible;
- sensitivity is explicitly safe;
- confidence meets the configured threshold;
- discard uses the stricter discard threshold;
- profile mode allows the route;
- memory and skill drafts are complete and sanitized;
- file mutations can be committed safely;
- audit traces can be written.

Sensitive or unknown-sensitivity decisions are deferred. Reviewer failure also defers candidates instead of deleting them.

## Database Changes

Migration `169_memory_curator_profile` adds:

- `memory_curator_profile` for saved user configuration;
- `memory_curator_target` for selected target Agents;
- profile and execution snapshot columns on `memory_curation_run`;
- statuses for queued, waiting runtime, running, succeeded, failed, invalid config, and cancelled;
- runtime queue indexes and scheduled-run uniqueness;
- claim fields for daemon execution fencing.

The down migration removes those additions.

## Frontend Changes

The Evolution Center now includes controls for:

- selected Pi Runtime;
- selected Curator Agent;
- target scope and selected target Agents;
- mode;
- automatic schedule toggle;
- timezone;
- schedule hour;
- model override;
- confidence threshold;
- catch-up setting;
- manual stage selection;
- manual dry-run.

Frontend core packages include typed API clients, schemas, query helpers, and exported member permission helpers. The UI follows the existing page and design-system style instead of introducing a separate settings surface.

## What Is Complete

Implemented in this branch:

- profile table, target table, run snapshot columns, queue indexes, and down migration;
- profile GET and PUT handlers with permission and ownership checks;
- manual run creation through saved profile snapshots;
- offline Runtime handling with `waiting_runtime`;
- invalid profile binding handling with fail-closed conflict responses;
- scheduler-created per-profile L1-L4 run intents;
- daemon claim, lease, heartbeat, and result reporting path;
- heartbeat protocol fields for memory-curation capability and active run ID;
- daemon-side execution using the selected Runtime and Curator Agent instructions/model;
- one active memory-curation run per Runtime in the daemon;
- claim-token fencing for result callbacks;
- profile-mode governance in the engine;
- sensitivity-aware L3 handling that defers sensitive or unknown candidates;
- frontend profile form and manual run controls;
- core API schemas, types, query options, and tests;
- backend tests for profile, manual run, scheduler, heartbeat, claim, and result flows.

## Validation Performed

Validated on 2026-07-14 from `/home/jianghp3/gaia/multica-curator-profile`.

Passing checks:

- clean temporary Postgres database, full migration, then `go test ./internal/handler -count=1`;
- `go test ./internal/handler -run 'Test(GetMemoryCurationRunRejectsInvalidRunID|GetAgentMemoryCurationStatusRejectsInvalidAgentID|PublicMemoryCurationStatsProjectsSharedPromotionCounts|PublicMemoryCurationStatsHandlesMalformedPayload|MemoryCuratorProfileQueuesAndCompletesDaemonRun)$' -count=1`;
- `go test ./internal/memorycuration ./internal/scheduler ./internal/daemonws -count=1`;
- `go test ./internal/daemon -run 'TestMemoryCuration|Test.*MemoryCuration' -count=20`;
- `pnpm --filter @multica/core test`;
- `pnpm --filter @multica/core typecheck`;
- `pnpm --filter @multica/views typecheck`;
- `git diff --check`.

Known validation note:

- `go test ./internal/daemon -count=1` full package hit an existing unrelated flaky failure in `TestPollLoopTargetsRuntimeWakeup`, where a targeted wakeup unexpectedly woke a slow runtime. The Memory Curator daemon tests passed repeatedly.
- A top-level LSP workspace diagnostic reported an unrelated `/home/jianghp3/gaia/oh-my-pi` TypeScript project include issue. The Multica worktree was validated through its own Go and pnpm commands.

## Limitations

Current limitations:

- The feature assumes the configured Runtime has local access to the target Agent memory roots.
- L3 model review is intentionally bounded and conservative; uncertain sensitivity is deferred.
- The UI exposes configuration and manual runs, but deeper per-run review UX can still be improved.
- Automatic scheduling creates intents; actual execution still depends on the selected Runtime being online and capable.
- The feature does not try to migrate every old local-memory-curation behavior into a compatibility path. It intentionally requires an explicit profile.
- The current implementation does not introduce a new cloud/team curator service; it prepares local governed units that future sync layers can use.

## Risks

Main risks to watch after merge:

- Dirty or non-standard local databases may contain old experimental schema and can produce misleading test failures.
- Runtime capability reporting must stay consistent across HTTP and WebSocket heartbeat paths.
- If a Runtime loses access to Agent memory roots, queued runs may fail even when the Server profile is valid.
- Automatic mode should be monitored carefully because model output quality affects proposal routing, even though Go still enforces final governance.
- Long-running or crashed daemon jobs rely on lease expiry and reclaim behavior; production metrics should watch stuck `running` and `waiting_runtime` counts.

## Next Steps

Recommended next steps:

1. Review and merge this branch into `LRM-Teams/multica` `dev`.
2. Add product polish for per-run review details in the Evolution Center.
3. Add operational dashboards for queued, waiting, running, succeeded, failed, and invalid-config memory-curation runs.
4. Add production alerts for stale running leases and repeated reviewer failures.
5. Decide whether to add a one-time migration or admin tool for users with old local-memory-curation setups.
6. Revisit broader team/cloud memory sync after the local profile-governed loop is stable.
