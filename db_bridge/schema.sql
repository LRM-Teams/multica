-- ============================================================================
-- db_bridge schema migration
--
-- Supabase-as-springboard RPC bridge between le-agent and AReaL.
--
-- Creates one request/response queue table per bridged endpoint plus
-- table-agnostic RPC functions used by the stub and executor workers:
--
--   * bridge_claim_next(table, worker_id, stale_seconds, user_id)
--       Atomically claims the oldest pending row (or reclaims a row stuck in
--       'claimed' past `stale_seconds`, for crash recovery), optionally scoped
--       to one user_id, using FOR UPDATE SKIP LOCKED so concurrent executors
--       never collide. Returns the claimed row as jsonb, or NULL when none is
--       claimable.
--
--   * bridge_complete(table, id, worker_id, status, response_status, response_headers,
--                     response_body, response_body_encoding, error)
--       Writes the response/error and marks the row done|error, but only when
--       the caller still owns the current claim.
--
--   * bridge_abandon(table, id, user_id, error)
--       Marks a still-pending/claimed row as terminal error after the stub times
--       out, preventing executors from processing requests whose callers have
--       already received a 504.
--
--   * bridge_cleanup_stale(table, retention_seconds, limit)
--       Deletes old terminal rows in bounded batches.
--
--   * bridge_redact_headers(table, id)
--       Optional post-completion hardening: drops the Authorization header
--       from a retained row while preserving the rest for audit.
--
-- Design notes:
--   * Tables live in `public` (so PostgREST/supabase-py expose them with no
--     extra schema config) and are namespaced by the `rpc_` prefix. Every row
--     stores user_id for per-user bridge isolation.
--   * Bodies/files are stored in `text` columns (not jsonb) with an encoding
--     marker; see db_bridge/codec.py. This dodges the ~256 MB jsonb ceiling
--     and keeps polling cheap (pollers never select the body columns).
--   * RLS is enabled with a service_role-only policy. On cloud Supabase the
--     service_role JWT bypasses RLS automatically; on self-hosted Supabase an
--     explicit GRANT + RLS policy is required. The bridge MUST use
--     SUPABASE_SERVICE_ROLE_KEY.
--
-- Apply with: psql "$DATABASE_URL" -f schema.sql   (or the Supabase SQL editor)
-- The migration is idempotent and safe to re-run.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- Tables
-- ----------------------------------------------------------------------------
do $bridge$
declare
    tbl text;
    has_null_user_id boolean;
    tables text[] := array[
        'rpc_rl_start_session',
        'rpc_rl_set_reward',
        'rpc_rl_end_session',
        'rpc_chat_completions',
        'rpc_agent_start',
        'rpc_agent_start_branch'
    ];
begin
    foreach tbl in array tables loop
        execute format($q$
            create table if not exists public.%1$I (
                id                     uuid primary key default gen_random_uuid(),
                channel                text not null,
                user_id                uuid not null,
                status                 text not null default 'pending',
                created_at             timestamptz not null default now(),
                claimed_at             timestamptz,
                completed_at           timestamptz,
                worker_id              text,
                -- request (captured by the stub) -----------------------------
                request_method         text not null,
                request_path           text not null,
                request_headers        jsonb not null default '{}'::jsonb,
                request_content_type   text,
                request_body           text,
                request_body_encoding  text not null default 'raw',
                request_meta           jsonb,
                -- response (written by the executor) -------------------------
                response_status        integer,
                response_headers       jsonb,
                response_body          text,
                response_body_encoding text default 'raw',
                error                  text,
                constraint %2$I
                    check (status in ('pending', 'claimed', 'done', 'error'))
            )
        $q$, tbl, tbl || '_status_chk');

        -- Existing installs may need the column added outside create table.
        -- Keep the add nullable so old rows do not break a schema re-apply; if
        -- the table has no null user_id values, enforce not-null immediately.
        execute format(
            'alter table public.%1$I add column if not exists user_id uuid',
            tbl
        );
        execute format(
            'select exists (select 1 from public.%1$I where user_id is null)',
            tbl
        ) into has_null_user_id;
        if not has_null_user_id then
            execute format(
                'alter table public.%1$I alter column user_id set not null',
                tbl
            );
        end if;

        -- Lightweight composite index for per-user claim ordering / status polling.
        execute format(
            'create index if not exists %1$I on public.%2$I (user_id, status, created_at)',
            tbl || '_user_status_created_idx', tbl
        );

        -- Cleanup index for bounded deletion of old terminal rows.
        execute format(
            'create index if not exists %1$I on public.%2$I (status, completed_at)',
            tbl || '_status_completed_idx', tbl
        );

        -- Lock the table down: RLS enabled with a service_role-only policy.
        -- anon/authenticated are denied; service_role can read/write.
        execute format('alter table public.%1$I enable row level security', tbl);
        execute format('grant all on table public.%1$I to service_role', tbl);
        execute format(
            'create policy allow_service_role on public.%1$I for all to service_role using (true) with check (true)',
            tbl
        );
    end loop;
