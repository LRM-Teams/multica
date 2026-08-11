# Raft Reminder Parity and Agent Card — Product Contract (V3)

- Date: 2026-07-22
- Parent: Multica task #654
- Replaces the delivery scope in `2026-07-08-agent-reminders-design.md`; that document remains the historical V1 implementation baseline.
- Current code baseline: merged PR #870 provides the Reminder definition/occurrence/lifecycle model, permissions, CLI/API, and human read API. The Agent Card frontend and user-facing Reminder Activity fast-follow remain unshipped. The shared server scheduler is explicitly **not** the final parity architecture and must not be released as the normal Reminder trigger path.
- 2026-08-11 correction: Raft parity keeps timer projection/fire-attempt on the dedicated `reminder.*` control plane, but the post-commit resident input is best-effort and transient. Multica carries that input on Workspace Runner `agent:deliver` with `kind:"reminder", transient:true`; it is not a durable Message wake and there is no `reminder.owner_input` event.

## Outcome

Multica Reminder must match both the user-visible Raft primitive and Raft's public daemon scheduling responsibility boundary, not merely produce equivalent delayed-task outcomes:

> A Reminder is an agent-authored, persistent, observable, snoozable, updateable, cancelable wake signal anchored to a message or thread. It wakes its author, and its human-facing projection is read-only.

The merged V2 work supplies most of the data, permission, lifecycle, and read-model commitments, but it is not full parity because it retained a server-side due-row scanner. V3 moves normal timing to the owner daemon using the Raft `snapshot -> versioned cache -> local timer -> fire_attempt` contract. The server remains the durable authority and commit boundary; it does not poll for due Reminders as the normal trigger.

Live Raft probe on this task (Reminder `ffee888c`, 2026-07-22 09:25–09:26 CST) confirmed the state boundary: the lifecycle log recorded `SCHEDULED` then `FIRED`, the one-shot moved to `fired`, and the author was woken at the anchor. Reading the thread afterward returned only ordinary discussion messages, not the fire notice as conversational history. Therefore V3 treats the receipt as a visible system projection and the lifecycle ledger as the durable source of truth.

## Product boundaries

1. **The Agent owns the Reminder.** Only the author Agent may schedule, snooze, update, cancel, or inspect its full CLI lifecycle log. Ownership never transfers.
2. **Humans observe; they do not operate.** Agent Card is read-only in V3. It must not expose Schedule, Snooze, Update, Cancel, or Dismiss actions. A human who wants a change asks the Agent.
3. **Every Agent-created Reminder has an immutable anchor.** Creation requires a `message_id` resolved to a readable channel message or thread. Schedule edits never retarget the anchor.
4. **Reminder inputs are author-only.** Fire attempts one transient resident input for the author. It must not wake other Agents merely because they can see the anchored surface.
5. **Reminder is not Autopilot.** Reminder is a self-owned follow-up signal tied to conversational context. Autopilot is a standing job/task. Scheduler mechanics may be reused; data, permissions, and surfaces must not be merged.
6. **Fired is not completed work.** `fired` means the platform emitted the due wake and recorded it. It does not claim that the Agent produced the intended report, message, or external result.
7. **The owner daemon owns time.** The server owns durable definitions, authorization, and commit-time idempotency; the daemon owns the versioned cache and timer. A periodic server scan is not an allowed normal fire path.
8. **One schedule has one authority at each boundary.** Server mutations advance a monotonic Reminder version and project it to the daemon. The daemon never invents or edits durable Reminder state; the server never races a second timer against the daemon.

## Raft parity matrix

