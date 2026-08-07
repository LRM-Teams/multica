# Research Transaction Fault Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every canonical Research Run write transaction deterministically fault-injectable and prove recovery from rollback and ambiguous commit outcomes, beginning with `CreateDispatchIntent` as the only migrated operation in foundation PR 1.

**Architecture:** `PostgresStore` keeps concrete PostgreSQL ownership and gains one unexported nil-by-default fault hook. A narrow internal runner centralizes begin/commit lifecycle and stable operation labels; SQL and business invariants stay in their current modules. Rollout PRs migrate existing commit boundaries in dependency groups, add real PostgreSQL recovery rows to a common matrix, and finish only when a source guard finds no uncovered mutating boundary.

**Tech Stack:** Go 1.26, `pgx/v5`, PostgreSQL 17, Go `testing`, Go AST (`go/parser`, `go/ast`, `go/token`).

---

## File map

- Create `server/internal/researchrun/transaction.go`: private operation/point types, complete label registry, nil-safe fault hook, `ErrCommitOutcomeUnknown`, and begin/commit lifecycle runner.
- Create `server/internal/researchrun/transaction_test.go`: pure unit coverage for `after_begin`, `before_commit`, and `after_commit` ordering/error semantics.
- Create `server/internal/researchrun/transaction_guard_test.go`: AST scanner plus rollout-aware structural assertions. PR 1 scopes the production assertion to `CreateDispatchIntent`; PR 5 expands it to every mutating transaction while preserving exact read-only exceptions.
- Create `server/internal/researchrun/transaction_recovery_integration_test.go`: real PostgreSQL recovery matrix. PR 1 adds only `CreateDispatchIntent`; later PRs append rows.
- Modify `server/internal/researchrun/postgres.go`: add only `txFaultHook researchTxFaultHook` to `PostgresStore`; do not change the public constructor.
- Modify `server/internal/researchrun/postgres_tasks.go`: PR 1 migrates only `CreateDispatchIntent` and adds exact replay of its already-committed Attempt/outbox/Event.
- Modify transaction-owning files in PRs 2–4 only when their group is migrated: `postgres_dispatch.go`, `postgres_tasks.go`, `postgres.go`, `postgres_result.go`, `postgres_node_command.go`, `postgres_circuit.go`, `postgres_circuit_routing.go`, and `postgres_gate.go`.
- Do not modify `canonical_state.go` except in PR 5 if the exact read-only guard needs an explanatory comment; `CanonicalState` and `ListRunEvents` remain direct repeatable-read, read-only transactions.
- Modify `docs/superpowers/plans/2026-08-05-autonomous-research-system.md` and `docs/engineering-principles.md` only in final PR 5, after the full matrix and zero-uncovered guard are green.

## Stable operation registry

Define every label in PR 1 so later PRs select existing constants rather than invent strings during migration. Labels are private and describe commit boundaries:

```go
const (
    txOpRunCreate                    researchTxOperation = "run.create"
    txOpRunInitialize                researchTxOperation = "run.initialize"
    txOpTaskActivateReady            researchTxOperation = "task.activate_ready"
    txOpDispatchIntentCreate         researchTxOperation = "dispatch_intent.create"
    txOpDispatchIntentClaim          researchTxOperation = "dispatch_intent.claim"
    txOpDispatchIntentReschedule     researchTxOperation = "dispatch_intent.reschedule"
    txOpDispatchIntentFail           researchTxOperation = "dispatch_intent.fail"
    txOpDispatchIntentAcknowledge    researchTxOperation = "dispatch_intent.acknowledge"
    txOpAttemptAttachInbox           researchTxOperation = "attempt.attach_inbox"
    txOpAttemptFail                  researchTxOperation = "attempt.fail"
    txOpAttemptCancelRequest         researchTxOperation = "attempt.cancel_request"
    txOpAttemptCancelComplete        researchTxOperation = "attempt.cancel_complete"
    txOpResultAccept                 researchTxOperation = "result.accept"
    txOpControlTaskCreate            researchTxOperation = "control_task.create"
    txOpRunAwaitConfirmation         researchTxOperation = "run.await_confirmation"
    txOpRunComplete                  researchTxOperation = "run.complete"
    txOpRunResume                    researchTxOperation = "run.resume"
    txOpRunTransition                researchTxOperation = "run.transition"
    txOpRunSteer                     researchTxOperation = "run.steer"
    txOpNodeCommand                  researchTxOperation = "node_command.execute"
    txOpCircuitFailure               researchTxOperation = "circuit.record_failure"
    txOpCircuitSuccess               researchTxOperation = "circuit.record_success"
    txOpCircuitProbeClaim            researchTxOperation = "circuit.probe_claim"
    txOpCircuitProbeResolve          researchTxOperation = "circuit.probe_resolve"
    txOpExecutionTargetDefer         researchTxOperation = "execution_target.defer"
    txOpBudgetExhausted              researchTxOperation = "budget.exhausted"
    txOpAttemptRuntimeReconcile      researchTxOperation = "attempt.runtime_reconcile"
    txOpProjectionAcknowledge        researchTxOperation = "projection.acknowledge"
    txOpProjectionRetry              researchTxOperation = "projection.retry"
    txOpReconcileLeaseClaim          researchTxOperation = "reconcile_lease.claim"
    txOpReconcileLeaseRenew          researchTxOperation = "reconcile_lease.renew"
    txOpReconcileLeaseRelease        researchTxOperation = "reconcile_lease.release"
)
```

`Pause`, `Cancel`, `Archive`, and `MarkFailed` share `txOpRunTransition` because they share the single `transitionRun` business commit boundary. `nodeCommandContinueFork`, `nodeCommandRetry`, and `nodeCommandReassign` run inside the one transaction opened by `NodeCommand`, so they share `txOpNodeCommand`. `ReconcileAttempts` calls one `reconcileAttemptRuntime` transaction per Attempt, so the label identifies that actual commit boundary.

## Recovery proof used by every matrix row

Each PostgreSQL row must record a valid pre-state, canonical hash, and latest Event sequence. `before_commit` must return the injected error, leave hash/sequence unchanged, leave no operation-specific child row, and allow one identical retry. `after_commit` must satisfy `errors.Is(err, ErrCommitOutcomeUnknown)`, show that the committed state landed, recover using the operation’s stable request/status/reconcile mechanism, and prove that replay/reconcile creates no second canonical entity or Event. Rows must assert operation-specific counts, not only a global hash.

### Task 1: Foundation PR 1 plan and RED tests

**Files:**
- Create: `docs/superpowers/plans/2026-08-07-research-transaction-fault-injection.md`
- Create: `server/internal/researchrun/transaction_test.go`
- Create: `server/internal/researchrun/transaction_guard_test.go`
- Create: `server/internal/researchrun/transaction_recovery_integration_test.go`

- [ ] **Step 1: Save this five-PR implementation plan before production edits**

Confirm the plan names every transaction-owning file, stable label, recovery mechanism, and final Chapter C gate; do not mark Chapter C complete in PR 1.

- [ ] **Step 2: Write pure runner tests against the wished-for private API**

Use a fake transaction that embeds `pgx.Tx` and overrides `Commit`/`Rollback`, plus a recording hook. Tests must assert:

```go
func TestResearchTransactionAfterBeginFaultRollsBack(t *testing.T)
func TestResearchTransactionBeforeCommitFaultDefersToRollback(t *testing.T)
func TestResearchTransactionAfterCommitFaultReturnsUnknownOutcome(t *testing.T)
```

The first test calls `beginResearchTx(...)` with a fake begin function, observes begin → hook → rollback, and asserts no transaction is returned. The second runs `defer tx.Rollback(ctx)` around `commitResearchTx(...)`, observes hook → rollback with zero commits, and preserves the injected error. The third observes before-commit hook → commit → after-commit hook, asserts one commit and `errors.Is(err, ErrCommitOutcomeUnknown)` (and the injected error), then verifies plain commit errors are not wrapped with the sentinel.

- [ ] **Step 3: Write the narrow structural guard before migration**

