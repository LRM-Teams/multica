---
status: accepted
---

# Reminder Wakes are idle-only transient owner inputs

After the first accepted `(Reminder ID, version)` commits, Multica attempts one
private system input to the Reminder owner. The runtime injects it only when the
owner Agent is idle. If the Agent is busy, the input is accepted and discarded:
it creates no busy inbox notice, enters no pending inbox, starts no concurrent
turn, and is not replayed at the next idle boundary. If the Agent is idle but
runtime injection fails, the input is likewise not queued, retried, or replayed.
If the post-commit transient delivery cannot reach the owner daemon because its
transport disconnects, it is likewise not retained or replayed. This differs
from a pre-commit `fire_attempt` loss: because that identity never committed,
the next owner snapshot may restore and attempt the still-due version again.
The lifecycle remains `fired` after commit because fire records the due schedule
identity, not Agent receipt or completed work. This deliberately matches Raft
1.0.15's transient Reminder delivery rather than the durable and retryable
delivery semantics of ordinary Messages. Busy discard, idle injection failure,
and post-commit transport loss remain diagnostic/telemetry facts only; they do
not create Reminder lifecycle events, Activity items, Agent Card statuses, or
any other user-visible delivery outcome. For a one-shot Reminder, the committed
definition remains terminal `fired` after any of these delivery losses. It is
never re-armed automatically; only an explicit snooze or a newly created
Reminder can schedule another fire.