| Capability | Raft behavior to match | Merged V2 state | V3 requirement |
|---|---|---|---|
| schedule | One-shot or recurring; Agent anchor required | Grammar and anchor exist; server owns timer | Preserve API; project the committed version to daemon |
| list | Own reminders; scheduled and fired are observable | Exists with recurrence/timezone/next/last | Preserve |
| snooze | Scheduled or fired reminder can be moved | Exists with recurring-next semantics | Advance version and project upsert |
| update | Title, next fire, or cadence can change | Exists; exactly one field per mutation | Advance version and project upsert/cancel as applicable |
| cancel | Stops future fires | Exists for one-shot and recurring definitions | Advance version and project cancel |
| log | Immutable lifecycle per Reminder | Dedicated lifecycle log exists | Preserve |
| recurrence | `every:15m`, `every:2h`, `every:1d`, `daily@09:00`, `weekly:mon,fri@09:00` | Grammar and validation exist | Preserve; daemon times each projected occurrence |
| timezone | Daily/weekly resolve in caller IANA timezone and lock at creation | Persisted Reminder-lifetime timezone exists | Preserve |
| wake | Fires to author only; transient resident input | Server scheduler commits author wake | Move trigger to daemon; after commit send one best-effort transient input through Workspace Runner |
| human UI | Agent detail/profile shows scheduled and fired; read-only | Human read API exists; frontend #656 is unshipped | Ship Agent Card Reminder tab after scheduler parity |
| timer owner | Owner daemon holds a versioned cache and local timer | Server scans due rows | Move the normal trigger path to the owner daemon; remove the server due-row scan |
| reconnect | Daemon requests an owner-scoped snapshot and rebuilds timers | Missing | Snapshot every running/idle Agent on connect/reconnect; reject cross-owner entries |
| ordering | Versioned upsert/cancel rejects stale delivery | Missing | Persist a monotonic version and fence daemon cache mutations and server fire attempts |
| fire transport | Daemon sends `reminder.fire_attempt` with owner, Reminder ID, version, and client fire time | Server fires directly | Add the Raft-aligned wire path; server validates and commits the occurrence idempotently |

## Schedule semantics

### One-shot

- Exactly one of `delay_seconds` or `fire_at` is required.
- A successful fire makes the definition terminal `fired`.
- Snoozing a fired one-shot re-arms the same Reminder ID and appends a `snoozed` lifecycle event.

### Recurring

Supported expressions are intentionally the Raft-visible grammar:

- `every:<N>m`, `every:<N>h`, `every:<N>d`
- `daily@HH:MM`
- `weekly:<weekday[,weekday...]>@HH:MM`

Rules:

- `daily` and `weekly` resolve Scheduling timezone at schedule time from `agent_task_queue.initiator_user_id -> user.timezone`. Accept only a valid IANA name; null, empty, or invalid values deterministically fall back to `UTC`. Do not read runtime/machine/browser timezone and do not add a public `--timezone` flag. Persist the resolved value and keep it fixed for the Reminder's lifetime.
- DST keeps the calendar meaning. A spring-forward wall time that does not exist fires at the first valid instant after the gap on that local date. A fall-back overlap fires at most once per eligible local date: an initial schedule created between the repeated wall-clock instants may select the still-future instant, but after that date's occurrence fires, cadence advancement skips the duplicate candidate and moves to the next eligible local date.
- The Reminder definition remains `scheduled` after each successful recurring fire. Each occurrence is an immutable log record; `next_fire_at` advances from the cadence rule.
- Snooze changes only `next_fire_at` for the current occurrence. The following occurrence resumes the stored cadence.
- Updating `cadence` replaces the cadence from the update time and computes a new `next_fire_at`; title-only updates do not affect timing. Updates never reinterpret an already locked timezone. If an interval/one-shot Reminder with no calendar timezone is first changed to `daily` or `weekly`, resolve and lock timezone from that update task's initiator using the same rule.
- Updating with a one-shot schedule (`fire_at` / `delay_seconds`) replaces any recurring cadence: clear cadence and cadence-next state so the definition fires once. Preserve an already acquired calendar timezone as a hidden Reminder-lifetime lock, so daily(A zone) → one-shot → daily still uses A rather than the later updater's zone. The database constraint must therefore allow a one-shot definition to retain a non-null locked timezone.
- Cancel is terminal and prevents future occurrences.
- A recurring Reminder may not create overlapping wakes. The server does not project the next versioned upsert until the current fire attempt commits and the next occurrence is durable.

## State and lifecycle

Keep a mutable Reminder definition plus an immutable lifecycle ledger. Every durable mutation also advances a monotonic `version`; versions never reset or repeat for a Reminder ID.

Definition states:

- `scheduled`: future occurrence exists.
- `firing`: one accepted fire attempt owns the current occurrence claim.
- `fired`: terminal one-shot definition.
- `cancelled`: terminal; no future occurrence.

Lifecycle event types:

- `scheduled`
- `fired`
- `snoozed`
- `updated`
- `cancelled`
- `dismissed` only if the platform later adds a distinct dismissal operation; do not invent a UI action in V3.

Each event records reminder ID, event time, actor, previous/next fire time when applicable, cadence/timezone snapshot, occurrence ID, and resulting state. The ledger is append-only and is the source for `reminder log`; Activity remains the user-facing narrative projection, not a substitute for the ledger.

## Daemon scheduling, fire, and recovery contract