Parse `postgres_tasks.go`, locate the `CreateDispatchIntent` receiver method, and report selector calls named `BeginTx` or `Commit`. Require exactly one `beginResearchTx` and two `commitResearchTx` selector calls after migration (the committed-replay exit and first-create exit) so an empty body cannot make the negative assertion vacuously green. Add an inline fixture containing direct `s.pool.BeginTx` and `tx.Commit` and assert the scanner reports both; this is the guard’s permanent mutation-test control.

- [ ] **Step 4: Write the PostgreSQL recovery matrix row before migration**

Add `TestCreateDispatchIntentTransactionRecovery` with `before_commit` and `after_commit` subtests. Each subtest creates an isolated run through existing `seedResearchRunFixture`/`testDispatchIntentInput`, cleans up by workspace/user, and checks canonical hash, latest sequence, Attempt count, outbox count, `task_dispatching` Event count, and Task status. The `after_commit` replay must return the original Attempt and Event IDs from the identical input.

- [ ] **Step 5: Run focused tests and preserve exact RED output**

Run:

```bash
cd server
env -u TEST_DATABASE_URL -u DATABASE_URL go test ./internal/researchrun \
  -run 'TestResearchTransaction|TestCreateDispatchIntentUsesResearchTransactionRunner' -count=1
```

Expected RED: compile failures for missing transaction runner symbols and/or the structural assertion reporting `CreateDispatchIntent`’s direct `BeginTx`/`Commit`. Do not set `TEST_DATABASE_URL`; the PostgreSQL row is reviewed and later exercised by CI.

### Task 2: Foundation PR 1 minimal runner implementation

**Files:**
- Create: `server/internal/researchrun/transaction.go`
- Modify: `server/internal/researchrun/postgres.go`
- Test: `server/internal/researchrun/transaction_test.go`

- [ ] **Step 1: Add private types, labels, hook, and sentinel**

Implement `researchTxOperation`, `researchTxFaultPoint`, all constants in the registry above, `researchTxFaultHook`, and:

```go
var ErrCommitOutcomeUnknown = errors.New("research transaction commit outcome unknown")
```

`ErrCommitOutcomeUnknown` is package-visible only; do not add it to HTTP/API contracts.

- [ ] **Step 2: Add the narrow lifecycle functions and store delegates**

The package-level `beginResearchTx` accepts a begin function so pure tests do not need PostgreSQL. It opens the transaction, calls `after_begin`, and rolls back before returning a hook error. The package-level `commitResearchTx` calls `before_commit`, commits, then calls `after_commit`; only a successful commit followed by hook failure wraps `ErrCommitOutcomeUnknown`. Store methods delegate with `s.pool.BeginTx` and `s.txFaultHook`.

- [ ] **Step 3: Add only the optional hook field to `PostgresStore`**

```go
type PostgresStore struct {
    pool        *pgxpool.Pool
    txFaultHook researchTxFaultHook
}
```

Keep `NewPostgresStore(pool)` unchanged and leave the hook nil in production.

- [ ] **Step 4: Run pure runner tests GREEN**

Run:

```bash
cd server
env -u TEST_DATABASE_URL -u DATABASE_URL go test ./internal/researchrun \
  -run '^TestResearchTransaction' -count=1
```

Expected: PASS with no database access.

### Task 3: Foundation PR 1 migrate only `CreateDispatchIntent`

**Files:**
- Modify: `server/internal/researchrun/postgres_tasks.go:95-282`
- Test: `server/internal/researchrun/transaction_guard_test.go`
- Test: `server/internal/researchrun/transaction_recovery_integration_test.go`

- [ ] **Step 1: Add exact committed-request replay inside the same run lock**

After `lockRunForMutation`, look up `research_dispatch_outbox` by `attempt_id`. If absent, continue the existing create path. If present, require the same session/task/Attempt/Agent/dispatch key/request hash and return `ErrResultConflict` on any mismatch. Load the existing Attempt with `attemptSelectSQL`/`scanAttempt` and load the existing `task_dispatching` Event by the request key; return those original objects. This check runs before current state-version/task-status validation so an `after_commit` retry can observe its own committed mutation without weakening first-attempt fencing.

