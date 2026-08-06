# Ephemeral Sandbox Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make single-agent env-dispatch sandbox routing replace dead sandboxes
on retry and reclaim every ephemeral sandbox reliably.

**Architecture:** A startup-wired `EphemeralSandboxManager` composes runtime
pre-creation with the existing sandbox lifecycle service. An ephemeral
`runtime_offline` retry provisions its replacement before inserting the retry
child directly on the new runtime. Task terminal paths share one cleanup seam,
and SQL predicates/idempotency keep liveness and deletion scoped and race-safe.

**Tech Stack:** Go 1.26.1, PostgreSQL/sqlc, Chi services/handlers, Python 3.12,
httpx, pytest.

## Global Constraints

- Preserve the current single-agent scope; do not add squad runtime routing.
- Do not add dependencies or alter launcher/scheduler behavior.
- Derive provider from the bound `agent_runtime`, never optional runtime JSON.
- The Cube topology supports `pi`; reject other providers before provisioning.
- Use red-green TDD for every production behavior.
- Regenerate sqlc output after SQL edits; do not hand-edit generated Go.
- Keep task retry creation atomic with pending-wake transfer.
- Keep AReaL proxy stripping/session reopening behavior intact.

---

## File map

- `server/internal/service/env_sandbox_lifecycle.go`: compensate partial creates
  and make create-job notification best-effort.
- `server/internal/handler/env_sandbox_lifecycle_adapter.go`: preserve creator
  identity, distinguish not-found from transient errors, enqueue idempotent
  delete jobs.
- `server/internal/handler/ephemeral_sandbox_manager.go`: provision replacement
  runtime/sandbox resources and clean terminal resources.
- `server/internal/service/task.go`: manager interface, retry override, and
  centralized terminal cleanup.
- `server/internal/service/env_dispatch.go` and
  `server/internal/handler/env_dispatch.go`: authoritative provider and richer
  ephemeral marker.
- `server/pkg/db/queries/{agent,runtime,sandbox}.sql`: scoped sweep, retry
  override, authoritative lookup, and idempotent delete job.
- `server/migrations/182_ephemeral_sandbox_delete_idempotency.{up,down}.sql`:
  one active delete job per sandbox instance.
- `server/cmd/server/{router,main,runtime_sweeper}.go`: startup wiring and shared
  task service.
- `customized_areal/tree_search/agents/multica_client.py`: remove deployment
  defaults.

---

### Task 1: Make sandbox lifecycle creation compensating

**Files:**

- Modify: `server/internal/service/env_sandbox_lifecycle.go:123-166`
- Modify: `server/internal/service/env_sandbox_lifecycle_test.go`

**Interfaces:**

- Consumes: `EnvSandboxLifecycleDeps.ForceDeleteSandboxInstance`.
- Produces: `EnvSandboxLifecycleService.Create`, which leaves no pending instance
  after any pre-job failure and treats notification as best-effort.

- [ ] **Step 1: Write failing lifecycle tests**

Add table-driven tests whose fake records force-delete calls:

