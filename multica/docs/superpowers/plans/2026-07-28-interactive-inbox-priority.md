# Interactive inbox priority repair

## Goal

Prevent queued low-priority background wakes from delaying later interactive
human messages. Keep one active execution per Agent and preserve FIFO among
events with the same priority.

## Evidence and execution log

- [x] Measure one production voice turn:
  - message created at `07:25:47.347Z`;
  - ASR completed in about `4.5s`;
  - inbox event created at `07:25:51.876Z`;
  - dispatch began at `07:28:25.162Z`;
  - Agent execution took about `21.9s`;
  - TTS completed in about `0.8s`.
- [x] Confirm the 154-second pre-dispatch wait was not ASR or TTS.
- [x] Identify a priority-2 background wake that was queued before the
  priority-10 voice event and therefore ran first for about 93 seconds.
- [x] Confirm current lease SQL requires the oldest pending event for an Agent,
  so `priority` cannot reorder that Agent's pending work.
- [x] Confirm the active-delivery exclusion and daemon Agent slot independently
  prevent concurrent execution; this repair does not interrupt an active run.
- [x] Make pending admission priority-first and FIFO within equal priority.
- [x] Reuse the existing pending index from migration 160:
  `(workspace_id, agent_id, status, priority DESC, created_at, id)`.
- [x] Update regression tests and the executable engineering rule.
- [x] Run focused tests against an isolated PostgreSQL 16 database:
  `TestAgentInboxDrainPrioritizesPendingWakeAcrossRuntimes`,
  `TestClaimTaskByRuntime_ConcurrentClaimsKeepEqualPriorityFIFOWithoutDuplicates`,
  `TestClaimTaskByRuntime_QueuesChatBehindActiveIssueWake`, and
  `TestClaimTaskByRuntime_SerializesAcrossChatSessions` all passed.
- [x] Investigate the first backend CI failure. The old Radar regression
  required a low-priority unauthorized event to be cleaned before leasing a
  later priority-100 human wake. Update it to verify the new order: lease the
  human wake, preserve active-delivery exclusion, then terminalize the poison
  on the first poll after the human delivery settles.
- [x] Push independent PR
  [#1318](https://github.com/LRM-Teams/multica/pull/1318) into `dev`.

## Boundaries

- A new interactive message does not cancel or run concurrently with an active
  Agent execution.
- Later interactive messages do not overtake earlier messages at the same
  priority.
- The change applies to all inbox events so the existing priority field has one
  consistent meaning; it is not a voice-only exception.
- This reduces queued-background delay. It cannot remove time spent in the
  already-active run without a separately released daemon execution lane or a
  server-side conversational model.