end;
$bridge$;


-- ----------------------------------------------------------------------------
-- claim_next: atomic, concurrency-safe claim with stale reclaim
-- ----------------------------------------------------------------------------
drop function if exists public.bridge_claim_next(text, text, integer);

create or replace function public.bridge_claim_next(
    p_table         text,
    p_worker_id     text,
    p_stale_seconds integer default 300,
    p_user_id       uuid    default null
)
returns jsonb
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_row jsonb;
begin
    -- Allowlist the table name: only our rpc_* tables, and only if they exist.
    if p_table !~ '^rpc_[a-z0-9_]+$' then
        raise exception 'invalid bridge table name: %', p_table;
    end if;
    if to_regclass('public.' || quote_ident(p_table)) is null then
        raise exception 'unknown bridge table: %', p_table;
    end if;

    execute format($q$
        with claimed as (
            select id
              from public.%1$I
             where ($3 is null or user_id = $3)
               and (status = 'pending'
                    or (status = 'claimed'
                        and claimed_at < now() - make_interval(secs => $1)))
             order by created_at
             for update skip locked
             limit 1
        )
        update public.%1$I t
           set status     = 'claimed',
               worker_id  = $2,
               claimed_at = now()
          from claimed
         where t.id = claimed.id
        returning to_jsonb(t)
    $q$, p_table)
    into v_row
    using p_stale_seconds, p_worker_id, p_user_id;

    return v_row;  -- NULL when nothing was claimable
end;
$$;


-- ----------------------------------------------------------------------------
-- complete: record response/error and finalize the row
--
-- Guarded against the stale-reclaim race: a row is only finalized while it is
-- still claimed by the worker that is completing it. If another worker reclaims
-- the row past BRIDGE_STALE_SECONDS, the stale worker's late completion updates
-- zero rows and returns false, so it can never overwrite a newer result.
-- ----------------------------------------------------------------------------
drop function if exists public.bridge_complete(
    text, uuid, text, integer, jsonb, text, text, text);
drop function if exists public.bridge_complete(
    text, uuid, text, text, integer, jsonb, text, text, text);

create or replace function public.bridge_complete(
    p_table                  text,
    p_id                     uuid,
    p_worker_id              text,
    p_status                 text,
    p_response_status        integer default null,
    p_response_headers       jsonb   default null,
    p_response_body          text    default null,
    p_response_body_encoding text    default 'raw',
    p_error                  text    default null
)
returns boolean
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_updated integer;
begin
    if p_table !~ '^rpc_[a-z0-9_]+$' then
        raise exception 'invalid bridge table name: %', p_table;
    end if;
    if to_regclass('public.' || quote_ident(p_table)) is null then
        raise exception 'unknown bridge table: %', p_table;
    end if;
    if p_status not in ('done', 'error') then
        raise exception 'invalid completion status: %', p_status;
    end if;

    execute format($q$
        update public.%1$I
           set status                 = $2,
               response_status        = $3,
               response_headers       = $4,
               response_body          = $5,
               response_body_encoding = coalesce($6, 'raw'),
               error                  = $7,
               completed_at           = now()
         where id = $1
           and status = 'claimed'
           and worker_id = $8
    $q$, p_table)
    using p_id, p_status, p_response_status, p_response_headers,
          p_response_body, p_response_body_encoding, p_error, p_worker_id;
    get diagnostics v_updated = row_count;
    return v_updated > 0;