```go
func TestEnvSandboxLifecycleCreateCompensatesPostInsertFailures(t *testing.T) {
    tests := []struct {
        name string
        configure func(*fakeEnvSandboxLifecycleDeps)
    }{
        {"mint failure", func(f *fakeEnvSandboxLifecycleDeps) { f.mintErr = errors.New("mint") }},
        {"enqueue failure", func(f *fakeEnvSandboxLifecycleDeps) { f.enqueueErr = errors.New("enqueue") }},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            f := newFakeEnvSandboxLifecycleDeps()
            tt.configure(f)
            svc := NewEnvSandboxLifecycleService(f, time.Second)
            _, err := svc.Create(context.Background(), CreateSandboxInstanceInput{
                WorkspaceID: "ws", Template: "default", DaemonEnabled: true,
            }, "user")
            if err == nil { t.Fatal("expected create failure") }
            if diff := cmp.Diff([]string{"instance-1"}, f.forceDeleted); diff != "" {
                t.Fatalf("force deletes (-want +got):\n%s", diff)
            }
        })
    }
}

func TestEnvSandboxLifecycleCreateNotificationFailureKeepsDurableJob(t *testing.T) {
    f := newFakeEnvSandboxLifecycleDeps()
    f.notifyErr = errors.New("websocket unavailable")
    svc := NewEnvSandboxLifecycleService(f, time.Second)
    ref, err := svc.Create(context.Background(), CreateSandboxInstanceInput{
        WorkspaceID: "ws", Template: "default",
    }, "user")
    if err != nil { t.Fatalf("Create: %v", err) }
    if ref.InstanceID != "instance-1" { t.Fatalf("instance = %q", ref.InstanceID) }
    if len(f.forceDeleted) != 0 { t.Fatalf("unexpected compensation: %v", f.forceDeleted) }
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
cd multica/server && go test ./internal/service -run 'TestEnvSandboxLifecycleCreate(Compensates|Notification)' -count=1
```

Expected: compensation assertions fail and notification failure is returned.

- [ ] **Step 3: Implement retained-ref compensation**

Add a local helper after insertion and use it on mint, payload, and enqueue
failures:

```go
compensate := func(cause error) (SandboxInstanceRef, error) {
    if cleanupErr := s.deps.ForceDeleteSandboxInstance(
        context.WithoutCancel(ctx), ref.WorkspaceID, ref.InstanceID,
    ); cleanupErr != nil {
        return SandboxInstanceRef{}, errors.Join(cause, fmt.Errorf("compensate sandbox instance: %w", cleanupErr))
    }
    return SandboxInstanceRef{}, cause
}
```

Log notification failure and return `ref, nil` after the durable job exists.

- [ ] **Step 4: Verify GREEN**

Run the Step 2 command and then:

```bash
cd multica/server && go test ./internal/service -run EnvSandboxLifecycle -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/env_sandbox_lifecycle.go server/internal/service/env_sandbox_lifecycle_test.go
git commit -m "fix(env-dispatch): compensate sandbox create failures"
```

### Task 2: Carry authoritative provider and cleanup actor

**Files:**

- Modify: `server/pkg/db/queries/runtime.sql`
- Regenerate: `server/pkg/db/generated/runtime.sql.go`
- Modify: `server/internal/service/env_dispatch.go`
- Modify: `server/internal/handler/env_dispatch.go`
- Modify: `server/internal/service/env_dispatch_test.go`
- Modify: `server/internal/handler/env_dispatch_squad_chat_test.go`
- Modify: `server/internal/handler/env_dispatch_squad_issue_test.go`

**Interfaces:**

- Changes `PrecreateAgentRuntime` to return an error unless the bound runtime's
  provider is `pi`.
- Changes `EnqueueAgentRun` to accept `actorUserID` and stores it under
  `context.ephemeral_sandbox.actor_user_id`.

- [ ] **Step 1: Write failing provider and marker tests**

Add handler/service tests that establish an agent with `RuntimeConfig={}` and a
bound `pi` runtime, plus a bound `codex` runtime rejection:

```go
func TestPrecreateAgentRuntimeUsesBoundRuntimeProvider(t *testing.T) {
    // Seed agent.runtime_config={} and agent.runtime_id -> provider=pi.
    runtimeID, _, err := adapter.PrecreateAgentRuntime(ctx, wsID, userID, agentID)
    if err != nil { t.Fatalf("PrecreateAgentRuntime: %v", err) }
    got := mustGetRuntime(t, queries, runtimeID)
    if got.Provider != "pi" { t.Fatalf("provider = %q, want pi", got.Provider) }
}

func TestPrecreateAgentRuntimeRejectsNonPiBoundRuntime(t *testing.T) {
    // Seed agent.runtime_id -> provider=codex.
    _, _, err := adapter.PrecreateAgentRuntime(ctx, wsID, userID, agentID)
    if err == nil || !strings.Contains(err.Error(), "requires pi") {
        t.Fatalf("error = %v, want pi validation", err)
    }
}
```