### Server-to-daemon projection

Transport remains Multica's existing WebSocket protocol envelope `{type, payload}`. This format is explicitly approved and is not required to copy Raft's top-level discriminated JSON byte-for-byte. Reminder event names, payload fields, ownership, version fencing, snapshot, timer, and fire-attempt semantics still match Raft. For example, Multica sends `{type:"reminder.upsert", payload:{reminder:{...}}}` where Raft places `reminder` beside `type` at the top level.

Snapshot owner discovery comes only from a daemon-local running/idle AgentManager, matching Raft. It must be an explicit Agent-session lifecycle registry, not a temporary projection of currently executing tasks and not a server-returned owner inventory. Running-to-idle transitions keep the owner registered; local owner creation adds it; terminal removal clears it and that owner's cache. Daemon restart rebuilds the registry from locally recoverable Agent session/config state before requesting one snapshot per owner.

1. A successful schedule, update, or snooze transaction advances the Reminder's monotonic `version`. After commit, the server sends the owner daemon `reminder.upsert` containing the complete timer job, including Reminder ID, owner Agent ID, version, and fire time.
2. A successful cancel or terminalization transaction advances `version`. After commit, the server sends `reminder.cancel {reminderId, version}`. A stale cancel may not remove a newer cached timer.
3. On daemon connect/reconnect, every running or idle Agent requests `reminder.snapshot`. The server returns exactly that Agent's active Reminder jobs. The daemon replaces only that owner's cache entries and rejects any snapshot job whose owner differs from the requested Agent.
4. Cache ordering matches Raft: an upsert whose version is less than or equal to the cached version is ignored; a cancel older than the cached version is ignored. Snapshot is the authoritative reconnect projection for that owner.
5. The daemon owns the local timer. When it becomes due, it removes the cached timer and sends `reminder.fire_attempt` with owner Agent ID, Reminder ID, version, and client fire timestamp. The daemon does not create a receipt, lifecycle row, or wake itself.

### Server fire commit

1. The server accepts only a fire attempt whose owner, active definition, and version still match durable state. Stale, cancelled, terminal, cross-owner, and duplicate attempts are harmless no-ops with a canonical result.
2. The accepted attempt atomically claims one occurrence and emits one visible fire receipt in the anchored channel/thread as a typed, localized system event. It is not ordinary conversational content: it must not enter message search, unread counts, quote/reply, or reactions. When the anchor message is unavailable, both the canonical fallback and localized projection explicitly say `Anchor unavailable` without metadata. Durable observability lives in the lifecycle ledger and Agent Card history. The receipt itself dispatches zero ambient or directed wakes to other Agents.
3. After that transaction commits, the server attempts one owner-only transient resident input with Reminder ID/title, exact target, anchor context, cadence/occurrence data, and the normal reply-target contract. It uses Workspace Runner `agent:deliver` with `kind:"reminder", transient:true`; it does not enter MessageCoordinator, cursor/replay/ACK, or Activity.
4. The same transaction appends one `fired` lifecycle event and either terminalizes the one-shot definition or advances the recurring definition, increments version, and projects the next `reminder.upsert` after commit.

### Recovery boundary

- Daemon restart or reconnect rebuilds local timers from an owner-scoped snapshot; a due Reminder is attempted immediately after restoration.
- A Reminder scheduled while its owner daemon is offline remains durable on the server and enters the daemon cache on the next snapshot. If the owner becomes busy, compacting, or unavailable after fire commit, the transient resident input is discarded and is not replayed.
- A lost, duplicate, or replayed fire attempt cannot duplicate the occurrence, receipt, lifecycle event, or wake. Reconnect snapshot reprojects every still-active definition whose attempt did not commit.
- The server must not run a periodic due-row scan as the normal trigger or race its own timer against the daemon. Any temporary migration bridge must be separately approved, observable, time-bounded, and removed before parity release.

Correctness gates:

- Receipt projection and lifecycle persistence must be idempotent by occurrence ID. The post-commit transient input is attempted at most once for the winning fire commit and is never staged for retry.
- Restart/reconnect snapshot recovers timers, and durable fire-attempt idempotency recovers partial commits. An occurrence already associated with a durable task must never be re-armed into a duplicate wake.
- If the Agent is offline, busy, or compacting at post-commit admission, the transient input is discarded; reconnect does not replay it.
- Before reading anchor content or creating receipt/session/task state, revalidate that the owning Agent is still an active member of the anchor channel. Hold the exact membership row (and channel/Agent eligibility) through the fire transaction commit so membership removal serializes either before fire (terminalize with zero receipt/task/wake) or after a committed fire; a stale member may never receive an anchor excerpt or wake.
- Anchor availability is one server-owned predicate shared by fire prompt, typed receipt, and human read projection. For a thread, both the root and anchored reply must exist, be undeleted, and remain authorized. If either message was deleted, keep the still-valid channel Reminder and fire with `anchor_available=false`, no excerpt/target metadata, and an explicit unavailable-anchor marker. Do not silently cancel it or report the deleted thread root as available.
- If the channel is archived/deleted, or the Agent is archived/deleted, end the definition without a wake and append an explicit terminal lifecycle reason.
- Mute does not suppress an Agent's own Reminder wake.
- The existing active cap of 25 may remain as a bounded-load guard, but it is an implementation safety limit rather than a new user-facing Reminder concept.

## CLI and agent transport

Canonical command family remains `multica reminder` and must expose:

```text
multica reminder schedule --title ... (--delay-seconds N | --fire-at ISO | --repeat RULE) --message-id ID
multica reminder list [--status scheduled|fired|cancelled|all]
multica reminder snooze --id ID (--delay-seconds N | --fire-at ISO)
multica reminder update --id ID [--title ...] [--fire-at ISO | --cadence RULE]
multica reminder cancel --id ID
multica reminder log --id ID
```

Transport requirements:

- Agent task credentials scope every mutation/read to the task's Agent and workspace.
- Short-ID resolution remains Agent-scoped and rejects ambiguous prefixes.
- Agent-created schedule always sends the anchor explicitly. The CLI requires `--message-id`, and the handler rejects an omitted `message_id`; neither layer infers an anchor from task prompt text.
- List/log output provides stable machine-readable JSON plus human-readable canonical text where the CLI convention supports it.
- Capability/runtime brief lists all six operations and recurring syntax only when both the connected server and daemon advertise the versioned Reminder transport/cache capability.

## Agent Card Reminder tab

Add `reminders` to the existing `AgentSidePanel` / mobile profile-page tab set. It is the same tab and body across docked and page variants, not a separate route or a hover-card expansion.

Visibility:

- Reuse the existing read-only Agent inspection boundary: workspace members may view Reminders for workspace-visible Agents; private Agents remain owner/authorized-inspector only.
- The server enforces visibility independently of the frontend.
- An anchor link is rendered only when the viewer can read that channel/thread. Otherwise show `Anchor unavailable` without leaking channel name, message content, or participants.

Content:

- `Upcoming`: scheduled definitions ordered by `next_fire_at`.
- `History`: recent fired occurrences ordered newest first, cursor-paginated.
- Each row shows title, status, one-shot/recurring, next or last fire time, cadence, locked timezone when applicable, and safe anchor link.
- The tab uses the viewer's locale for display while making the locked schedule timezone explicit; it must not silently render a daily schedule as if it were defined in the viewer's timezone.
- Provide loading, empty, error/retry, and inaccessible-anchor states.
- Live updates invalidate the full per-Agent Reminder query after schedule/fire/snooze/update/cancel and fire commit/terminalization. Reconnect refetches the query. A polling/manual refresh fallback is not the normal live contract.
- No mutation buttons, kebab actions, inline schedule form, or human-owned reminder model in V3.

Suggested human read API:

- `GET /api/agents/{agentId}/reminders?status=scheduled|fired|all&cursor=...&limit=...` returns one page `{definitions, occurrences, limit, has_more, next_cursor}`. Definitions retain their own lifecycle `status`; each occurrence retains its own `status` and the parent `definition_status`, including the transient `firing` state.
- Optionally `GET /api/agents/{agentId}/reminders/{reminderId}/log` if the UI exposes lifecycle details; CLI log still uses Agent transport auth.

The response must return a server-computed authorized anchor `{available:true, kind, display, href}` or exactly `{available:false}`. When unavailable, omit raw channel/message/thread IDs, names, participants, and excerpts entirely. Group display may use the authorized channel name; DM display must never expose its internal canonical channel name or member IDs. The client must not infer authorization or construct targets from raw IDs.

The safe `href` must land on the actual anchor. A top-level anchor opens and highlights that message. A thread anchor opens the existing Thread panel and highlights the anchored reply, not merely the parent message; an equivalent server-owned URL contract to `?thread={root}&message={anchor}` is acceptable. Desktop, mobile, group, and DM reuse the same route semantics.

