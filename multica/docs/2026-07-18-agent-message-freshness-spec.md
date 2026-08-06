# Agent Message Freshness Spec

Written: 2026-07-18
Status: draft for review
Target branch: `LRM-Teams/multica` `dev`

## Summary

Agent message freshness makes visible agent chat output behave like a human conversation turn: an agent may think in parallel with other agents, but before its message becomes visible, the system checks whether the room has moved. If new relevant messages arrived after the agent's last observed channel sequence, the message is held instead of sent. The agent resumes with its original context plus the newer room context, then decides whether to send, revise, or stay silent.

This is an optimistic-concurrency model for chat actions. It uses `channel_message.seq` as the compare-and-set token, not wall-clock time.

## Motivation

In a group chat, multiple agents can be triggered by the same message at nearly the same time. If each agent independently reasons from the same snapshot, they can all produce the same stale reply.

Example: a user asks five agents to play a counting game: `1, 2, 3, 4, 5`.

Without freshness protection:

1. All five agents observe the same room state at `seq=10`.
2. All five reason independently.
3. All five decide the next valid answer is `1`.
4. All five send `1`.
5. The conversation is no longer coherent.

Expected behavior:

1. All five agents observe the same room state at `seq=10`.
2. The fastest agent reaches the send boundary first.
3. The system verifies that the channel is still at `seq=10`, sends `1`, and creates `seq=11`.
4. The other agents reach the send boundary with stale `seen_up_to_seq=10`.
5. The system holds those messages, returns the newer message `Agent A: 1`, and resumes the agents.
6. One agent revises to `2`, sends successfully, and creates `seq=12`.
7. The pattern repeats until the group has a coherent sequence.

## Current Implementation

The current code already implements part of this model under the agent transport freshness path.

Important code paths:

- `server/internal/handler/agent_transport.go`
  - `AgentTransportSendMessage`
  - `agentTransportFreshnessDecision`
  - `agentTransportSeenUpToSeq`
  - `agentTransportNewerMessageStats`
  - `holdAgentTransportSend`
  - `agentTransportSendDraft`
- `server/internal/handler/channel.go`
  - `enqueueChannelAgentPromptRangeWithTx`
  - `buildChannelMentionPrompt`
- `server/internal/handler/channel_ambient_wake.go`
  - `buildChannelAmbientUnreadPromptWithDB`

Current flow for `multica message send`:

1. Agent receives a channel prompt with a bounded message context.
2. Prompt context has a current target, and transport source includes the inbox event / task context.
3. Agent calls `multica message send`.
4. Server resolves the target and computes `seen_up_to_seq` from:
   - explicit `seen_up_to_seq`, if provided;
   - latest `message_read` transport audit for that task/inbox event;
   - otherwise the source `agent_inbox_event.seq_to`.
5. Server checks `channel_message` for `seq > seen_up_to_seq` in the relevant channel/thread scope.
6. If no newer messages exist, the message is inserted.
7. If newer messages exist, the message is held and saved as a draft. The response includes:
   - `state: held`
   - `outcome: held`
   - `subtype: freshness`
   - `reason: newer_messages_available`
   - `decision: local_hold`
   - `heldMessages`
   - `seenUpToSeq`
   - `latestSeq`
   - `newMessageCount`
   - `availableActions: [read_newer_messages, send_draft, revise_message]`

This matches the desired direction, but it is not yet a complete optimistic-send boundary.

## Gaps

## Gap 1: `send_draft` Bypasses Freshness

`agentTransportSendDraft` currently loads a held draft and sends it through `createAgentTransportMessage` without first re-running `agentTransportFreshnessDecision`.

This means a held stale draft can still be sent unchanged after the room has moved again.

For human-driven confirmation, `send_draft` can be useful. For autonomous agent collaboration, defaulting to `send_draft` is unsafe because it can publish a stale action.

## Gap 2: Check and Insert Are Not a Single CAS Boundary

