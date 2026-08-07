# Research Run Transaction Fault Injection Design

Date: 2026-08-07

Status: proposed for implementation

Scope: autonomous research backend plan, Chapter C formal exit condition only. This design adds deterministic transaction-boundary fault injection and recovery evidence; it does not change Research Run product semantics, Prompt/Result schemas, API payloads, or PostgreSQL schema.

## 1. Problem

Research Run already has strong tests for concrete crash windows: dispatch outbox replay, external Inbox commit before Research acknowledgement, lease expiry/takeover, reconcile generation fencing, probe-owner fencing, target repair idempotency, stale Result isolation, cancellation races, and canonical state/event replay.

The Chapter C exit condition is stronger: every canonical transaction commit point must be fault-injectable and recoverable. The current `PostgresStore` calls `pool.BeginTx` and `tx.Commit` directly across its PostgreSQL modules. Tests can inject adapter failures and manipulate leases, but they cannot deterministically produce:

1. failure after begin but before commit, proving complete rollback;
2. failure immediately before commit, proving retry starts from unchanged canonical state;
3. an ambiguous outcome where PostgreSQL committed successfully but the caller receives an error, proving stable idempotency/reconcile converges without duplicate Task, Attempt, Source, Claim, Event, repair, or Decision.

Network killing or timing a backend termination can approximate these states but is nondeterministic and unsuitable for a permanent CI gate.

## 1.1 No Scope Reduction

This design does not remove, defer indefinitely, substitute, or weaken any item in `2026-08-05-autonomous-research-system.md`. Chapters D–N and completion criteria 1–19 remain mandatory exactly as written. “Scope: Chapter C” means only that this document defines the missing Chapter C verification infrastructure; after C passes, implementation continues through D, E, F, G, H, I, J, K, L, M and N in dependency order.

The transaction runner is test and reliability infrastructure, not a replacement for Artifact Passports, Inquiry Graph, Corpus lineage, Integration, Dispute/Deliberation, Exploration, dynamic teams, report lineage, monitoring, Strategy evolution, projection, migration, or production acceptance. No Prompt-only substitute, fake fixture capability, reduced V6 schema, or skipped system evaluation is permitted.

## 2. Chosen Architecture

Use one narrow internal transaction runner owned by `researchrun.PostgresStore`.

`PostgresStore` remains the concrete production implementation. No Store-wide interface, Handler seam, public constructor option, or runtime configuration is introduced. The only additional production field is an unexported optional fault hook whose nil value has zero behavioral effect.

Conceptual API:

```go
type researchTxOperation string
type researchTxFaultPoint string

const (
    txAfterBegin   researchTxFaultPoint = "after_begin"
    txBeforeCommit researchTxFaultPoint = "before_commit"
    txAfterCommit  researchTxFaultPoint = "after_commit"
)

type researchTxFaultHook func(
    context.Context,
    researchTxOperation,
    researchTxFaultPoint,
) error

func (s *PostgresStore) beginResearchTx(
    ctx context.Context,
    operation researchTxOperation,
    options pgx.TxOptions,
) (pgx.Tx, error)

func (s *PostgresStore) commitResearchTx(
    ctx context.Context,
    operation researchTxOperation,
    tx pgx.Tx,
) error
```

The runner is deliberately not a generic repository abstraction. SQL and business invariants remain in their existing modules; only transaction lifecycle ownership moves to one place.

## 3. Fault Semantics

### 3.1 `after_begin`

The real PostgreSQL transaction is opened, then the hook fails. The runner rolls it back before returning. This proves callers do not leak an open transaction or perform external work from a failed transaction setup.

### 3.2 `before_commit`

All transaction SQL has run, but the hook fails before `tx.Commit`. The deferred rollback removes every mutation. The operation returns the injected error. The test verifies the canonical state hash and Event sequence are unchanged, then retries the same request and succeeds once.

### 3.3 `after_commit`

`tx.Commit` succeeds first. The hook then returns an error wrapped with a stable sentinel:

```go
var ErrCommitOutcomeUnknown = errors.New("research transaction commit outcome unknown")
```

This models “database committed, response/connection was lost.” The caller must not compensate or cancel an external fact based only on this error. Recovery uses the operation’s existing idempotency key, frozen outbox request, durable status, or reconcile path. Repeating the same request must return replay/success or observe the already-committed state without creating another canonical entity.

The hook is test-only in use. Production never configures it.

## 4. Operation Registry

Every mutating transaction receives a stable operation label. Labels describe business commit boundaries, not filenames. Initial complete registry:

- run initialize/start;
- dispatch intent create, claim, reschedule, fail, acknowledge;
- ready-task activation and dependency blocking;
- Attempt runtime reconcile and cancellation mark/complete;
- Result acceptance;
- Task failure / target repair settlement;
- Run pause, resume, cancel, archive, fail, steer;
- control-task creation and awaiting-confirmation transition;
- budget decision and delivery gate mutations;
- node command continue/fork/retry/reassign;
- execution circuit failure, success, probe claim, probe resolution;
- execution-target deferral;
- reconcile lease claim, renew, release and canonical fenced writes;
- projection acknowledgement/retry mutations.

Read-only repeatable-read snapshots (`CanonicalState`, Event listing) are excluded because they do not create a canonical commit ambiguity. Their read consistency remains tested separately.

## 5. Structural Enforcement

A source-level guard test scans non-test files in `server/internal/researchrun` and rejects new direct mutating transaction boundaries:

- `s.pool.BeginTx(...)` outside the transaction runner and approved read-only snapshot functions;
- `tx.Commit(...)` outside `commitResearchTx` and approved read-only snapshot functions.

The allowlist names exact read-only functions, not files, so a new write transaction cannot hide beside snapshot code. This makes transaction-runner coverage structural rather than dependent on review memory.

The guard itself must be mutation-tested once: add a temporary direct `BeginTx`/`Commit` fixture, observe failure, then remove it.

## 6. Recovery Matrix

Real PostgreSQL tests use the existing isolated `TEST_DATABASE_URL`. For each registered mutating operation:

1. establish a valid pre-state and record canonical hash + latest Event sequence;
2. inject `before_commit` once;
3. assert the operation errors, canonical hash and sequence remain unchanged, and no partial child rows exist;
4. retry the identical request without injection and assert one successful logical mutation;
5. establish a fresh valid pre-state;
6. inject `after_commit` once;
7. assert `errors.Is(err, ErrCommitOutcomeUnknown)`;
8. read canonical state to determine that commit landed;
9. repeat/reconcile the identical request;
10. assert canonical hash does not change on replay, Event IDs/sequences remain unique, and operation-specific entity counts remain one.

Operations that intentionally cannot be directly replayed after commit (for example one-time lifecycle commands) recover through `GetRun`/reconcile rather than treating the second command as a new mutation. The matrix records the expected recovery mechanism per operation.

## 7. External Side-Effect Rule

The transaction hook never wraps external Dispatcher, Projector, WebSocket, or Runtime calls. Existing ordering remains:

1. commit canonical intent/outbox;
2. perform external action;
3. commit acknowledgement;
4. project committed Event.

An `after_commit` error must therefore be treated as unknown outcome, not proof of rollback. Existing outbox and stable request keys are the recovery mechanism. No new dual-write or compensating cancellation path is added.

## 8. Error Handling and Safety

- Hook errors are bounded test values and never persisted.
- `ErrCommitOutcomeUnknown` exposes no SQL, credentials, or payload.
- A hook fires at most once per configured `(operation, point)` in tests, using a mutex/atomic counter, so retry can proceed.
- Context cancellation and real PostgreSQL commit errors retain their existing errors; only a successful commit followed by injected failure gets the unknown-outcome sentinel.
- Panic is not used for fault injection.
- Workspace filtering, reconcile fencing, state-machine triggers, and idempotency checks remain inside the same transactions.

## 9. Rollout

Implementation is split into small PRs:

1. transaction runner, sentinel, operation labels, structural guard, and one proving operation (`CreateDispatchIntent`);
2. dispatch/outbox and cancellation operations;
3. Result/failure/repair and node-command operations;
4. circuit, gate, lifecycle, reconcile and projection operations;
5. full operation registry/matrix audit, plan evidence update, and Chapter C completion.

No intermediate PR marks Chapter C complete. The final PR does so only after the structural guard reports zero uncovered mutating commits and the PostgreSQL recovery matrix is green.

## 10. Acceptance Criteria

1. Production behavior with nil hook is byte/SQL-semantics compatible except transaction lifecycle calls are centralized.
2. Every mutating Research Run transaction has a stable operation label and uses the runner.
3. Structural guard rejects direct new write `BeginTx`/`Commit` calls.
4. `before_commit` proves rollback and successful identical retry for every operation.
5. `after_commit` proves committed-state detection and idempotent/reconcile convergence for every operation.
6. Canonical hash/Event sequence and operation-specific counts prove no partial or duplicate canonical entities.
7. Existing real PostgreSQL, race, golden Prompt/Result, behavior, migration, handler, vet and build gates remain green.
8. No public API, schema, Prompt, Result contract, giant Store interface, shallow persistence interface, or network-timing test is introduced.
