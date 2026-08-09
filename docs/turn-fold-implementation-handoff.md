# Turn-fold implementation handoff for @nash

Status: implementation handoff doc (from @current-backend-developer to @nash)
Reviewer: @Alice (will re-review against the 4 boundaries once the PR is up)
Branch: `cb/reply-cross-turn-dedup` (baseline = current dev incl. #2572)
One PR: server batch-drain-by-conversation **+** daemon one-turn-per-batch **together**, per Frank.

---

## 1. What and why

**Bug**: "一条消息发的回复报数重复" = **cross-turn duplication**. One logical
exchange for a conversation gets processed as multiple independent turns, and
each turn sends a reply with a distinct `client_message_id` (`batchClientMessageID`
depends on seq, which changes per turn). The server dedups only by
`client_message_id`, so two different ids for the same content both insert →
duplicate. Evidence: 里维 sent "里维：3" twice with different ids.

**Fix (approved direction)**: **turn-fold** — one exchange (one conversation
batch) runs as **one turn**; the agent reads the whole coalesced range and
replies once, so a duplicate is never produced at the source. This is the Raft
alignment (one exchange = one turn). NOTE: **replyID is NOT the main path** —
it is only a defensive fallback and is out of scope here. Don't slip back into
content-hash id work; this PR is about the turn model.

## 2. Alice's review boundaries (must satisfy; she re-verifies the actual diff)

1. **No lease race back**: when the server returns a batch by conversation, an
   event/batch is consumed exactly once. Never let a "parked, unacked event's
   lease expire → reclaimed → redelivered → processed twice" (that was why the
   old drain-ahead approach A was rejected). After one-turn-per-batch, the same
   turn sends exactly once.
2. **Batch-stable but batch-distinct client_message_id**: within one turn/batch,
   reuse one **stable** id (so idempotency collapses a duplicate); but different
   batches / different content must get **different** ids — do NOT collapse two
   genuinely different messages (that was the #2575 regression: distinct messages
   in one turn were wrongly folded into one id → 409-rejected the legitimate one).
3. **Don't break existing paths**: draft / `--send-draft` replay stay as-is;
   #2572 batch at-most-once (per-turn) and #2575 per-message cmid (regression fix)
   must compose correctly with turn-fold; don't let them fight each other.
4. **fail-soft**: if any step of fold/batch fails, fall back to per-message sends
   (never lose a message as the price of deduping).

## 3. Current code shape (read these first)

### Server side
- `server/internal/handler/agent_inbox.go`
  - `DrainAgentInboxByRuntime` (handler): calls `leaseAgentInboxEventForRuntime`
    → leases **one** event → builds a single-event `DrainAgentInboxResponse`.
    Response struct already has `Events []AgentInboxEventResponse` **and**
    `HasMore bool` (`HasMore: pending > 0` where pending counts ready events).
  - `leaseAgentInboxEventForRuntime`: the SQL/lease step that claims one next
    ready event for the runtime. **This is where "return more than one" starts.**
  - `countReadyAgentInboxEventsForRuntime`: counts pending ready events (used for
    HasMore today).
  - `AckAgentInboxEvent` already enforces `seen_up_to_seq >= SeqTo` per event
    (see `if seenUpToSeq < event.SeqTo → 409`).
- Coalescing signal: `enqueueOrCoalesceChannelMessageWakeWithTx`
  (`server/internal/handler/channel.go:5658`) coalesces same
  `(conversation_id, agent_id, reason)` messages into one event with a
  `seq_from..seq_to` interval. So **one coalesced event already = one
  conversation batch** in the common case; the second event arises when a new
  message to the same conversation arrives after the first event was drained.

### Daemon side
- `server/internal/daemon/client.go`
  - `DrainAgentInbox` currently returns only `Events[0]` and **ignores the rest
    of the array and HasMore**. This is the gap to close on the daemon.
  - `AgentInboxEvent` already carries `ConversationID`, `SeqFrom`, `SeqTo`,
    `Reason`, `Task`, lease fields.
  - `CompleteAgentInboxEvent`, `FailAgentInboxEvent`, `RenewAgentInboxEvent`,
    `AckAgentInboxEvent` operate on a single `AgentInboxLease`.
- `server/internal/daemon/daemon.go`
  - `drainInboxTask` (line ~2666): drains one event → returns one `Task` with a
    single `InboxEvent` (an `*AgentInboxLease`); acked non-runnable events in its
    internal `continue` loop.
  - Poller loop (line ~2619): on `task != nil` it spawns one goroutine per task.
  - `watchInboxLease` (line ~3254): renews **one** lease during execution;
    closes `lost` on permanent rejection (caller cancels executor).
  - `reportTaskResultForTask` (line ~3150): on "completed" →
    `CompleteAgentInboxEvent` (primary lease); on failure → `FailAgentInboxEvent`.
  - `Task` struct (`server/internal/daemon/types.go:39`): has `InboxEvent
    *AgentInboxLease`; needs a field to carry **folded** same-conversation leases.

## 4. Implementation plan

### Server — drain returns a conversation batch (additive)
Goal: a single `drain` returns **all pending runnable events of ONE
conversation** (the first available conversation), so the daemon never has to
consume a different-conversation event and park it.