end;
$$;


-- ----------------------------------------------------------------------------
-- abandon: mark a timed-out row terminal so it is not executed after 504
-- ----------------------------------------------------------------------------
drop function if exists public.bridge_abandon(text, uuid, text);

create or replace function public.bridge_abandon(
    p_table   text,
    p_id      uuid,
    p_user_id uuid,
    p_error   text default null
)
returns boolean
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_updated integer;
begin
    if p_table !~ '^rpc_[a-z0-9_]+$' then
        raise exception 'invalid bridge table name: %', p_table;
    end if;
    if to_regclass('public.' || quote_ident(p_table)) is null then
        raise exception 'unknown bridge table: %', p_table;
    end if;

    execute format($q$
        update public.%1$I
           set status                 = 'error',
               response_status        = null,
               response_headers       = null,
               response_body          = null,
               response_body_encoding = 'raw',
               error                  = coalesce($2, 'bridge request abandoned'),
               completed_at           = now()
         where id = $1
           and user_id = $3
           and status in ('pending', 'claimed')
    $q$, p_table)
    using p_id, p_error, p_user_id;
    get diagnostics v_updated = row_count;
    return v_updated > 0;
end;
$$;


-- ----------------------------------------------------------------------------
-- cleanup_stale: bounded deletion of old terminal rows
-- ----------------------------------------------------------------------------
create or replace function public.bridge_cleanup_stale(
    p_table             text,
    p_retention_seconds integer default 86400,
    p_limit             integer default 1000
)
returns integer
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_deleted integer;
begin
    if p_table !~ '^rpc_[a-z0-9_]+$' then
        raise exception 'invalid bridge table name: %', p_table;
    end if;
    if to_regclass('public.' || quote_ident(p_table)) is null then
        raise exception 'unknown bridge table: %', p_table;
    end if;
    if p_retention_seconds <= 0 then
        raise exception 'retention seconds must be positive: %', p_retention_seconds;
    end if;
    if p_limit <= 0 then
        raise exception 'cleanup limit must be positive: %', p_limit;
    end if;

    execute format($q$
        with doomed as (
            select id
              from public.%1$I
             where status in ('done', 'error')
               and completed_at < now() - make_interval(secs => $1)
             order by completed_at
             for update skip locked
             limit $2
        )
        delete from public.%1$I t
         using doomed
         where t.id = doomed.id
    $q$, p_table)
    using p_retention_seconds, p_limit;
    get diagnostics v_deleted = row_count;
    return v_deleted;
end;
$$;


-- ----------------------------------------------------------------------------
-- redact_headers: optional post-completion token redaction (Task 10)
-- ----------------------------------------------------------------------------
create or replace function public.bridge_redact_headers(
    p_table text,
    p_id    uuid
)
returns void
language plpgsql
security definer
set search_path = public, pg_temp
as $$
begin
    if p_table !~ '^rpc_[a-z0-9_]+$' then
        raise exception 'invalid bridge table name: %', p_table;
    end if;
    if to_regclass('public.' || quote_ident(p_table)) is null then
        raise exception 'unknown bridge table: %', p_table;
    end if;

    execute format($q$
        update public.%1$I
           set request_headers =
                   case
                       when request_headers ? 'authorization'
                           then jsonb_set(request_headers, '{authorization}',
                                          '"REDACTED"'::jsonb)
                       else request_headers
                   end
         where id = $1
    $q$, p_table)
    using p_id;
end;
$$;