Extend enqueue tests to decode context and assert both marker fields.

- [ ] **Step 2: Verify RED**

Run:

```bash
cd multica/server && go test ./internal/handler ./internal/service -run 'TestPrecreateAgentRuntime|TestDispatch_.*Actor' -count=1
```

Expected: provider test reads no authoritative runtime and actor marker is absent.

- [ ] **Step 3: Add authoritative provider query**

Add to `runtime.sql`:

```sql
-- name: GetAgentBoundRuntimeForWorkspace :one
SELECT r.*
FROM agent a
JOIN agent_runtime r ON r.id = a.runtime_id
WHERE a.id = @agent_id AND a.workspace_id = @workspace_id
  AND r.workspace_id = @workspace_id;
```

Run:

```bash
cd multica && make sqlc
```

- [ ] **Step 4: Thread actor identity and replace provider extraction**

Change `EnqueueAgentRun` to include `actorUserID` immediately after
`workspaceID`, pass `in.UserID` from `dispatchOne`, and replace `agentProvider`
with the bound-runtime query:

```go
bound, err := a.h.Queries.GetAgentBoundRuntimeForWorkspace(ctx,
    db.GetAgentBoundRuntimeForWorkspaceParams{
        AgentID: agentUUID, WorkspaceID: wsUUID,
    })
if err != nil { return "", "", stackerr.Wrap(err, "get bound runtime") }
if bound.Provider != "pi" {
    return "", "", fmt.Errorf("pi-in-sandbox requires pi runtime, got %q", bound.Provider)
}
```

Update the marker merger:

```go
marker, _ := json.Marshal(map[string]string{
    "sandbox_instance_id": instanceID,
    "actor_user_id": actorUserID,
})
```

- [ ] **Step 5: Verify GREEN and regenerate drift**

Run:

```bash
cd multica/server && go test ./internal/handler ./internal/service -run 'TestPrecreateAgentRuntime|TestDispatch_' -count=1
cd multica && make sqlc && git diff --exit-code -- server/pkg/db/generated
```

Expected: PASS and no generated drift.

- [ ] **Step 6: Commit**

```bash
git add server/pkg/db/queries/runtime.sql server/pkg/db/generated/runtime.sql.go server/internal/service/env_dispatch.go server/internal/handler/env_dispatch.go server/internal/service/env_dispatch_test.go server/internal/handler/env_dispatch_squad_chat_test.go server/internal/handler/env_dispatch_squad_issue_test.go
git commit -m "fix(env-dispatch): bind sandbox runtime identity safely"
```

### Task 3: Add scoped sweep and retry overrides

**Files:**

- Modify: `server/pkg/db/queries/agent.sql`
- Regenerate: `server/pkg/db/generated/agent.sql.go`
- Modify: `server/cmd/server/runtime_sweeper_test.go`
- Modify: `server/internal/service/task_test.go`

**Interfaces:**

- `ExpireQueuedTasksOnOfflineRuntimes` only matches tasks with a valid ephemeral
  marker.
- `CreateRetryTask` accepts nullable `runtime_id` and `context` overrides.

- [ ] **Step 1: Write failing SQL integration tests**

Extend the sweeper fixture with one ordinary and one ephemeral queued task on
offline runtimes. Assert only the ephemeral row is returned. Add a retry test:

```go
child, err := queries.CreateRetryTask(ctx, db.CreateRetryTaskParams{
    ID: parent.ID,
    RuntimeID: replacementRuntime.ID,
    Context: replacementContext,
})
if err != nil { t.Fatal(err) }
if child.RuntimeID != replacementRuntime.ID { t.Fatalf("wrong runtime") }
if !bytes.Equal(child.Context, replacementContext) { t.Fatalf("wrong context") }
```

