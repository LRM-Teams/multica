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

The lifecycle control path replaces Multica's existing Agent Lifecycle
Operation implementation; it does not adapt, dual-write, or preserve that path.
It follows Raft's composition instead: the server coordinates machine commands
for stop, session reset, workspace reset, and start, while the machine publishes
Agent status, session, and Activity events. The three product modes are composed
as follows:

1. `restart`: stop, then start with the retained session.
2. `reset_session_restart`: stop, reset the session, then start fresh.
3. `full_reset_restart`: stop, reset the session, reset the workspace, then
   start fresh.

We copy Raft's boundaries and event vocabulary, not its known loss windows.
Every command carries a stable command ID, destructive commands are idempotent,
and the Machine Service retains and retries the terminal command result until
the server acknowledges it. A start acknowledgement means the command was
accepted, not that the Agent is ready; readiness comes from the later status and
session events. Workspace reset reports success only after deletion and
reprovisioning complete.

Frontend Activity consumes those same lifecycle events rather than a separate
operation-phase event stream. It shows Raft-style `Stopped`, `Starting`, and
`Working` transitions. The stop event carries the selected restart mode and
whether active work was interrupted, so Full Reset and forced interruption are
visible without inventing additional request, phase, or completion Activity
rows.