The normal send path checks freshness before insert. However, the final check and insert are not explicitly modeled as a single compare-and-set boundary under a channel/conversation lock.

Race window:

1. Agent A checks `latest_seq == seen_up_to_seq`.
2. Agent B checks the same state before A commits.
3. Both pass.
4. Both insert.

PostgreSQL sequencing will assign different `seq` values, but both messages were generated from the same old context. For counting-game coherence, only one should win the snapshot.

## Gap 3: Held State Is Returned to Tool Caller, Not Always Resumed as Agent Continuation

The transport response is structured and good for the CLI, but the ideal abstraction is waitable agents:

- pause at the action boundary;
- append newer room context;
- resume the same agent with the original draft and the new messages;
- ask the agent to decide: send, revise, or stay silent.

Today, this behavior depends on the runtime/agent handling the held response correctly. A future platform-level continuation path would make it reliable for all agents.

## Gap 4: Prompt Contract Still Offers `send_draft` as a Peer Option

Freshness hold responses currently advertise `send_draft` alongside `read_newer_messages` and `revise_message`.

For autonomous agent runs, `send_draft` should not be presented as the default or first-class path. The safe path is:

1. read/review newer messages;
2. revise or decide no visible reply;
3. send with an updated `seen_up_to_seq`.

## Goals

- Prevent stale visible agent messages when the room has moved.
- Make agent chat output behave like optimistic concurrency over `channel_message.seq`.
- Preserve fast parallel thinking while serializing only the final visible action boundary.
- Give agents enough new context to revise instead of blindly retrying.
- Support both directed mentions and ambient group observations.
- Keep human/manual overrides possible, but do not let autonomous agents blindly send stale drafts.

## Non-Goals

- Do not globally serialize all agent thinking. Agents should still think concurrently.
- Do not require a full channel-history reload before every send.
- Do not use wall-clock time as the concurrency token.
- Do not remove drafts entirely; drafts remain useful for explicit confirmation and UX.
- Do not solve all turn-taking policy in this change. The first layer is a safe transport boundary.

## Proposed Model

## Core Concept: Action Boundary CAS

Every visible agent chat action must carry or derive a freshness token:

```text
seen_up_to_seq = latest channel_message.seq included in the agent's working context for this target scope
```

Before inserting a visible message, the server performs:

```text
latest_seq = max(channel_message.seq) for the same channel/thread visible scope

if latest_seq > seen_up_to_seq:
  hold message and return newer context
else:
  insert message
```

This is the chat equivalent of optimistic locking.

## Target Scope

Freshness must be scoped to the same target the message would affect:

- main channel target: messages in the main timeline (`thread_root_message_id IS NULL`);
- thread target: root message plus replies in that thread;
- DM target: the DM conversation timeline;
- future task/issue-derived targets: the conversation/channel projection that users see.

The existing `agentTransportNewerMessageStats` target scoping is the right foundation.

## Required Behavior

## Normal Send

When an agent sends content:

1. Resolve target.
2. Normalize message parts.
3. Derive `seen_up_to_seq`.
4. Start a transaction.
5. Acquire a short-lived target lock.
6. Re-read `latest_seq` inside the transaction.
7. If `latest_seq > seen_up_to_seq`, hold and do not insert.
8. If fresh, insert `channel_message` and transport audit in the same transaction.
9. Publish realtime events after commit.

## Held Send

When freshness fails:

1. Save the attempted message as a draft with:
   - target;
   - content/parts;
   - options;
   - client message id;
   - `seen_up_to_seq`;
   - `held_from_seq`;
   - `held_to_seq`.
2. Return the newer messages after the last shown/held range.
3. Record one `send_freshness_hold` activity event. Its safe structured details
   carry target, new/shown/omitted message counts, decision, and recommended
   next action. Activity renderers must present those facts from the structured
   fields; they must not parse a duplicated prose detail event.
4. Do not mark the agent task as failed.
5. Prefer continuation/resume over final completion when possible.

## Revised Send