- [ ] **Step 2: Replace only this method’s direct lifecycle calls**

Use:

```go
tx, err := s.beginResearchTx(ctx, txOpDispatchIntentCreate, pgx.TxOptions{})
// existing defer tx.Rollback(ctx), SQL, locks, and invariants remain
err = s.commitResearchTx(ctx, txOpDispatchIntentCreate, tx)
```

Do not migrate any neighboring method in this PR.

- [ ] **Step 3: Run structural and unit tests GREEN**

Run:

```bash
cd server
env -u TEST_DATABASE_URL -u DATABASE_URL go test ./internal/researchrun \
  -run 'TestResearchTransaction|TestCreateDispatchIntentUsesResearchTransactionRunner|TestTransactionGuardDetectsDirectBoundary' -count=1
```

Expected: PASS; the production assertion covers only `CreateDispatchIntent`, while the inline direct-boundary mutation fixture still proves the guard can fail.

- [ ] **Step 4: Do not run the PostgreSQL recovery test locally**

Compile it as part of package tests with `TEST_DATABASE_URL` unset. The GitHub backend job supplies the isolated `multica_research` PostgreSQL database and is the execution evidence for `before_commit` rollback/retry and `after_commit` unknown-outcome replay.

### Task 4: Foundation PR 1 verification, commit, and PR

**Files:** all PR 1 files above.

- [ ] **Step 1: Format and run permitted local checks**

```bash
gofmt -w server/internal/researchrun/transaction.go \
  server/internal/researchrun/transaction_test.go \
  server/internal/researchrun/transaction_guard_test.go \
  server/internal/researchrun/transaction_recovery_integration_test.go \
  server/internal/researchrun/postgres.go \
  server/internal/researchrun/postgres_tasks.go
cd server
env -u TEST_DATABASE_URL -u DATABASE_URL go test ./internal/researchrun -count=1
go test -run '^$' ./internal/researchrun
go vet ./internal/researchrun
go build ./internal/researchrun
cd ..
git diff --check
git diff --exit-code origin/dev -- server/pkg/db/migrations server/pkg/db/queries
```

No local database, service, migration, or network-timing test is permitted.

- [ ] **Step 2: Review scope**

Confirm no public API, migration, Prompt/Result, giant Store interface, runtime config, external side-effect ordering, or unrelated file changed. Confirm every non-test production transaction except `CreateDispatchIntent` remains untouched.

- [ ] **Step 3: Commit atomically**

```bash
git add docs/superpowers/plans/2026-08-07-research-transaction-fault-injection.md \
  server/internal/researchrun/transaction.go \
  server/internal/researchrun/transaction_test.go \
  server/internal/researchrun/transaction_guard_test.go \
  server/internal/researchrun/transaction_recovery_integration_test.go \
  server/internal/researchrun/postgres.go \
  server/internal/researchrun/postgres_tasks.go
git commit -m "feat(research): add transaction fault runner foundation"
```

- [ ] **Step 4: Push and create a ready PR to `dev`**

Push `feat/research-transaction-runner-foundation` with upstream and create a non-draft PR. The PR body must include Chapter C mapping, exact RED output, local no-DB checks, PostgreSQL CI recovery cases, unchanged schema/Prompt/Result/API, risks, and next rollout group. Do not merge.

### Task 5: Rollout PR 2 — dispatch, outbox, cancellation, and task activation

**Files:**
- Modify: `server/internal/researchrun/postgres_dispatch.go`
- Modify: `server/internal/researchrun/postgres_tasks.go`
- Modify: `server/internal/researchrun/postgres.go`
- Modify: `server/internal/researchrun/transaction_guard_test.go`
- Modify: `server/internal/researchrun/transaction_recovery_integration_test.go`

- [ ] **Step 1: Add RED matrix rows before migration**