- Extend the lease selection so that, after leasing the first ready event, it
  continues to lease **all other ready runnable events for the SAME
  `conversation_id`** (same runtime) and returns them together in `Events`.
  - Everything returned is **run-or-acked as one turn**; nothing is parked.
  - Leave other conversations' events untouched (do **not** consume them) so they
    stay pending server-side → each is its own later batch/turn (correct).
- `HasMore` = true when any other conversation still has pending ready events
  (so the daemon loops for the next conversation).
- Keep each event's own `SeqFrom`/`SeqTo` so the daemon can merge ranges for
  ack and present the union to the agent.
- **Lease-safety**: every event you hand to the daemon in the batch is leased;
  the daemon renews **all** of them while that one turn runs and acks them all
  when the turn ends. If any event in the batch is actually not runnable, ack it
  the same way `drainInboxTask` already acks non-runnable events (don't hold it).

Concretely: write a query variant of the current single-lease that returns the
set of ready runnable events for the first conversation, plus the conversation's
conversation_id to feed the batch. Preserve the existing per-event delivery
lease/token fields.

### Daemon client — read the whole batch
- `DrainAgentInbox`: return the full `Events` slice + `HasMore`, not just
  `Events[0]`. Keep `RuntimeID` set on each. (Change signature to return
  `([]*AgentInboxEvent, bool, error)` or a small struct.)

### Daemon task — one turn per conversation batch
- `drainInboxTask`: drain a batch; ack any non-runnable events; if runnable,
  build **one task** whose:
  - `InboxEvent` = the first event's lease (primary, carries the agent's real
    output on complete).
  - new `Task.FoldedInboxEvents []*AgentInboxLease` = the other events of the
    same conversation batch.
  - combined `SeqFrom` = min, `SeqTo` = max across the batch (feed the agent the
    whole range; the daemon already injects `MULTICA_TURN_SEQ_FROM/SEQ_TO`).
- `watchInboxLease`: renew **all** folded leases alongside the primary (same
  ticker; any permanent rejection → cancel the executor, as today). They belong
  to the same turn, so holding them for the turn's duration is **not** a park.
- Completion (`reportTaskResultForTask`):
  - primary (first event): `CompleteAgentInboxEvent` with the agent's real
    output (as today).
  - folded events: **ack** (`AckAgentInboxEvent` with each event's own
    `seen_up_to_seq >= its SeqTo`, merged where sensible). Satisfies Alice pt.2/
    the existing per-event `seen>=SeqTo` enforcement.
  - Failure path: `FailAgentInboxEvent` on the primary **and** fail/ack the
    folded set consistently (no orphaned leased event left to reclaim-loop).
- fail-soft: wrap the ack/fold; on any anomaly fall back to treating each
  otherwise-valid event on its own (do not drop a message).

## 5. client_message_id semantics (Alice pt.2, #2575)

- Keep `batchClientMessageID(conversationID, seqFrom, seqTo)` for the **primary**
  within-turn send so the one turn has one stable id (idempotent collapse).
- Do **not** let folding merge distinct-message cmids: the fold changes *how many
  turns serve one conversation* (fewer), not how messages map to ids. A turn that
  sends multiple distinct reply messages must still give each a distinct id
  (this is the #2575 behavior — don't regress it).
- Preserve draft / `--send-draft` replay paths untouched.

## 6. Tests to add
- Same conversation, N coalesced messages → **one** task; agent sees the full
  seq range; replies once; one stable cmid.
- Two conversations pending → **two** separate tasks/turns (cross-conversation
  independence preserved).
- Folded leases renewed during a long turn; all acked (their own
  seen_up_to_seq >= SeqTo) on completion — no leftover leased/reclaimable event.
- Failure path: primary + folded all fail/acked consistently, no orphan lease.
- Regression: #2575 (two distinct messages in one turn → distinct cmids, both
  land, no 409), #2572 batch at-most-once, draft/--send-draft replay.

## 7. Build / verify
- `cd server && go build ./...` and `go vet ./...`.
- Run the daemon + handler inbox tests: `go test ./internal/handler/ -run AgentInbox`
  and `go test ./internal/daemon/ -run Inbox`.
- Commit + push the branch, open ONE PR (title e.g.
  `feat(daemon,server): turn-fold — one exchange runs as one turn`), flag
  @Alice for review. After her GO + CI green, she builds/replaces the fix daemon
  for the local test111222 报数 retest.

## 8. Pitfalls to avoid (learned)
- Do **not** drain-ahead and hold a not-yet-acked cross-conversation event in a
  daemon buffer → its lease can expire and be reclaimed → reprocessed (the exact
  duplicate we're fixing). Batch-by-conversation + renew-all + ack-all avoids this.
- `HasMore` already exists in the response; don't reinvent it. It's Raft-aligned
  (agent-action/pagination `has_more`), treat as pagination continuation.
- The server's per-event ack enforces `seen_up_to_seq >= SeqTo`; honor per-event
  ranges when acking folded events.
- Don't rename `Task.InboxEvent`; add the folded set alongside it so
  completion/failure/nil-guards keep working.