When the agent reviews newer context and sends a revised message:

1. The revised request should include `seen_up_to_seq = latestSeq` from the held response, or it should first call `message read` to record a new seen seq.
2. The normal send CAS runs again.
3. If the room moved again, hold again.
4. If fresh, insert the revised message.

## Send Draft

`send_draft` must not bypass freshness.

Recommended behavior:

1. Load draft.
2. Resolve target.
3. Re-run freshness with the draft's saved `seen_up_to_seq` or with an explicit updated seen seq if the caller provides one.
4. If newer messages exist, keep the draft held and return another held response.
5. If fresh, send the draft.

For autonomous agents, `send_draft` should be discouraged unless the held response has been explicitly reviewed and there are no newer messages.

## Target Locking

To close the race between freshness check and insert, use a transaction-scoped advisory lock at the target boundary.

Candidate lock keys:

- channel main timeline: `workspace_id + channel_id + main`
- thread: `workspace_id + channel_id + thread_root_message_id`
- DM: same as channel main timeline

Implementation options:

- `pg_advisory_xact_lock(hash(workspace_id, channel_id, thread_root_message_id_or_zero))`
- or a narrower row lock on a conversation/channel state row if one already exists and is guaranteed to cover the target.

The lock should only protect the final check-and-insert boundary. It should not cover model inference.

## Agent Continuation Contract

Freshness hold should be framed to the agent as a continuation, not just a transport error.

Continuation prompt shape:

```text
Your previous visible message was held because the room moved before it could be sent.

Original draft:
<draft>

New messages since your last seen seq:
[seq=11] Agent A: 1
[seq=12] User: ...

Decide exactly one:
1. Send a revised message.
2. Stay silent because the newer messages make your draft unnecessary.
3. Explicitly send the original draft only if it is still correct after reviewing the new context.
```

Rules:

- The agent must not send protocol text like `held` or `no_reply` to the channel.
- The agent should use the chat output path only if it still needs a visible reply.
- For games/coordinated tasks, the agent should treat the latest message as the source of truth.

## API / Transport Contract

Current fields should stay:

- `state`
- `outcome`
- `subtype`
- `reason`
- `decision`
- `heldMessages`
- `newMessageCount`
- `shownMessageCount`
- `omittedMessageCount`
- `seenUpToSeq`
- `latestSeq`
- `transport_id`

Recommended additions or clarifications:

- `draft_id` or reuse `transport_id` as the held draft handle;
- `required_next_step`: `review_newer_context` for autonomous agents;
- `safe_actions`: `read_newer_messages`, `revise_message`, `stay_silent`;
- `unsafe_without_review`: `send_draft`;
- `target_scope`: `channel_main` or `thread`;
- `freshness_token`: latest sequence the agent must acknowledge before retrying.

## Data Model Impact

Existing table likely involved:

- `agent_transport_draft`
- `agent_task_transport_audit`
- `channel_message`
- `agent_inbox_event`

Potential changes:

- Ensure `agent_transport_draft` stores the original `seen_up_to_seq` and held seq range. Migration `175_agent_transport_draft` already appears to have `seen_up_to_seq`, `held_from_seq`, and `held_to_seq`; verify current generated models and queries.
- No new table is required for the first implementation.
- Optional later: a `conversation_action_boundary` or conversation state row if advisory locks need a durable named target.

## Implementation Plan

## Phase 1: Close the Immediate Safety Gap

Scope:

- Update `agentTransportSendDraft` to run freshness before sending.
- If stale, update/keep the held draft and return a held response.
- Add tests covering:
  - normal send holds when a newer message exists;
  - `send_draft` also holds when a newer message exists;
  - `send_draft` succeeds when no newer message exists;
  - repeated holds show only newly arrived messages where possible;
  - held terminal outcome is not treated as task failure.

Acceptance:

- A stale draft cannot be sent blindly by an autonomous agent.
- Existing manual draft send behavior still works when the target is fresh.

## Phase 2: Transactional CAS Boundary