- [ ] **Step 2: Verify RED**

Run:

```bash
cd multica/server && go test ./cmd/server ./internal/service -run 'TestExpireQueuedTasksOnOfflineRuntimes|TestCreateRetryTaskOverrides' -count=1
```

Expected: ordinary task is expired and retry override params do not exist.

- [ ] **Step 3: Scope the sweep and parameterize retry creation**

Add this predicate to victim selection and the final update:

```sql
AND NULLIF(t.context->'ephemeral_sandbox'->>'sandbox_instance_id', '') IS NOT NULL
```

Change the retry projection to:

```sql
COALESCE(sqlc.narg('runtime_id'), p.runtime_id),
COALESCE(sqlc.narg('context'), p.context)
```

and use `@id` for the parent key. Regenerate with `make sqlc`.

- [ ] **Step 4: Verify GREEN**

Run the Step 2 command and `make sqlc` drift check.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/db/queries/agent.sql server/pkg/db/generated/agent.sql.go server/cmd/server/runtime_sweeper_test.go server/internal/service/task_test.go
git commit -m "fix(tasks): scope sandbox liveness and retry binding"
```

### Task 4: Implement fresh-sandbox retry manager

**Files:**

- Create: `server/internal/handler/ephemeral_sandbox_manager.go`
- Create: `server/internal/handler/ephemeral_sandbox_manager_test.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/task_test.go`
- Modify: `server/internal/service/env_sandbox_lifecycle.go`
- Modify: `server/internal/handler/env_sandbox_lifecycle_adapter.go`

**Interfaces:**

- Produces `service.EphemeralSandboxManager` with `PrepareRetry`, `Reclaim`, and
  `Cleanup`.
- `TaskService.MaybeRetryFailedTask` requests replacement resources only for an
  ephemeral `runtime_offline` task and creates the child directly on them.

- [ ] **Step 1: Write failing service tests with a fake manager**

```go
func TestMaybeRetryOfflineEphemeralTaskUsesFreshResources(t *testing.T) {
    manager := &fakeEphemeralSandboxManager{prepared: &EphemeralRetryResources{
        RuntimeID: uuid("new-runtime"), Context: []byte(`{"ephemeral_sandbox":{"sandbox_instance_id":"new","actor_user_id":"actor"}}`),
    }}
    svc := newTaskServiceForTest(t)
    svc.EphemeralSandboxManager = manager
    child, err := svc.MaybeRetryFailedTask(ctx, ephemeralOfflineParent())
    if err != nil { t.Fatal(err) }
    if child.RuntimeID != manager.prepared.RuntimeID { t.Fatalf("child used old runtime") }
    if manager.reclaims != 0 { t.Fatalf("replacement reclaimed after success") }
}

func TestMaybeRetryReclaimsFreshResourcesWhenChildInsertFails(t *testing.T) {
    svc := newTaskServiceForTest(t)
    manager := &fakeEphemeralSandboxManager{prepared: &EphemeralRetryResources{
        RuntimeID: util.MustParseUUID("eeeeeeee-0000-0000-0000-000000000098"),
    }}
    svc.EphemeralSandboxManager = manager
    child, err := svc.MaybeRetryFailedTask(ctx, ephemeralOfflineParent())
    if err == nil { t.Fatal("expected retry insert failure") }
    if child != nil { t.Fatalf("unexpected child: %+v", child) }
    if manager.reclaims != 1 { t.Fatalf("reclaims = %d, want 1", manager.reclaims) }
}
```

- [ ] **Step 2: Verify RED**

Run:

```bash
cd multica/server && go test ./internal/service -run 'TestMaybeRetry.*Fresh' -count=1
```

Expected: manager types/field are undefined.

- [ ] **Step 3: Add service interface and retry override flow**

Define in `task.go`:

```go
type EphemeralRetryResources struct {
    RuntimeID pgtype.UUID
    Context []byte
}