-- ----------------------------------------------------------------------------
-- Permissions: only the service role may execute the bridge functions.
-- ----------------------------------------------------------------------------
do $perm$
declare
    fn text;
    fns text[] := array[
        'public.bridge_claim_next(text, text, integer, uuid)',
        'public.bridge_complete(text, uuid, text, text, integer, jsonb, text, text, text)',
        'public.bridge_abandon(text, uuid, uuid, text)',
        'public.bridge_cleanup_stale(text, integer, integer)',
        'public.bridge_redact_headers(text, uuid)'
    ];
begin
    foreach fn in array fns loop
        if to_regprocedure(fn) is not null then
            execute format('revoke all on function %s from public', fn);
            -- service_role always exists on Supabase; guard for plain Postgres.
            if exists (select 1 from pg_roles where rolname = 'service_role') then
                execute format('grant execute on function %s to service_role', fn);
            end if;
        end if;
    end loop;
end;
$perm$;



-- ============================================================================
-- AReaL DB-backed tmux remote shell
--
-- A trusted-host command bridge: authenticated users and agents enqueue shell
-- text into `areal_remote_commands`, and a runner process on the AReaL machine
-- claims rows and executes them in tmux_id-scoped tmux sessions. This is NOT a
-- sandbox or isolation mechanism -- it deliberately runs arbitrary host shell
-- code for authorized callers. Guard it behind a feature flag.
--
-- Functions (all `security definer`, service_role-only) used by the runner:
--
--   * areal_shell_claim_next(runner_id, lease_seconds)
--       Atomically claim the oldest PENDING row (or reclaim a CLAIMED row whose
--       lease expired before it started running) using FOR UPDATE SKIP LOCKED.
--       Sets status=CLAIMED, runner_id, lease_expires_at, heartbeat_at.
--
--   * areal_shell_mark_running(id, runner_id, lease_seconds)
--       CLAIMED -> RUNNING, stamps started_at and refreshes the lease. Owner-
--       guarded so a reclaimed row cannot be advanced by a stale runner.
--
--   * areal_shell_heartbeat(id, runner_id, lease_seconds, stdout_tail,
--                           stderr_tail, log_bytes)
--       Refreshes the lease/heartbeat and bounded logs while a command runs.
--       Returns the row status + cancel flag so the runner observes a
--       backend-requested cancellation. Returns NULL when ownership was lost.
--
--   * areal_shell_complete(id, runner_id, status, exit_code, stdout_tail,
--                          stderr_tail, log_bytes, error_message)
--       Writes a terminal status (SUCCEEDED|FAILED|CANCELLED|TIMED_OUT), exit
--       code, final logs and finished_at, only while the row is still owned.
--
--   * areal_shell_sweep_stale(limit)
--       Marks RUNNING/CANCEL_REQUESTED rows whose lease expired as STALE rather
--       than re-executing them (ambiguous: execution may have started).
--
--   * areal_shell_request_cancel(id, user_id)
--       Backend-owned cancellation. PENDING rows go straight to CANCELLED;
--       CLAIMED/RUNNING rows go to CANCEL_REQUESTED for the runner to finalize.
--       Returns the resulting status, or {ok:false} for a terminal row.
--
--   * areal_shell_cleanup(retention_seconds, limit)
--       Bounded deletion of old terminal rows.
--
-- RLS is enabled with a service_role-only policy; the runner MUST use
-- SUPABASE_SERVICE_ROLE_KEY because it claims/updates rows across users.
-- ============================================================================

