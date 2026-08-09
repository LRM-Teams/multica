# Turn fold — one exchange → one turn (lease-safe)

Status: proposal for review (@Alice)
Owner: current-backend-developer
Related: #2572 (batch client_message_id), report-dup root cause (cross-turn)

## Problem
"一条消息发两次" root cause is **cross-turn duplication**: one logical exchange
for a conversation gets processed as multiple independent turns, and each turn
sends a reply with a distinct `client_message_id` (batchClientMessageID depends
on seq, which changes per turn). The server dedups only by
`client_message_id`, so two different ids for the same content both insert →
duplicate (evidence: 里维 sent "里维：3" twice, different ids).

The multi-turn arises because a **second event for the same conversation is
created after the first event has already been drained** (server coalesce window
closed on the dispatched event), so the daemon drains a fresh event → fresh task
→ fresh turn.

## Goal
One exchange (one conversation batch) runs as **one turn** — the agent reads the
full coalesced `seq_from..seq_to` range and replies once. Duplicate never
produced at the source. Aligns with Raft (one exchange = one turn).

## Constraint (from review)
**No cross-conversation park / drain-ahead buffering.** Parking a leased event
in a daemon buffer lets its lease expire → reclaim → re-drain → processed twice
(the very duplication we're fixing). Never hold a lease for an event beyond its
own turn.

## Why a pure daemon-side fold is not sufficient
`DrainAgentInbox` returns **one event per call and consumes it** — no peek.
To fold same-conversation events, the daemon must drain-and-inspect the next
event. If that next event is a **different conversation**, it has already been
consumed; the daemon cannot return two tasks from one `drainInboxTask`, so it
would have to hold that event's lease → the forbidden park.

Therefore a correct, lease-safe fold needs a minimal server capability.

## Proposed change — server drains a conversation batch (additive)
Server `agent-inbox/drain` returns a **batch of all pending runnable events for
ONE conversation** for the runtime:
- `events`: all runnable events of the first available conversation,
  with a combined `seq_from..seq_to` (or the daemon combines them).
- `has_more`: true when other conversations still have pending events.
- No event is returned across conversation boundaries, so the daemon never has
  to push a partially-drained different-conversation event aside — nothing is
  left parked with a live lease between turns.
- The daemon runs **one turn per conversation batch** and completes/acks the
  whole batch's leases together when the turn finishes.

This is additive to the existing API shape (the response already carries an
`events` array) and does not require cross-executor coordination.

## Daemon-side handling
- `drainInboxTask` returns a `Task` whose primary `InboxEvent` is the first
  event of the batch; additional same-conversation events of the batch are
  carried as **folded leases** on the task.
- `watchInboxLease`: renews all folded leases alongside the primary (all belong
  to the same turn, so holding them for the turn's duration is not a park).
- Completion: `CompleteAgentInboxEvent` on the primary (with the agent's real
  output); folded events are **acked** (`AckAgentInboxEvent` with combined
  `seen_up_to_seq`) as processed-by-this-turn. Failure path fails/acks them too.

## Regression guard
- #2572 batch `client_message_id` stays for within-turn sends; the fold only
  changes how many messages/turns serve one conversation, not the id scheme.
- Lease/ack semantics preserved: every drained event is either run (lease
  renewed through its turn) or left pending server-side; none is parked.
- Cross-conversation events remain independent turns (correct).

## Tests
- One conversation, N coalesced messages → one task, agent reads full range,
  replies once.
- Two conversations pending → two separate tasks/turns.
- Folded events renewed during a long turn; all acked on completion.
- No regression: #2572 batch id; lease renew/reject cancels executor; fail path.