type EphemeralSandboxManager interface {
    PrepareRetry(context.Context, db.AgentTaskQueue) (*EphemeralRetryResources, error)
    Reclaim(context.Context, *EphemeralRetryResources) error
    Cleanup(context.Context, db.AgentTaskQueue) error
}
```

Pass overrides into `createRetryTaskWithPendingWakeTransfer`. On insertion or
transaction failure, call `Reclaim(context.WithoutCancel(ctx), resources)`.
Preserve `StripArealProxyFromTaskContext` and pending-wake transfer in the same
transaction.

- [ ] **Step 4: Verify service tests GREEN**

Run the Step 2 command.

- [ ] **Step 5: Write failing handler manager tests**

Cover:

```go
func TestEphemeralSandboxManagerPrepareRetryCreatesNewRuntimeAndSandbox(t *testing.T)
func TestEphemeralSandboxManagerPrepareRetryCompensatesRuntimeOnCreateFailure(t *testing.T)
func TestEphemeralSandboxManagerCleanupUsesMarkerActor(t *testing.T)
func TestEphemeralSandboxManagerCleanupFallsBackToSandboxCreator(t *testing.T)
func TestEphemeralSandboxManagerCleanupPropagatesTransientLookupError(t *testing.T)
```

Assert `MULTICA_DAEMON_ID` is new, provider is authoritative `pi`, returned
context points at the new instance, and delete uses a non-empty actor.

- [ ] **Step 6: Implement the handler manager**

The implementation must:

```go
type ephemeralSandboxManager struct {
    h *Handler
    lifecycle service.SandboxInstanceCreator
}
```

Use `GetSandboxInstanceRef` for the old template, the authoritative runtime
query from Task 2, `PrecreateAgentRuntime`, and `CreateSandboxInstance` with:

```go
CreateSandboxInstanceInput{
    WorkspaceID: workspaceID,
    Template: oldRef.Template,
    DaemonEnabled: true,
    RuntimeEnv: map[string]string{"MULTICA_DAEMON_ID": daemonID},
}
```

`Cleanup` checks `HasOtherActiveTaskForRuntime`, offlines R′, and deletes the
sandbox with marker actor or `oldRef.CreatorUserID` fallback. Use
`errors.Is(err, pgx.ErrNoRows)` for idempotent lookup success and propagate all
other errors.

- [ ] **Step 7: Verify handler and service GREEN**

```bash
cd multica/server && go test ./internal/handler ./internal/service -run 'EphemeralSandbox|MaybeRetry' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add server/internal/handler/ephemeral_sandbox_manager.go server/internal/handler/ephemeral_sandbox_manager_test.go server/internal/handler/env_sandbox_lifecycle_adapter.go server/internal/service/env_sandbox_lifecycle.go server/internal/service/task.go server/internal/service/task_test.go
git commit -m "fix(tasks): reprovision ephemeral sandbox retries"
```

### Task 5: Make cleanup idempotent and wire every terminal path

**Files:**

- Create: `server/migrations/182_ephemeral_sandbox_delete_idempotency.up.sql`
- Create: `server/migrations/182_ephemeral_sandbox_delete_idempotency.down.sql`
- Modify: `server/pkg/db/queries/sandbox.sql`
- Regenerate: `server/pkg/db/generated/sandbox.sql.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/task_test.go`
- Modify: `server/cmd/server/router.go`
- Modify: `server/cmd/server/main.go`
- Modify: `server/cmd/server/runtime_sweeper_test.go`
- Modify: `server/internal/handler/env_dispatch.go`

**Interfaces:**

- One active delete job per instance.
- The handler and sweeper use the same startup-configured `TaskService` and
  manager.
- Every bulk cancellation finalizes ephemeral resources.

- [ ] **Step 1: Write failing wiring/cancellation/idempotency tests**

Add tests that construct the router without sending env-dispatch and assert the
manager is already non-nil. Pass `h.TaskService` to a sweeper helper and assert
offline failure invokes its fake manager. Extend task tests so
`CancelTasksForIssue`, `CancelTasksForAgent`, `CancelTasksByTriggerComment`,
`BroadcastCancelledTasks`, `CaptureCancelledTasks`, and `RerunIssue` each call
cleanup for returned ephemeral rows.

Add a DB integration test that enqueues delete twice and observes one active
job.

- [ ] **Step 2: Verify RED**

```bash
cd multica/server && go test ./internal/service ./cmd/server ./internal/handler -run 'Cleanup.*Cancel|Router.*Ephemeral|DeleteJob.*Idempotent|Sweeper.*Cleaner' -count=1
```

Expected: manager is nil before request, bulk paths omit cleanup, duplicate job
is inserted.

- [ ] **Step 3: Add active-delete uniqueness and query**

Migration up:

```sql
CREATE UNIQUE INDEX sandbox_job_one_active_delete_per_instance
ON sandbox_job(instance_id)
WHERE type = 'delete' AND status IN ('queued', 'dispatched', 'running');
```

Migration down drops that index. Add `CreateSandboxDeleteJob` using
`ON CONFLICT DO NOTHING`; when no row is returned, load the existing active
delete job so lifecycle delete remains idempotent. Regenerate sqlc.

- [ ] **Step 4: Centralize cancellation cleanup**

Add:

```go
func (s *TaskService) finalizeCancelledTask(ctx context.Context, task db.AgentTaskQueue) {
    s.captureTaskCancelled(ctx, task)
    s.RouteTerminalTrainingTask(ctx, task)
    s.cleanupEphemeralSandbox(ctx, task)
}
```

Call it from all cancellation loops and from `BroadcastCancelledTasks` and
`CaptureCancelledTasks`. Ensure callers do not invoke it twice for the same row;
database delete idempotency remains the concurrency backstop.

- [ ] **Step 5: Wire manager at startup and share TaskService**

Construct lifecycle/manager in `NewRouterWithOptions`, assign it to
`h.TaskService`, and remove request-time mutation from `Handler.EnvDispatch`.
In `main.go`, replace the second service construction with:

```go
taskSvc := h.TaskService
taskSvc.Wakeup = daemonWakeup
```

Apply training configuration to that shared instance before starting workers.

- [ ] **Step 6: Verify GREEN and migration round-trip**

Run Step 2, then:

```bash
cd multica/server && go test ./cmd/server ./internal/handler ./internal/service -count=1
cd multica && make sqlc && git diff --exit-code -- server/pkg/db/generated
```

Expected: PASS and generated code clean.

- [ ] **Step 7: Commit**

```bash
git add server/migrations/182_ephemeral_sandbox_delete_idempotency.* server/pkg/db/queries/sandbox.sql server/pkg/db/generated/sandbox.sql.go server/internal/service/task.go server/internal/service/task_test.go server/cmd/server/router.go server/cmd/server/main.go server/cmd/server/runtime_sweeper_test.go server/internal/handler/env_dispatch.go
git commit -m "fix(tasks): centralize ephemeral sandbox cleanup"
```

### Task 6: Remove deployment-specific debug defaults

**Files:**

- Modify: `../customized_areal/tree_search/agents/multica_client.py`
- Modify: `../customized_areal/tree_search/tests/test_env_dispatch_client.py`

**Interfaces:**

- Debug CLI accepts explicit flags/environment only and errors before network
  access when base URL, workspace, or agent ID is absent.

- [ ] **Step 1: Write failing CLI tests**

```python
def test_debug_main_requires_deployment_coordinates(monkeypatch, capsys):
    for key in ("MULTICA_BASE_URL", "MULTICA_WORKSPACE_SLUG", "MULTICA_WORKSPACE_ID"):
        monkeypatch.delenv(key, raising=False)
    with pytest.raises(SystemExit) as exc:
        main(["--agent-id", "agent"])
    assert exc.value.code == 2
    assert "--base-url" in capsys.readouterr().err