create table if not exists public.areal_remote_commands (
    id                  uuid primary key default gen_random_uuid(),
    user_id             uuid not null,
    tmux_id             text not null,
    agent_run_id        uuid,
    command             text not null,
    cwd                 text,
    timeout_seconds     integer not null,
    status              text not null default 'PENDING',
    exit_code           integer,
    stdout_tail         text not null default '',
    stderr_tail         text not null default '',
    log_bytes           integer not null default 0,
    runner_id           text,
    lease_expires_at    timestamptz,
    heartbeat_at        timestamptz,
    started_at          timestamptz,
    finished_at         timestamptz,
    cancel_requested_at timestamptz,
    error_message       text,
    metadata            jsonb not null default '{}'::jsonb,
    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),
    constraint areal_remote_commands_status_chk check (
        status in (
            'PENDING', 'CLAIMED', 'RUNNING', 'SUCCEEDED', 'FAILED',
            'CANCEL_REQUESTED', 'CANCELLED', 'TIMED_OUT', 'STALE'
        )
    )
);

-- Existing installs may have the older task/account-scoped remote-shell shape.
-- Re-applying schema.sql migrates them to the tmux-scoped command shape.
alter table public.areal_remote_commands
    add column if not exists tmux_id text;

do $remote_shell_tmux_backfill$
begin
    if exists (
        select 1
          from information_schema.columns
         where table_schema = 'public'
           and table_name = 'areal_remote_commands'
           and column_name = 'task_id'
    ) then
        update public.areal_remote_commands
           set tmux_id = task_id::text
         where tmux_id is null;
    end if;
end;
$remote_shell_tmux_backfill$;

update public.areal_remote_commands
   set tmux_id = id::text
 where tmux_id is null;

alter table public.areal_remote_commands
    alter column tmux_id set not null,
    drop column if exists task_id,
    drop column if exists account_id;

drop index if exists areal_remote_commands_task_idx;
drop index if exists areal_remote_commands_account_idx;

-- Claim ordering (oldest PENDING/stale CLAIMED first).
create index if not exists areal_remote_commands_claim_idx
    on public.areal_remote_commands (status, created_at);
-- Lease sweep for stale running rows.
create index if not exists areal_remote_commands_lease_idx
    on public.areal_remote_commands (status, lease_expires_at);
-- Tmux-session and user-scoped reads. The claim RPC serializes active work per tmux_id.
create index if not exists areal_remote_commands_tmux_idx
    on public.areal_remote_commands (tmux_id, status, created_at);
create index if not exists areal_remote_commands_user_idx
    on public.areal_remote_commands (user_id, created_at);

-- Lock the table down: RLS enabled with a service_role-only policy.
alter table public.areal_remote_commands enable row level security;
grant all on table public.areal_remote_commands to service_role;
do $shell_policy$
begin
    if not exists (
        select 1 from pg_policies
         where schemaname = 'public'
           and tablename = 'areal_remote_commands'
           and policyname = 'allow_service_role'
    ) then
        create policy allow_service_role on public.areal_remote_commands
            for all to service_role using (true) with check (true);
    end if;
end;
$shell_policy$;


-- ----------------------------------------------------------------------------
-- claim_next: claim a PENDING row or reclaim a CLAIMED row whose lease expired
-- before it ever started running. RUNNING/CANCEL_REQUESTED rows are never
-- reclaimed here -- they are swept to STALE instead.
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_claim_next(
    p_runner_id     text,
    p_lease_seconds integer default 60
)
returns jsonb
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_row jsonb;
begin
    if p_lease_seconds <= 0 then
        raise exception 'lease seconds must be positive: %', p_lease_seconds;
    end if;

    with claimable as (
        select c.id
          from public.areal_remote_commands c
         where (c.status = 'PENDING'
            or (c.status = 'CLAIMED'
                and c.lease_expires_at is not null
                and c.lease_expires_at < now()))
           and not exists (
                select 1
                  from public.areal_remote_commands active
                 where active.tmux_id = c.tmux_id
                   and active.id <> c.id
                   and active.status in ('CLAIMED', 'RUNNING', 'CANCEL_REQUESTED')
           )
           and not exists (
                select 1
                  from public.areal_remote_commands earlier
                 where earlier.tmux_id = c.tmux_id
                   and earlier.created_at < c.created_at
                   and (earlier.status = 'PENDING'
                    or (earlier.status = 'CLAIMED'
                        and earlier.lease_expires_at is not null
                        and earlier.lease_expires_at < now()))
           )
         order by c.created_at
         for update skip locked
         limit 1
    )
    update public.areal_remote_commands t
       set status           = 'CLAIMED',
           runner_id        = p_runner_id,
           lease_expires_at = now() + make_interval(secs => p_lease_seconds),
           heartbeat_at     = now(),
           updated_at       = now()
      from claimable
     where t.id = claimable.id
    returning to_jsonb(t) into v_row;

    return v_row;  -- NULL when nothing was claimable