Cover `ActivateReadyTasks`, `ClaimDispatchIntents`, `RescheduleDispatchIntent`, `FailDispatchIntent`, `AcknowledgeDispatchIntent`, `AttachInboxTask`, `FailAttempt`, `MarkCancellationsRequested`, and `CompleteCancellations`. Use stable outbox ID/token/request hash, Attempt ID, cancellation Inbox ID, and existing idempotent terminal observations as recovery mechanisms.

- [ ] **Step 2: Migrate boundaries with existing labels**

Map methods to `txOpTaskActivateReady`, `txOpDispatchIntentClaim`, `txOpDispatchIntentReschedule`, `txOpDispatchIntentFail`, `txOpDispatchIntentAcknowledge`, `txOpAttemptAttachInbox`, `txOpAttemptFail`, `txOpAttemptCancelRequest`, and `txOpAttemptCancelComplete`. Preserve all defer rollback, lock ordering, `SKIP LOCKED`, fencing, and external Inbox ordering.

- [ ] **Step 3: Expand guard scope only to migrated functions**

The guard must reject direct `BeginTx`/`Commit` in the PR 1 method plus the exact PR 2 method list. Do not claim package-wide coverage.

- [ ] **Step 4: Run recovery and full backend gates in CI**

Require operation-specific uniqueness for Attempt/outbox/Event/cancellation rows and no external compensation after `after_commit` unknown outcome.

### Task 6: Rollout PR 3 — Result, failure/repair, and node commands

**Files:**
- Modify: `server/internal/researchrun/postgres_result.go`
- Modify: `server/internal/researchrun/postgres_tasks.go`
- Modify: `server/internal/researchrun/postgres_node_command.go`
- Modify: `server/internal/researchrun/transaction_guard_test.go`
- Modify: `server/internal/researchrun/transaction_recovery_integration_test.go`

- [ ] **Step 1: Add RED matrix rows before migration**

Cover `AcceptResult`, `CreateControlTask`, and `NodeCommand`. `FailAttempt` is already migrated in PR 2; its nested `failAttemptTx` target-repair settlement remains in that same boundary. Cover node actions `continue`, `fork`, `retry`, and `reassign` as cases under the single `txOpNodeCommand` boundary.

- [ ] **Step 2: Prove ambiguous result recovery through existing request/result hashes**

`AcceptResult` replays identical accepted results and rejects same ID/different payload. Assert no duplicate Source, Observation, Claim, Report, Evaluation, Decision, repair, Task, or Event for the applicable fixture.

- [ ] **Step 3: Prove control/node recovery through stable client keys**

Use control-task objective/target keys and node-command semantic request hashes. One-time actions may recover by reading current Run/Task/Attempt state rather than executing a second transition.

- [ ] **Step 4: Migrate and expand only the migrated-function guard**

Use `txOpResultAccept`, `txOpControlTaskCreate`, and `txOpNodeCommand`; keep helper SQL and state-machine logic in place.

### Task 7: Rollout PR 4 — circuit, lifecycle, gate, reconcile, and projection

**Files:**
- Modify: `server/internal/researchrun/postgres.go`
- Modify: `server/internal/researchrun/postgres_tasks.go`
- Modify: `server/internal/researchrun/postgres_circuit.go`
- Modify: `server/internal/researchrun/postgres_circuit_routing.go`
- Modify: `server/internal/researchrun/postgres_gate.go`
- Modify: `server/internal/researchrun/transaction_guard_test.go`
- Modify: `server/internal/researchrun/transaction_recovery_integration_test.go`

- [ ] **Step 1: Add RED matrix rows for remaining mutating boundaries**

Cover `CreateRun`, `InitializeRun`, `SetAwaitingConfirmation`, `Complete`, `Resume`, `transitionRun` (`Pause`/`Cancel`/`Archive`/`MarkFailed` cases), `Steer`, `RecordCircuitFailure`, `RecordCircuitSuccess`, `ClaimCircuitProbe`, `ResolveCircuitProbe`, `DeferTaskForExecutionTarget`, `RecordBudgetExhausted`, `reconcileAttemptRuntime`, `MarkEventProjected`, `MarkEventProjectionFailed`, `ClaimRun`, `RenewRunLease`, and `ReleaseRun`.

- [ ] **Step 2: Use the existing durable recovery fact for each class**

