-- name: ListAgentRuntimes :many
SELECT * FROM agent_runtime
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: ListVisibleAgentRuntimes :many
-- Privacy-scoped list: a member sees their own runtimes plus every public
-- runtime; another member's private runtime is never returned. There is NO
-- owner/admin override here — visibility is per-user even for workspace
-- admins (the unscoped ListAgentRuntimes stays for internal callers).
SELECT * FROM agent_runtime
WHERE workspace_id = $1 AND (owner_id = $2 OR visibility = 'public')
ORDER BY created_at ASC;

-- name: GetAgentRuntime :one
SELECT * FROM agent_runtime
WHERE id = $1;

-- name: LockAgentRuntime :one
-- Acquires a row-level exclusive lock on the runtime row. Used at the
-- top of the cascade-delete transaction so that:
--   1. PostgreSQL's FK validation on agent.runtime_id (FK ... ON DELETE
--      RESTRICT) needs FOR KEY SHARE on the parent runtime row, which
--      conflicts with FOR UPDATE — so any concurrent INSERT or UPDATE
--      that would point a new/moved agent at this runtime blocks until
--      our transaction finishes; and
--   2. concurrent UPDATE/DELETE of the runtime row itself (e.g. another
--      delete attempt) waits for us to commit.
-- Combined with ListActiveAgentsByRuntimeForUpdate (which row-locks the
-- existing active set) this closes the plan-compare → archive race that
-- was possible at read-committed isolation between the snapshot and the
-- bulk archive.
SELECT * FROM agent_runtime
WHERE id = $1
FOR UPDATE;

-- name: GetAgentRuntimeForWorkspace :one
SELECT * FROM agent_runtime
WHERE id = $1 AND workspace_id = $2;

-- name: GetAgentBoundRuntimeForWorkspace :one
SELECT r.*
FROM agent a
JOIN agent_runtime r ON r.id = a.runtime_id
WHERE a.id = @agent_id AND a.workspace_id = @workspace_id
  AND r.workspace_id = @workspace_id;

