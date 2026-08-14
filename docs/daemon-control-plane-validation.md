# Daemon control-plane validation

Run these scenarios against the locally built daemon before publishing a
daemon release. Use the Test Server that contains the matching Server change;
keep the previous installed binary as a reversible backup.

| Scenario | Operation | Required evidence |
| --- | --- | --- |
| Single carrier | Start one Computer with one Workspace and inspect daemon/Server traffic | The current Runner advertises `workspace_runner_control_plane_v1`; heartbeat acknowledgements arrive on that Runner; the process makes no `/api/daemon/heartbeat` calls and the legacy runtime socket sends no `daemon:heartbeat`. |
| Reconnect fence | Interrupt the Runner socket while keeping the process alive, then wait for reconnect | The old socket stops producing or consuming control frames; the replacement becomes current and resumes one heartbeat stream; a queued non-destructive action is delivered once. |
| Workspace isolation | Connect at least two Workspace bindings on one Computer and request one action per Workspace | Each Runner sends only its Workspace Runtime IDs. Cross-Workspace and cross-Computer Runtime IDs are rejected, while both legitimate actions complete independently. |
| Historical Runtime residue | Keep an offline Runtime row for an uninstalled provider, then restart the Computer with the remaining current providers | Attachment replay accepts the current Runtime cursor set, rejects foreign Runtime IDs, and starts control heartbeats without waiting for or reviving the historical row. |
| Three Agent restarts | Run `restart`, `session`, and `full`, including one active turn | The durable server orchestrator delivers discrete `agent:stop → agent:reset-workspace? → agent:start` commands through the current Runner. `restart` resumes the canonical provider session; `session` starts with `config:{}` while retaining Workspace files; `full` additionally waits for the same operation's terminal workspace-reset result before starting. Inactive is emitted only after the exact old `launch_id` has quiesced; Activity follows `Stopped → Starting → Idle`; a subsequent message receives a reply. |
| Reminder receipt recovery | Schedule a near-term Reminder, withhold its first `fire_result`, then reconnect or restart the Computer | Local wake retries only until it is enqueued, so local input is injected once. The unacknowledged Server receipt remains durable and is replayed on reconnect/restart; the exact ack removes it, and no replay injects a second local turn. This matches Raft 1.0.16 and intentionally has no same-connection receipt retry timer after wake enqueue. |
| Message and compaction regression | Send ordinary messages before and after context compaction and exercise freshness hold | Each accepted message eventually gets one reply; Activity includes compaction start/finish and later message activity; hold remains explicit and does not auto-resend a draft. |
| Process restart recovery | Restart the Computer process while an Attachment and an unacknowledged Reminder receipt exist | Attachment replay completes before control heartbeat begins; pending receipt/control work resumes without duplicate local input; Agent replies after recovery. |

Automated wire and ownership tests are necessary but not sufficient. Release
evidence must also include the local binary version and PID, matching Server
deployment, the live scenario results above, and the official artifact/feed
after publishing.