def test_debug_parser_has_no_shared_deployment_defaults(monkeypatch):
    monkeypatch.delenv("MULTICA_BASE_URL", raising=False)
    parser = build_debug_parser()
    args = parser.parse_args([])
    assert args.base_url is None
    assert args.workspace_slug is None
    assert args.agent_id is None
```

- [ ] **Step 2: Verify RED**

```bash
uv run pytest customized_areal/tree_search/tests/test_env_dispatch_client.py -k 'debug_main_requires or no_shared_deployment' -q
```

Expected: hardcoded values make assertions fail.

- [ ] **Step 3: Extract parser and remove defaults**

Create `build_debug_parser()` and use environment-only defaults:

```python
parser.add_argument("--base-url", default=os.environ.get("MULTICA_BASE_URL"), required=False)
parser.add_argument("--workspace-slug", default=os.environ.get("MULTICA_WORKSPACE_SLUG"))
parser.add_argument("--workspace-id", default=os.environ.get("MULTICA_WORKSPACE_ID"))
parser.add_argument("--agent-id", default=os.environ.get("MULTICA_AGENT_ID"))
```

After parsing, call `parser.error` when base URL, agent ID, or exactly one
workspace selector is missing.

- [ ] **Step 4: Verify GREEN**

```bash
uv run pytest customized_areal/tree_search/tests/test_env_dispatch_client.py -q
```

Expected: PASS.

- [ ] **Step 5: Commit in the AReaL repository**

```bash
git add customized_areal/tree_search/agents/multica_client.py customized_areal/tree_search/tests/test_env_dispatch_client.py
git commit -m "fix: remove env-dispatch debug deployment defaults"
```

### Task 7: Final verification and graph refresh

**Files:**

- Update generated graph files via `graphify update .` after code changes.

- [ ] **Step 1: Run Go formatting and generated-code checks**

```bash
cd multica && gofmt -w server/internal/service/env_sandbox_lifecycle.go server/internal/service/env_dispatch.go server/internal/service/task.go server/internal/handler/env_dispatch.go server/internal/handler/env_sandbox_lifecycle_adapter.go server/internal/handler/ephemeral_sandbox_manager.go
cd multica && make sqlc
git -C multica diff --check
```

Expected: exit 0.

- [ ] **Step 2: Run affected Go packages**

```bash
cd multica/server && go test ./internal/service ./internal/handler ./cmd/server -count=1
```

Expected: PASS with zero failures.

- [ ] **Step 3: Run Python client tests**

```bash
uv run pytest customized_areal/tree_search/tests/test_env_dispatch_client.py -q
```

Expected: PASS.

- [ ] **Step 4: Run repository checks proportional to the change**

```bash
cd multica && make test
cd multica && make check
```

Expected: exit 0. If integration tests require unavailable PostgreSQL/Cube
infrastructure, record the exact skipped/failed command and run every remaining
unit package.

- [ ] **Step 5: Refresh Graphify**

```bash
graphify update .
```

Expected: graph update exits 0; graph output changes are retained as required by
the repository instructions.

- [ ] **Step 6: Review requirement coverage**

Confirm from fresh test output that:

- fresh retry binds directly to R2/S2;
- partial resources are compensated;
- normal offline runtimes retain the two-hour TTL;
- every cancellation and sweeper path reaches cleanup;
- delete jobs carry a valid actor and deduplicate;
- provider is authoritative and pi-validated;
- debug code has no deployment endpoint/workspace/agent defaults.

- [ ] **Step 7: Commit verification-only changes in their owning repository**

```bash
git -C multica add server
git -C multica commit -m "chore: apply sandbox reliability verification output"
git add graphify-out
git commit -m "chore: refresh code graph after sandbox reliability fixes"
```

Run only the pair whose repository has tracked verification changes. Skip both
commits when verification produces no tracked changes.
