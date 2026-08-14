# Model Agent restart as three explicit modes

Agent lifecycle exposes three ordered restart modes rather than one restart
flag with optional side effects:

1. `restart` replaces the runtime process while retaining both its model
   session and complete Agent Workspace.
2. `reset_session_restart` clears the model session and context, retains the
   complete Agent Workspace, and starts a fresh model session.
3. `full_reset_restart` clears the model session and context, removes the
   complete Agent Workspace, reprovisions an empty canonical directory, and
   restarts the Agent.

None of these modes deletes the server-side Agent identity, configuration,
chat history, or Issues. Full Reset is destructive local reinitialization, not
Agent Deletion and not Local Agent Workspace Deletion: unlike local deletion,
it immediately reprovisions the directory and keeps the Agent executable.

All three modes may force-interrupt an active turn. Full Reset cannot remove the
Agent Workspace until Machine Service has reliable evidence that the runtime
process has stopped and its provider lease has been released.

Raft Computer 1.0.16 accepts three separate lifecycle messages:
`agent:stop`, `agent:reset-workspace`, and `agent:start`; it has no
`agent:lifecycle` composite message. Therefore `agent_lifecycle_operation` is
the durable server-side product orchestrator, while the Workspace Runner owns
only the local process transition named by each Raft message. The three product
modes are composed as follows:

1. `restart`: stop, then start with the retained session.
2. `reset_session_restart`: stop, reset the session, then start fresh.
3. `full_reset_restart`: stop, reset the session, reset the workspace, then
   start fresh.

Stop persists the exact old launch in `stop_launch_id`; only that launch's
inactive status advances the operation. The Runner removes it from local
admission, fences late startup, force-interrupts any resident provider, and
waits for both its startup owner and provider lease/process to quiesce before
publishing inactive. Start persists a new `launchId` and stable
`startDispatchId` before dispatch. `restart` leaves `config.sessionId`
unspecified so the
Computer resumes its stored provider session; both reset modes send an explicit
empty session ID so startup cannot resume stale context. `full_reset_restart`
adds the Raft `agent:reset-workspace` boundary and waits for Multica's terminal
reset result extension before start, because Raft's own fire-and-forget handler
does not provide enough evidence for a destructive product operation.

Runner reconnect resumes the persisted current step. Stop and reset steps
suppress generic desired/observed start reconciliation; the starting step uses
that same reconciliation and immutable start IDs. Relay publication is not
accepted/completed proof: an undelivered operation stays visible and resumable
until Runner ready or its timeout. Workspace reset is idempotently retryable
only while no managed launch/provider process exists.

The removed paths must not return in parallel: no Agent lifecycle payload on
daemon heartbeat, no composite `agent:lifecycle` wire event, no lifecycle
delivery lease, and no local `.multica/lifecycle-commands` ledger.

Frontend Activity consumes the normal managed-launch facts rather than a
separate operation-phase event stream. It shows Raft-style `Stopped`,
`Starting`, and `Idle/Working`; operation mode and failures remain in the
server business record.
