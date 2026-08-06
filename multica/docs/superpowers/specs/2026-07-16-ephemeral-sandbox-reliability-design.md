# Ephemeral Sandbox Reliability Design

Date: 2026-07-16
Status: approved for implementation

## 1. Goal

Make single-agent env-dispatch rollouts reliably execute inside a fresh Cube
sandbox, replace a sandbox whose daemon does not register, and reclaim every
ephemeral sandbox on all terminal task paths.

Squad runtime routing remains out of scope. Existing `per_agent_env` behavior is
unchanged until the squad topology and cost model are separately approved.

## 2. Invariants

1. A routed task always carries a non-null runtime ID for the sandbox daemon.
2. The daemon adopts the pre-created runtime using the same authoritative
   provider as the agent's bound runtime.
3. A retry caused by sandbox-registration timeout uses a new sandbox, runtime,
   and daemon ID. It never reuses the known-dead runtime.
4. The retry child is never visible on the old runtime. Replacement resources
   are created before the child is inserted, and the child is inserted directly
   with the replacement runtime and context.
5. Every terminal task path is cleanup-aware, including background sweeps and
   bulk cancellation.
6. Sandbox create and replacement failures compensate all resources created
   before the failure.
7. The five-minute offline-runtime timeout applies only to env-dispatch
   ephemeral tasks.
8. Cleanup is idempotent. Not-found is success; transient errors remain visible.

## 3. Runtime and sandbox identity

`agent_runtime.provider` on the agent's bound `runtime_id` is the authoritative
runtime provider. Env-dispatch must not derive it from the optional
`agent.runtime_config` JSON.

For the pi-in-sandbox topology, runtime pre-creation validates that the
authoritative provider is `pi`. A different provider fails dispatch before a
sandbox instance or task is created, because the Cube image is only guaranteed
to register the installed pi runtime.

Each ephemeral task context stores:

```json
{
  "ephemeral_sandbox": {
    "sandbox_instance_id": "<uuid>",
    "actor_user_id": "<uuid>"
  }
}
```

The actor ID is the authenticated env-dispatch caller. It is used as the
initiator for create/delete lifecycle jobs and is copied to retry children.
The marker is not a secret.

## 4. Shared sandbox manager

Replace the cleanup-only seam with one startup-wired manager shared by HTTP task
handling and the runtime sweeper:

```go
type EphemeralSandboxManager interface {
    PrepareRetry(ctx context.Context, task db.AgentTaskQueue) (
        EphemeralRetryResources, error,
    )
    Cleanup(ctx context.Context, task db.AgentTaskQueue) error
}
```

`EphemeralRetryResources` contains the new runtime ID, sandbox marker context,
and a compensation callback or explicit reclaim method. The implementation
composes the existing env-sandbox lifecycle service and runtime queries. It does
not call the HTTP env-dispatch handler or recreate projects, issues, or chat
sessions.

The server constructs the lifecycle service and manager once during startup,
then injects the same manager into the handler's `TaskService` and the sweeper's
`TaskService`. Request handlers never mutate shared service configuration.

## 5. Fresh-sandbox retry flow

Only `failure_reason=runtime_offline` tasks carrying a valid ephemeral marker
use replacement provisioning. Other retries retain the existing behavior.

1. Load the old sandbox reference and its template.
2. Resolve the task agent's authoritative runtime provider and validate `pi`.
3. Pre-create a new offline runtime R2 with a new daemon ID.
4. Create a new daemon-enabled sandbox S2, overlaying the new
   `MULTICA_DAEMON_ID` and using the marker's actor ID.
5. Insert the retry child directly with `runtime_id=R2` and a marker for S2.
6. If child insertion fails, delete S2 and R2.
7. After child insertion succeeds, mark the old runtime R1 offline and enqueue
   deletion of old sandbox S1.

Provisioning failure produces no retry child. The original task remains failed,
the error is logged and observable, and any partially-created replacement
resources are reclaimed.

This ordering removes the claim race that would exist if a child were first
created on R1 and rebound later.

## 6. Terminal cleanup

All terminal transitions feed one cleanup function after retry creation has
finished:

- normal completion;
- explicit single-task cancellation;
- failure;
- sweeper failure batches;
- cancel-by-issue;
- cancel-by-agent;
- cancel-by-trigger-comment;
- rerun cancellation and other bulk cancellation broadcasts.

Cleanup first checks for another active task on the runtime. If one exists, it
returns without tearing down shared retry resources. Otherwise it marks the
runtime offline and deletes the sandbox using the marker's actor ID.

Cleanup must be idempotent under concurrent terminal callbacks. A conditional
sandbox state transition or equivalent database serialization ensures at most
one active delete job is enqueued per instance. A missing sandbox instance is
success; any other lookup or enqueue failure is returned for logging.

## 7. Scoped liveness timeout

The five-minute query must identify ephemeral tasks explicitly from
`agent_task_queue.context.ephemeral_sandbox.sandbox_instance_id`. Ordinary
queued tasks on offline runtimes continue to use the existing two-hour queued
TTL.

The query retains its current `FOR UPDATE SKIP LOCKED`, queued-status recheck,
and runtime-still-offline recheck so an online/claimed task cannot be overwritten.

## 8. Partial-create compensation

`EnvSandboxLifecycleService.Create` retains the inserted sandbox reference and
compensates every later failure:

- runtime environment mint failure: force-delete the pending instance;
- payload construction failure: force-delete the pending instance;
- job enqueue failure: force-delete the pending instance;
- notification failure: keep the durable queued job and treat notification as
  best-effort, because sandboxd polls jobs independently.

Runtime pre-creation callers continue reclaiming R' when sandbox creation or
task creation fails. The lifecycle layer owns sandbox-instance compensation;
env-dispatch owns runtime compensation.

## 9. Debug client configuration

The AReaL debug CLI has no deployment-specific defaults. Base URL, workspace,
and agent ID come from explicit flags or environment variables. Missing required
values produce an actionable argument error before making a request.

## 10. Testing

Implementation follows red-green TDD with focused tests for:

- cleanup enqueues a delete job with the marker actor ID;
- manager wiring exists before any env-dispatch request and is shared with the
  sweeper service;
- provider comes from the bound runtime and non-pi providers are rejected;
- runtime-offline retry creates R2/S2 and inserts the child directly on R2;
- replacement provisioning and child-insert failures compensate R2/S2;
- every bulk cancellation path invokes cleanup;
- the five-minute query ignores ordinary offline runtimes;
- post-insert sandbox-create failures delete the pending instance;
- transient sandbox lookup errors propagate while not-found remains idempotent;
- deployment-specific debug defaults are absent.

Targeted Go tests run first. The final gate includes the affected Go packages,
the AReaL Python client tests, formatting, and repository graph refresh after
code changes.

## 11. Rollout risks

- Replacement provisioning increases the amount of work performed inside the
  failure path. Compensation tests and direct child insertion bound orphan and
  claim-race risks.
- Context markers from tasks created before this change lack `actor_user_id`.
  For backward cleanup compatibility, the manager resolves the sandbox creator
  from the existing sandbox row when the marker field is absent; it never uses
  an empty actor ID.
- Existing duplicate terminal callbacks may race. Database-backed delete
  idempotency is required rather than relying on in-process synchronization.