-- name: UpsertAgentRuntime :one
-- (xmax = 0) AS inserted distinguishes a fresh insert (true) from an upsert
-- that updated an existing row (false). Analytics reads this to fire
-- runtime_registered/runtime_ready only on first-time registration.
INSERT INTO agent_runtime (
    workspace_id,
    daemon_id,
    name,
    runtime_mode,
    provider,
    status,
    device_info,
    metadata,
    owner_id,
    last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (workspace_id, daemon_id, provider)
DO UPDATE SET
    name = EXCLUDED.name,
    runtime_mode = EXCLUDED.runtime_mode,
    status = EXCLUDED.status,
    device_info = EXCLUDED.device_info,
    metadata = EXCLUDED.metadata,
    owner_id = COALESCE(EXCLUDED.owner_id, agent_runtime.owner_id),
    last_seen_at = now(),
    updated_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: PrecreateAgentRuntime :one
-- Inserts a pending (offline) agent_runtime row keyed by a caller-supplied
-- daemon_id, so an in-sandbox daemon booted with MULTICA_DAEMON_ID=<daemon_id>
-- adopts THIS row on register: UpsertAgentRuntime's ON CONFLICT
-- (workspace_id, daemon_id, provider) DO UPDATE matches it, flips status to
-- online, and reuses this id (R'). Used by env-dispatch to make a sandbox's
-- runtime_id deterministic at dispatch time so the task can carry it
-- immediately (no NULL, no deferred binding). runtime_mode='local',
-- status='offline' until the daemon registers.
INSERT INTO agent_runtime (
    workspace_id,
    daemon_id,
    name,
    runtime_mode,
    provider,
    status,
    device_info,
    metadata,
    owner_id,
    last_seen_at
) VALUES ($1, $2, $3, 'local', $4, 'offline', '', '{}', $5, now())
RETURNING id, workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, created_at, updated_at, owner_id, legacy_daemon_id, visibility;

-- name: UpdateAgentRuntimeVisibility :one
-- Toggles a runtime between 'private' (only owner can bind agents) and
-- 'public' (any workspace member can). Default for new rows is 'private'
-- (see migration 083). Gated at the handler layer to owner / workspace
-- admin only.
UPDATE agent_runtime
SET visibility = @visibility, updated_at = now()
WHERE id = @id
RETURNING *;


-- name: TouchAgentRuntimeLastSeen :execrows
-- Bumps last_seen_at on an already-online runtime. Deliberately does NOT
-- touch status or updated_at: status is unchanged on the hot heartbeat path,
-- and avoiding updated_at keeps the row HOT-eligible (no index columns
-- change) and avoids invalidating any downstream consumer that watches
-- updated_at.
--
-- The status='online' predicate is load-bearing: callers read rt.Status from
-- a prior SELECT and may race with the sweeper, which can flip the row to
-- offline between that SELECT and this UPDATE. Without the predicate this
-- query would silently leave a freshly-heartbeated runtime stuck in offline.
-- Returning affected rows lets callers detect that race and fall back to
-- MarkAgentRuntimeOnline to flip the row back online.
UPDATE agent_runtime
SET last_seen_at = now()
WHERE id = $1 AND status = 'online';

-- name: TouchAgentRuntimesLastSeenBatch :execrows
-- Bulk variant of TouchAgentRuntimeLastSeen used by the BatchedHeartbeatScheduler:
-- coalesces N per-runtime "bump last_seen_at" requests into a single UPDATE so a
-- fleet beating every 15s costs ~1 DB transaction per batch tick instead of N.
--
-- Same load-bearing predicate as the single-id form: status='online' avoids
-- silently un-deleting a sweeper-flipped offline row, and we deliberately do
-- NOT touch updated_at so the rows stay HOT-eligible. Affected-rows < len(ids)
-- means some IDs raced to offline between Schedule and flush; their next beat
-- will fall through the recordHeartbeat sync path and call MarkAgentRuntimeOnline.
UPDATE agent_runtime
SET last_seen_at = now()
WHERE id = ANY(@ids::uuid[]) AND status = 'online';

-- name: MarkAgentRuntimeOnline :one
-- Used on the offline→online transition (and on first heartbeat after
-- registration). Writes status, last_seen_at, and updated_at because the
-- status flip is a real state change and we want updated_at to reflect it.
UPDATE agent_runtime
SET status = 'online', last_seen_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAgentRuntimeOffline :exec
UPDATE agent_runtime
SET status = 'offline', updated_at = now()
WHERE id = $1;

-- name: SelectStaleOnlineRuntimes :many
-- Lists online runtimes whose last_seen_at exceeds the stale window. The
-- sweeper uses this as a candidate set, then optionally filters via the
-- LivenessStore before flipping rows to offline (a fresh Redis liveness
-- record means the DB row is just lagging, not actually dead).
SELECT id, workspace_id, owner_id, daemon_id, provider FROM agent_runtime
WHERE status = 'online'
  AND last_seen_at < now() - make_interval(secs => @stale_seconds::double precision);

-- name: MarkRuntimesOfflineByIDs :many
-- Flips a known set of runtime IDs from online to offline. Paired with
-- SelectStaleOnlineRuntimes in the sweeper so the candidate selection and
-- the actual write are decoupled (the LivenessStore filter sits between).
--
-- Re-checks the stale predicate inside the UPDATE so a concurrent heartbeat
-- between the SELECT (candidate gather), the LivenessStore filter, and this
-- UPDATE cannot demote a runtime that just refreshed last_seen_at. The
-- legacy MarkStaleRuntimesOffline UPDATE had this property implicitly
-- because the predicate and the write lived in one statement; here we
-- carry it forward explicitly so the SELECT/filter/UPDATE pipeline retains
-- the same race-freedom.
UPDATE agent_runtime
SET status = 'offline', updated_at = now()
WHERE status = 'online'
  AND id = ANY(@ids::uuid[])
  AND last_seen_at < now() - make_interval(secs => @stale_seconds::double precision)
RETURNING id, workspace_id, owner_id, daemon_id, provider;

-- name: FailTasksForOfflineRuntimes :many
-- Marks dispatched/running/waiting_local_directory tasks as failed when
-- their runtime is offline. This cleans up orphaned tasks after a daemon
-- crash or network partition.
UPDATE agent_inbox_event
SET status = 'pending', last_error = 'runtime went offline',
    failure_reason = 'runtime_offline', claimed_at = NULL,
    wait_reason = NULL
WHERE status = 'draining'
  AND runtime_id IN (
    SELECT id FROM agent_runtime WHERE status = 'offline'
  )
RETURNING *;

-- name: ListAgentRuntimesByOwner :many
SELECT * FROM agent_runtime
WHERE workspace_id = $1 AND owner_id = $2
ORDER BY created_at ASC;

-- name: ForceOfflineRuntimesByIDs :many
-- Unconditionally flips a known set of runtime IDs to offline. Distinct from
-- MarkRuntimesOfflineByIDs (which keeps a stale-window predicate so the
-- sweeper cannot demote a runtime that just heartbeated): this variant is
-- used by intentional revocation paths — e.g. removing a workspace member —
-- where the caller has already decided the runtime should be offline
-- regardless of recent liveness.
UPDATE agent_runtime
SET status = 'offline', updated_at = now()
WHERE id = ANY(@runtime_ids::uuid[]) AND status = 'online'
RETURNING id, workspace_id, owner_id, daemon_id, provider;

-- name: CancelAgentTasksByRuntimeOrAgent :many
-- Cancels every active task that either lives on one of the given runtimes
-- OR belongs to one of the given agents. Used by the member-revocation flow:
-- the runtime-side covers tasks queued against the leaving member's runtimes;
-- the agent-side covers tasks pinned to a different runtime that those agents
-- left behind from a prior UpdateAgent (agent.runtime_id can change, but
-- agent_inbox_event.runtime_id does not get rewritten when it does, so a task
-- queued on runtime A by agent X — later moved to runtime B — survives the
-- runtime-only revoke and could still be drained because inbox admission does
-- not gate on agent.archived_at).
--
-- We use 'cancelled' rather than 'failed' so the daemon's per-task status
-- poller (watchTaskCancellation) interrupts the running agent gracefully.
-- Returns the affected rows so the caller can broadcast task:cancelled and
-- reconcile per-agent status.
UPDATE agent_inbox_event
SET status = 'suppressed', terminal_outcome = 'cancelled',
    completed_at = now(), terminal_at = now(), acked_at = now()
WHERE (runtime_id = ANY(@runtime_ids::uuid[]) OR agent_id = ANY(@agent_ids::uuid[]))
  AND status IN ('pending', 'draining', 'failed')
RETURNING *;

-- name: DeleteAgentRuntime :exec
DELETE FROM agent_runtime WHERE id = $1;

-- name: DeleteAgentRuntimeForWorkspace :exec
-- Workspace-scoped delete used by env-dispatch to reclaim a pre-created
-- runtime R' when its rollout fails before the task is created. Scoping on
-- workspace_id is defense-in-depth: R' is server-generated, but this keeps the
-- delete inside the dispatch's own workspace. The task FK is ON DELETE CASCADE,
-- so callers must ensure no task references R' (the failure path creates none).
DELETE FROM agent_runtime WHERE id = $1 AND workspace_id = $2;

-- name: CountActiveAgentsByRuntime :one
SELECT count(*) FROM agent WHERE runtime_id = $1 AND archived_at IS NULL;

-- name: DeleteArchivedAgentsByRuntime :exec
DELETE FROM agent WHERE runtime_id = $1 AND archived_at IS NOT NULL;

-- name: PauseAutopilotsByAgentAssignees :exec
-- Pauses every active autopilot whose agent assignee is in the supplied list.
-- Called before hard-deleting archived agents on runtime teardown so the rows
-- do not become dangling (autopilot.assignee_id no longer has an agent FK
-- since migration 096). Status='paused' makes the breakage visible in the UI
-- — operators can re-point the autopilot at a live agent or delete it —
-- rather than silently piling skipped runs.
UPDATE autopilot
SET status = 'paused', updated_at = now()
WHERE status = 'active'
  AND assignee_type = 'agent'
  AND assignee_id = ANY(@assignee_ids::uuid[]);

-- name: ListArchivedAgentIDsByRuntime :many
-- Companion to DeleteArchivedAgentsByRuntime: enumerates the archived agents
-- about to be hard-deleted so the runtime teardown can pause autopilots that
-- still point at them. Returns ids only — the caller only needs the set.
SELECT id FROM agent WHERE runtime_id = $1 AND archived_at IS NOT NULL;

-- name: FindLegacyRuntimesByDaemonID :many
-- Looks up runtime rows keyed on a prior (hostname-derived) daemon_id. Used
-- at register-time to find rows owned by the same machine under its old
-- identity so agents/tasks can be re-pointed at the new UUID-keyed row.
--
-- Comparison is case-insensitive because os.Hostname() has been observed to
-- return different casings on the same machine (e.g. `Jiayuans-MacBook-Pro`
-- vs `jiayuans-macbook-pro`) across reboots/mDNS state changes. A case-
-- sensitive `=` would strand the old row; LOWER() on both sides handles drift
-- without forcing the daemon to enumerate cased permutations.
--
-- Returns many rather than one because case drift may have already minted
-- duplicate rows historically (e.g. `Foo.local` AND `foo.local` under the
-- same workspace+provider). A single-row lookup would consolidate only one
-- of them and leave the rest orphaned. Callers must merge every returned
-- row into the new UUID-keyed runtime.
SELECT * FROM agent_runtime
WHERE workspace_id = @workspace_id
  AND provider = @provider
  AND LOWER(daemon_id) = LOWER(@daemon_id);

-- name: ReassignAgentsToRuntime :execrows
-- Re-points every agent referencing old_runtime_id at new_runtime_id.
UPDATE agent
SET runtime_id = @new_runtime_id
WHERE runtime_id = @old_runtime_id;

-- name: ReassignTasksToRuntime :execrows
-- Re-points every queued/running/completed task referencing old_runtime_id.
-- Required before deleting the old runtime row because agent_inbox_event has
-- an ON DELETE CASCADE FK that would otherwise drop historical tasks.
UPDATE agent_inbox_event
SET runtime_id = @new_runtime_id
WHERE runtime_id = @old_runtime_id;

-- name: RecordRuntimeLegacyDaemonID :exec
-- Remembers the most recent hostname-derived daemon_id that was merged into
-- this row. Useful for debugging when tracing back why a given runtime row
-- subsumed an old one, and only overwrites NULL so the earliest merge is
-- preserved.
UPDATE agent_runtime
SET legacy_daemon_id = COALESCE(legacy_daemon_id, $2)
WHERE id = $1;

-- name: DeleteStaleOfflineRuntimes :many
-- Deletes runtimes that have been offline for longer than the TTL and have
-- no agents bound (active or archived). The FK constraint on agent.runtime_id
-- is ON DELETE RESTRICT, so we must exclude all agent references.
DELETE FROM agent_runtime
WHERE status = 'offline'
  AND last_seen_at < now() - make_interval(secs => @stale_seconds::double precision)
  AND id NOT IN (SELECT DISTINCT runtime_id FROM agent)
RETURNING id, workspace_id;

-- name: FindOnlineSandboxRuntime :one
-- Resolves the daemon-registered Pi runtime for an env-dispatch binding by
-- immutable identity (workspace, daemon_id, sandbox_instance_id) once the
-- provider reports online. Runtime display names are intentionally not used;
-- matching by name would let an unrelated runtime bind to the dispatch. Used
-- by WaitForOnlineSandboxRuntime during first-address provisioning.
SELECT * FROM agent_runtime
WHERE workspace_id = @workspace_id
  AND provider = 'pi'
  AND daemon_id = @daemon_id
  AND status = 'online'
  AND metadata->>'sandbox_instance_id' = @sandbox_instance_id
LIMIT 1;

-- name: ListAgentRuntimesByDaemonID :many
-- Computer / host scope: every runtime row sharing a daemon_id inside a
-- workspace. Case-insensitive match matches FindLegacyRuntimesByDaemonID —
-- os.Hostname()-derived ids have drifted in casing across reboots. Optional
-- runtime_mode narrows to the FE machine key (`local:<daemon>` vs
-- `cloud:<daemon>`). Empty @runtime_mode means "any mode".
SELECT *
FROM agent_runtime
WHERE workspace_id = @workspace_id
  AND daemon_id IS NOT NULL
  AND LOWER(daemon_id) = LOWER(@daemon_id)
  AND (
    @runtime_mode::text = ''
    OR runtime_mode = @runtime_mode
  )
ORDER BY created_at ASC;

-- name: CountActiveTasksByRuntimeIDs :one
-- Active (queued/dispatched/running/waiting_local_directory) tasks pinned to
-- any of the given runtimes. Used by computer-level bulk delete to refuse
-- with a structured 4xx when the machine still has live work (LRM-238 /
-- LRM-438) instead of silently leaving orphaned tasks.
SELECT count(*)::bigint
FROM agent_inbox_event
WHERE runtime_id = ANY(@runtime_ids::uuid[])
  AND status IN ('pending', 'draining', 'failed');
