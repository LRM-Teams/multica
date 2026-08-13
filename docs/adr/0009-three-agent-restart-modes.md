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

The lifecycle control path keeps `agent_lifecycle_operation` only as the
user-visible request/result record; it removes the old dispatch orchestration.
It follows Raft's direct Computer boundary: the server sends one stable
Workspace Runner lifecycle command, and the machine composes stop, optional
session reset, optional workspace reset, and start while publishing Agent
status, session, and Activity events. The three product modes are composed as
follows:

1. `restart`: stop, then start with the retained session.
2. `reset_session_restart`: stop, reset the session, then start fresh.
3. `full_reset_restart`: stop, reset the session, reset the workspace, then
   start fresh.

Every command carries the operation's stable ID. The Workspace Runner keeps a
bounded in-process receipt cache: the first delivery returns `accepted`, a local
relay duplicate returns `duplicate`, and destructive work runs once in that
process. This receipt cache is deliberately not a durable queue. An unavailable
Runner fails the operation immediately; an accepted command without a terminal
result times out visibly, and the server never replays a destructive Full Reset
after reconnect. Acceptance is not readiness; readiness comes from later status
and session events. Workspace reset reports success only after deletion and
reprovisioning complete.

The removed paths must not return in parallel: no Agent lifecycle payload on
daemon heartbeat, no Redis delivery lease, no local `.multica/lifecycle-commands`
ledger, and no extra managed-launch `agent:stop` projection.

Frontend Activity consumes those same lifecycle events rather than a separate
operation-phase event stream. It shows Raft-style `Stopped`, `Starting`, and
`Working` transitions. The stop event carries the selected restart mode and
whether active work was interrupted, so Full Reset and forced interruption are
visible without inventing additional request, phase, or completion Activity
rows.