end;
$$;


-- ----------------------------------------------------------------------------
-- mark_running: CLAIMED -> RUNNING, owner-guarded.
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_mark_running(
    p_id            uuid,
    p_runner_id     text,
    p_lease_seconds integer default 60
)
returns boolean
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_updated integer;
begin
    update public.areal_remote_commands
       set status           = 'RUNNING',
           started_at       = coalesce(started_at, now()),
           lease_expires_at = now() + make_interval(secs => p_lease_seconds),
           heartbeat_at     = now(),
           updated_at       = now()
     where id = p_id
       and runner_id = p_runner_id
       and status = 'CLAIMED';
    get diagnostics v_updated = row_count;
    return v_updated > 0;
end;
$$;


-- ----------------------------------------------------------------------------
-- heartbeat: refresh lease + bounded logs while running; report cancel flag.
-- Returns NULL when the runner no longer owns the row (reclaimed / swept).
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_heartbeat(
    p_id            uuid,
    p_runner_id     text,
    p_lease_seconds integer,
    p_stdout_tail   text default null,
    p_stderr_tail   text default null,
    p_log_bytes     integer default null
)
returns jsonb
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_row jsonb;
begin
    update public.areal_remote_commands
       set lease_expires_at = now() + make_interval(secs => p_lease_seconds),
           heartbeat_at     = now(),
           stdout_tail      = coalesce(p_stdout_tail, stdout_tail),
           stderr_tail      = coalesce(p_stderr_tail, stderr_tail),
           log_bytes        = coalesce(p_log_bytes, log_bytes),
           updated_at       = now()
     where id = p_id
       and runner_id = p_runner_id
       and status in ('CLAIMED', 'RUNNING', 'CANCEL_REQUESTED')
    returning jsonb_build_object(
        'status', status,
        'cancel_requested', cancel_requested_at is not null
    ) into v_row;

    return v_row;  -- NULL when ownership was lost
end;
$$;


-- ----------------------------------------------------------------------------
-- complete: write a terminal status, exit code, final logs and finished_at.
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_complete(
    p_id            uuid,
    p_runner_id     text,
    p_status        text,
    p_exit_code     integer default null,
    p_stdout_tail   text    default null,
    p_stderr_tail   text    default null,
    p_log_bytes     integer default null,
    p_error_message text    default null
)
returns boolean
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_updated integer;
begin
    if p_status not in ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT') then
        raise exception 'invalid terminal status: %', p_status;
    end if;

    update public.areal_remote_commands
       set status           = p_status,
           exit_code        = p_exit_code,
           stdout_tail      = coalesce(p_stdout_tail, stdout_tail),
           stderr_tail      = coalesce(p_stderr_tail, stderr_tail),
           log_bytes        = coalesce(p_log_bytes, log_bytes),
           error_message    = p_error_message,
           finished_at      = now(),
           lease_expires_at = null,
           updated_at       = now()
     where id = p_id
       and runner_id = p_runner_id
       and status in ('CLAIMED', 'RUNNING', 'CANCEL_REQUESTED');
    get diagnostics v_updated = row_count;
    return v_updated > 0;
end;
$$;