Lifecycle uses current Run status/Event key; circuit uses transition uniqueness and probe token/generation; reconcile uses lease token/generation and current Attempt state; projection uses Event projected/retry columns; budget/gate uses Decision/Event idempotency; execution-target deferral uses Task/Event state.

- [ ] **Step 3: Migrate every remaining boundary with the declared labels**

Map methods to their same-named registry constants. `Pause`/`Cancel`/`Archive`/`MarkFailed` continue to share `txOpRunTransition`; `RenewRunLease` and `ReleaseRun` move their current single auto-commit statements into runner-owned transactions labeled `txOpReconcileLeaseRenew` and `txOpReconcileLeaseRelease`. Preserve SQL, return values, row-count checks, lock order, and fencing predicates exactly.

- [ ] **Step 4: Preserve external-side-effect ordering**

Do not wrap Dispatcher, Projector, WebSocket, Runtime, or cancellation calls. Canonical intent commits first; external work remains between intent and acknowledgement transactions. An unknown outcome never triggers compensating cancellation by itself.

- [ ] **Step 5: Expand guard to the exact PR 4 migrated methods**

Keep `CanonicalState` and `ListRunEvents` outside the mutating registry as exact read-only exceptions.

### Task 8: Rollout PR 5 — complete registry/matrix audit and Chapter C exit

**Files:**
- Modify: `server/internal/researchrun/transaction_guard_test.go`
- Modify: `server/internal/researchrun/transaction_recovery_integration_test.go`
- Modify: `docs/superpowers/plans/2026-08-05-autonomous-research-system.md`
- Modify: `docs/engineering-principles.md`

- [ ] **Step 1: Audit all production boundaries by AST**

Scan every non-test Go file in `server/internal/researchrun`. Direct `BeginTx` and `Commit` are forbidden except exact receiver/function identities `(*PostgresStore).CanonicalState` and `(*PostgresStore).ListRunEvents`, both of which must prove `pgx.ReadOnly` + `pgx.RepeatableRead`. Reject file-level allowlists.

- [ ] **Step 2: Prove operation registry completeness**

The guard must collect every `beginResearchTx`/`commitResearchTx` label pair, reject unregistered literals, reject begin/commit label mismatch, reject duplicate lifecycle ownership, and compare the observed mutating operation set with the declared registry.

- [ ] **Step 3: Run every PostgreSQL recovery matrix row**

For every label, require `before_commit` rollback/identical retry and `after_commit` committed-state detection/idempotent or reconcile recovery. Compare canonical state hash and Event sequence with a no-fault execution and assert operation-specific row uniqueness.

- [ ] **Step 4: Mutation-test the final guard**

Temporarily add direct `BeginTx` and `Commit` in a non-allowed mutating fixture/function, capture guard RED, remove the mutation, and capture GREEN. Also mutate a label mismatch and prove the registry check fails.

- [ ] **Step 5: Update authoritative completion evidence only now**

Mark Chapter C’s remaining crash/transaction item complete in `2026-08-05-autonomous-research-system.md`, recording PR links, matrix counts, mutation RED evidence, PostgreSQL CI run, and zero uncovered commits. Add an executable engineering-principle entry naming the AST guard and matrix. Do not reduce or mark D–N complete.

- [ ] **Step 6: Run final gates**

Run full Go unit/PostgreSQL/race/vet/build/diff checks and all unchanged V1–V5 golden Prompt/Result/behavior tests in CI. Chapter C exits only if every registry row passes and the structural audit reports zero uncovered mutating boundaries.

## Explicit non-goals for every rollout PR

- No public API or constructor option.
- No database migration or schema change.
- No Prompt, Result, Skill, source-map, or frontend change.
- No generic repository, giant Store interface, or shallow persistence interface.
- No network kill, process timing, sleep-based crash test, or service test.
- No transaction wrapping around external Dispatcher, Projector, WebSocket, or Runtime calls.
- No compensating cancellation based solely on `ErrCommitOutcomeUnknown`.
- No unrelated refactor and no claim that Chapter C is complete before PR 5.