Scope:

- Move final latest-seq check into the same transaction as insert.
- Add target-scoped advisory lock around check + insert.
- Reuse existing `insertAgentTransportMessageWithAudit` transaction.
- Make held path safe inside the same transaction or split with a clear lock/check boundary.

Acceptance:

- Two concurrent sends from the same `seen_up_to_seq` cannot both pass freshness for the same target.
- Tests simulate two concurrent agent sends and assert only one creates a visible message; the other receives a freshness hold.

## Phase 3: Runtime Continuation

Scope:

- Teach the daemon/runtime to treat held freshness as a resumable continuation.
- Append held context to the agent's active session instead of requiring the parent/chat harness to manually interpret the held response.
- The agent decides send/revise/silence in one more turn.

Acceptance:

- In a multi-agent counting game, agents naturally produce a sequence rather than duplicate first answers.
- Agent activity timeline shows held/revised decisions clearly.

## Phase 4: UX and Policy Refinement

Scope:

- Adjust transport response labels so `send_draft` is not the default autonomous path.
- Add UI wording for held drafts.
- Optionally expose a user-facing room setting for strict turn-taking games/coordinated tasks.

Acceptance:

- Human users understand why an agent did not immediately speak.
- Agents do not loop forever on repeated holds.

## Test Plan

## Unit Tests

- `agentTransportFreshnessDecision` detects newer main-channel messages.
- `agentTransportFreshnessDecision` detects newer thread messages only in the target thread scope.
- `agentTransportSeenUpToSeq` prefers explicit `seen_up_to_seq` over audit/inbox fallback.
- `send_draft` checks freshness before insert.
- Draft metadata is preserved across repeated holds.

## Integration Tests

- Concurrent two-agent same-snapshot send:
  - both start from `seen_up_to_seq=10`;
  - one sends successfully;
  - the other receives held response with the first message.
- Five-agent counting game simulation:
  - initial trigger asks agents to count from 1 to 5;
  - agents try to send from the same snapshot;
  - only one visible answer is accepted per fresh snapshot.
- Thread-target test:
  - newer messages in another thread do not hold a reply in this thread;
  - newer messages in this thread do hold it.
- Main timeline test:
  - main timeline visible thread replies are handled according to existing projection rules.

## Manual Verification

1. Create a group channel with at least three agents.
2. Send: `Let's play a counting game. Agents reply 1, 2, 3 in order; only one number per message.`
3. Observe that only one agent sends `1` from the first snapshot.
4. Others should either hold and revise, or stay silent if no longer needed.
5. Confirm activity timeline shows freshness holds, not failures.

## Risks

- Too-strict freshness could suppress useful parallel replies in brainstorming channels.
- Agents may loop if every retry sees another new message.
- Holding every stale message can create draft clutter.
- Advisory locks must be narrow and short-lived; never hold them during model inference.
- Existing scripts or tests that call `send_draft` as a forced send may need updates.

## Mitigations

- Keep freshness target-scoped, not workspace-global.
- Limit repeated hold attempts per run and ask the agent to summarize or stay silent after multiple holds.
- Keep explicit human override possible for drafts.
- Log structured details for debugging: target, seen seq, latest seq, held count.
- Add feature flags if rollout risk is high.

## Open Questions

1. Should `send_draft` remain available to autonomous agents, or only to human/operator flows?
2. Should strict action-boundary CAS apply to all group chats by default, or only agent-authored messages?
4. How many repeated freshness holds should one agent run tolerate before staying silent?
5. Should freshness continuation be implemented in the daemon, the server, or the Pi/Codex transport harness first?

## Recommended Next Step

Implement Phase 1 and Phase 2 together as the first production PR:

- `send_draft` must re-run freshness;
- check + insert must become a transaction-scoped CAS boundary;
- add concurrent-send tests around the counting-game failure mode.

This gives immediate correctness for the user's reported multi-agent sequencing problem while keeping larger continuation UX work separate.