The dedicated realtime event is `agent_reminder:changed` with minimal payload `{agent_id}`. It is emitted only after the state transaction commits for schedule, snooze, update, cancel, fire, and terminalization. Authorized clients invalidate the whole matching Agent Reminder query; they do not apply speculative row patches.

## Acceptance matrix

### Backend and CLI

1. One-shot schedule → one committed fire ledger event and at most one transient author input attempt.
2. Each supported repeat grammar computes the correct next occurrence; daily/weekly remain correct across DST using the locked IANA timezone.
3. Recurring fire advances `next_fire_at` and does not terminally mark the definition fired.
4. Snooze a scheduled or just-fired occurrence; recurring cadence resumes afterward.
5. Update title, one-shot time, and cadence independently; anchor cannot change.
6. Cancel prevents all future fires; list and log report the final state.
7. `log` returns scheduled/fired/snoozed/updated/cancelled in exact chronological order with occurrence IDs.
8. Server schedule/update/snooze/cancel advances a monotonic version and emits the exact post-commit `upsert` or `cancel`; transaction rollback emits nothing.
9. Daemon cache ignores stale/equal upserts, ignores stale cancels, replaces only the requested owner's entries on snapshot, and rejects cross-owner snapshot jobs.
10. Daemon restart/reconnect requests snapshots for running and idle Agents, rebuilds timers, and a past-due job produces one fire attempt without a server poll.
11. Current, duplicate, stale-version, cancelled, terminal, and cross-owner fire attempts produce exact canonical outcomes; only the current attempt can create one occurrence, receipt, lifecycle event, and transient author input attempt.
12. Update-vs-fire and cancel-vs-fire races are version-fenced with one deterministic winner; no old timer can fire a newer definition and no stale cancel can delete it.
13. Recurring fire commits the occurrence before the next versioned upsert. Reconnect/replay never overlaps occurrences or duplicates wake delivery.
14. Offline owner receives still-scheduled timer definitions through snapshot on reconnect; an already committed fire never replays its transient resident input.
15. Executable integration evidence proves the server has no periodic due-row Reminder scan registered as the normal trigger.
16. Receipt is visibly projected in the correct main timeline or thread as a non-conversational `author_type=system` event and creates zero search hits, channel unread increments, quote/reply/reaction surface, or other-Agent wake/inbox/task increments; these properties are bound in a Reminder-specific regression, not only inferred from global guards.
17. Deleted anchor or deleted thread root degrades safely with `anchor_available=false` and no excerpt; archived/deleted channel or Agent, or an Agent removed from the anchor channel, terminates with an explicit lifecycle reason and zero receipt/task/wake. A remove-vs-fire concurrency fixture proves serialization.
18. Existing one-shot Reminders migrate without ID, anchor, fire time, status, snooze count, or timestamp loss. A committed executable migration fixture covers the previous schema → current four-state preservation and down/up round trip; hand-run evidence alone is not a release gate.
19. Recurring → one-shot update clears cadence and fires once; a later calendar update reuses the original hidden timezone lock.

### Agent Card

1. Authorized humans see a Reminder tab for workspace-visible Agents; private Agent access remains restricted.
2. Tab shows scheduled and fired data from the real server, including recurring cadence, next/last time, and locked timezone.
3. Human UI contains no mutation affordance.
4. Authorized top-level and thread anchor deep links land on and highlight the actual anchored message/reply in group and DM surfaces; unauthorized/deleted anchors return only `{available:false}` and do not leak metadata.
5. Empty/loading/error/retry states are localized and responsive in both side-panel and mobile page variants.
6. A real schedule, fire, snooze, update, cancel, and terminalization cycle refreshes the tab without full-page reload through `agent_reminder:changed`; reconnect also refetches.

## Delivery split

- **Server/CLI lane:** retain the merged definition/occurrence/lifecycle, permission, API, and read-model work; add monotonic versioning, owner-scoped snapshot/upsert/cancel projection, fire-attempt fencing/idempotency, and remove the periodic due-row trigger.
- **Daemon lane:** implement the Raft-aligned versioned Reminder cache, local timer, reconnect snapshot requests, stale-message rejection, and fire-attempt transport. The daemon capability gates the runtime brief and release.
- **Frontend/UX lane:** add the read-only Agent Card tab against the human read API; no local mock may count as served acceptance.
- The lanes can proceed in parallel where contracts are stable. Final closure requires a released daemon plus deployed server and the full disconnect/reconnect, ordering, race, DB/API/Activity/DOM matrix; merged code alone is not completion.
