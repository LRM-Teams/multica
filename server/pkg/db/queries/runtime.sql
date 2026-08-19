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

-- name: ListAgentRuntimeConnectivityByIDs :many
-- task #84: replaces attachAgentRuntimeNames's hand-rolled SELECT. That
-- query listed columns explicitly, so every new agent_runtime column (task
-- #1801 offline_reason, #1802 starting_since, #81 pinned_version) needed a
-- manual SELECT+scan+struct-field edit to actually reach GET /agents — three
-- separate "done but unreachable" incidents in one day. sqlc.embed(agent_runtime)
-- returns the whole row, so a future column is available on the generated
-- struct the moment its migration lands and this query is regenerated — no
-- Go changes here. effective_name mirrors the display-name fallback the old
-- inline SQL computed (display_name, then name, then "Cloud" for cloud-mode
-- runtimes with neither).
SELECT sqlc.embed(agent_runtime),
       COALESCE(
         NULLIF(display_name, ''),
         NULLIF(name, ''),
         CASE WHEN runtime_mode = 'cloud' THEN 'Cloud' ELSE '' END
       )::text AS effective_name
FROM agent_runtime
WHERE id = ANY(@ids::uuid[]);

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
--
-- display_name is intentionally omitted from DO UPDATE: daemon register /
-- reconnect must keep refreshing `name` (hostname / reported label) but
-- must never clobber a user-set display_name. New inserts default to ''
-- via the column default.
--
-- offline_reason = NULL is required, not optional cleanup (found while
-- writing the agent intentional-stop signal design doc, Open Question 1):
-- without it, a runtime that was gracefully stopped (offline_reason set by
-- SetAgentRuntimeOffline) and later reconnects would carry the stale reason
-- forever, so a FUTURE real (silence-based) disconnect would incorrectly
-- still read as "stopped" instead of "disconnected".
--
-- starting_since is unconditionally cleared here: a completed register is
-- the authoritative "no longer starting" fact. The retired
-- /api/daemon/starting write path is gone, but leftover rows from older
-- daemons must still drop the stamp on the next successful register.
--
-- owner_id uses first-owner-wins: COALESCE(existing, excluded). A later PAT
-- re-register (e.g. admin helping install-service) must not steal ownership
-- from the machine's original owner — FE upgrade alerts filter owner=me.
--
-- pinned_version (task #81) mirrors the same "refresh on every register"
-- rule: the daemon sends its current MULTICA_PINNED_VERSION (empty string
-- when unset) on every call, so unpinning a machine (removing the env var
-- and restarting) clears the stale value here too, the same way
-- offline_reason/starting_since already do. NULLIF converts "" to NULL so
-- "not pinned" reads as absent, not as an empty-string pin.
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
    last_seen_at,
    pinned_version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), NULLIF($10, ''))
ON CONFLICT (workspace_id, daemon_id, provider)
DO UPDATE SET
    name = EXCLUDED.name,
    runtime_mode = EXCLUDED.runtime_mode,
    status = EXCLUDED.status,
    device_info = EXCLUDED.device_info,
    metadata = EXCLUDED.metadata,
    owner_id = COALESCE(agent_runtime.owner_id, EXCLUDED.owner_id),
    offline_reason = NULL,
    last_seen_at = now(),
    updated_at = now(),
    starting_since = NULL,
    pinned_version = NULLIF($10, '')
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

-- name: UpdateAgentRuntimeDisplayName :one
-- Sets the user-editable machine label. Empty string clears the override so
-- clients fall back to daemon-reported `name`. Gated at the handler by
-- canEditRuntime (owner / workspace admin).
UPDATE agent_runtime
SET display_name = @display_name, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: UpdateAgentRuntimeCustomEnv :one
-- Replaces a runtime's custom_env map wholesale. Used by the dedicated
-- env-management endpoint (GET/PUT /api/runtimes/{id}/env). Runtime-level
-- env is the machine-default layer injected before agent custom_env.
UPDATE agent_runtime
SET custom_env = @custom_env, updated_at = now()
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
-- offline_reason is optional: callers that know why the runtime is offline
-- (daemon graceful deregister, sandbox teardown) pass a reason_code string
-- (uses the shared runtime reason-code vocabulary, no new enum). Callers that
-- don't know pass a zero-value/NULL pgtype.Text, which preserves today's
-- "we don't know why" behavior — see docs/superpowers/specs/2026-08-02-
-- agent-intentional-stop-signal-design.md.
UPDATE agent_runtime
SET status = 'offline', offline_reason = @offline_reason, updated_at = now()
WHERE id = @id;

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
-- Requeues in-flight tasks when their runtime is offline. This cleans up
-- orphaned tasks after a daemon crash or network partition.
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

-- name: DetachDerivedAgentsFromSources :exec
-- A derived agent may outlive the source agent's computer. Permanent computer
-- deletion removes the source agent, so preserve surviving derived agents while
-- clearing their lineage FK before the source row is hard-deleted.
UPDATE agent
SET source_agent_id = NULL, updated_at = now()
WHERE source_agent_id = ANY(@source_agent_ids::uuid[]);

-- name: DeleteLegacySquadsByLeaderIDs :exec
-- Migration 222 retired the Squad surface and emptied this table, but kept the
-- legacy schema. Delete any unexpected legacy rows whose RESTRICT leader FK
-- would otherwise make permanent agent deletion fail.
DELETE FROM squad
WHERE leader_id = ANY(@leader_ids::uuid[]);

-- name: DeleteVoiceCallSessionsByAgentIDs :exec
-- voice_call_session.agent_id intentionally has no ON DELETE rule. Computer
-- deletion owns the archived agent's complete history, so remove both ended and
-- active session rows here; voice_call_turn follows through ON DELETE CASCADE.
DELETE FROM voice_call_session
WHERE agent_id = ANY(@agent_ids::uuid[]);

-- name: CancelRunningAgentExecutionsByAgentIDs :exec
-- agent_execution is an immutable attribution ledger and intentionally has no
-- agent FK, so hard-deleting an agent does not remove its history. It must
-- still stop claiming that the provider execution is live once the owning
-- agent is archived or permanently deleted.
UPDATE agent_execution
SET status = 'cancelled',
    completed_at = COALESCE(completed_at, now()),
    failure_reason = @failure_reason
WHERE agent_id = ANY(@agent_ids::uuid[])
  AND status = 'running';

-- name: ListArchivedAgentIDsByRuntime :many
-- Companion to DeleteArchivedAgentsByRuntime: enumerates archived agents about
-- to be hard-deleted. The row lock also prevents a concurrent FK writer from
-- attaching new voice-call/lineage/legacy-squad dependents between dependent
-- cleanup and the final agent DELETE.
SELECT id FROM agent WHERE runtime_id = $1 AND archived_at IS NOT NULL FOR UPDATE;

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
-- Active tasks pinned to
-- any of the given runtimes. Used by computer-level bulk delete to refuse
-- with a structured 4xx when the machine still has live work (LRM-238 /
-- LRM-438) instead of silently leaving orphaned tasks.
SELECT count(*)::bigint
FROM agent_inbox_event
WHERE runtime_id = ANY(@runtime_ids::uuid[])
  AND status IN ('pending', 'draining', 'failed');