-- ----------------------------------------------------------------------------
-- sweep_stale: mark ambiguous running rows STALE rather than re-executing them.
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_sweep_stale(
    p_limit integer default 100
)
returns integer
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_updated integer;
begin
    if p_limit <= 0 then
        raise exception 'sweep limit must be positive: %', p_limit;
    end if;

    with doomed as (
        select id
          from public.areal_remote_commands
         where status in ('RUNNING', 'CANCEL_REQUESTED')
           and lease_expires_at is not null
           and lease_expires_at < now()
         order by lease_expires_at
         for update skip locked
         limit p_limit
    )
    update public.areal_remote_commands t
       set status        = 'STALE',
           finished_at    = now(),
           error_message  = coalesce(t.error_message, 'runner lease expired'),
           updated_at     = now()
      from doomed
     where t.id = doomed.id;
    get diagnostics v_updated = row_count;
    return v_updated;
end;
$$;


-- ----------------------------------------------------------------------------
-- request_cancel: backend-owned cancellation. PENDING rows are cancelled
-- immediately; CLAIMED/RUNNING rows are flagged for the runner to finalize.
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_request_cancel(
    p_id      uuid,
    p_user_id uuid default null
)
returns jsonb
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_row jsonb;
begin
    update public.areal_remote_commands
       set status              = case
                                      when status = 'PENDING' then 'CANCELLED'
                                      else 'CANCEL_REQUESTED'
                                  end,
           cancel_requested_at = now(),
           finished_at         = case
                                     when status = 'PENDING' then now()
                                     else finished_at
                                 end,
           updated_at          = now()
     where id = p_id
       and (p_user_id is null or user_id = p_user_id)
       and status in ('PENDING', 'CLAIMED', 'RUNNING')
    returning jsonb_build_object('ok', true, 'status', status) into v_row;

    if v_row is not null then
        return v_row;
    end if;

    -- Either terminal (conflict) or not found / not this user.
    select jsonb_build_object('ok', false, 'status', status)
      from public.areal_remote_commands
     where id = p_id
       and (p_user_id is null or user_id = p_user_id)
      into v_row;

    return v_row;  -- NULL when the row does not exist for this user
end;
$$;


-- ----------------------------------------------------------------------------
-- cleanup: bounded deletion of old terminal rows.
-- ----------------------------------------------------------------------------
create or replace function public.areal_shell_cleanup(
    p_retention_seconds integer default 604800,
    p_limit             integer default 1000
)
returns integer
language plpgsql
security definer
set search_path = public, pg_temp
as $$
declare
    v_deleted integer;
begin
    if p_retention_seconds <= 0 then
        raise exception 'retention seconds must be positive: %', p_retention_seconds;
    end if;
    if p_limit <= 0 then
        raise exception 'cleanup limit must be positive: %', p_limit;
    end if;

    with doomed as (
        select id
          from public.areal_remote_commands
         where status in ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'STALE')
           and finished_at is not null
           and finished_at < now() - make_interval(secs => p_retention_seconds)
         order by finished_at
         for update skip locked
         limit p_limit
    )
    delete from public.areal_remote_commands t
     using doomed
     where t.id = doomed.id;
    get diagnostics v_deleted = row_count;
    return v_deleted;
end;
$$;


-- ----------------------------------------------------------------------------
-- Permissions: only the service role may execute the remote-shell functions.
-- ----------------------------------------------------------------------------
do $shell_perm$
declare
    fn text;
    fns text[] := array[
        'public.areal_shell_claim_next(text, integer)',
        'public.areal_shell_mark_running(uuid, text, integer)',
        'public.areal_shell_heartbeat(uuid, text, integer, text, text, integer)',
        'public.areal_shell_complete(uuid, text, text, integer, text, text, integer, text)',
        'public.areal_shell_sweep_stale(integer)',
        'public.areal_shell_request_cancel(uuid, uuid)',
        'public.areal_shell_cleanup(integer, integer)'
    ];
begin
    foreach fn in array fns loop
        if to_regprocedure(fn) is not null then
            execute format('revoke all on function %s from public', fn);
            if exists (select 1 from pg_roles where rolname = 'service_role') then
                execute format('grant execute on function %s to service_role', fn);
            end if;
        end if;
    end loop;
end;
$shell_perm$;