---
status: accepted
---

# Channel management reuses ordinary Agent Reminders

Assigning an Agent the Channel Manager Role injects a persistent responsibility
that the Agent recovers from role context on every start. Assignment may wake
the Agent once to take over, after which the Agent decides when to create,
update, snooze, or cancel its ordinary self-owned Reminders. Multica has one
Reminder domain model and does not create a
`group_manager_auto`/`patrol` subtype, a server-owned patrol schedule, bounded
fallback timers, or role-specific Reminder permissions. Although a
server-managed patrol could enforce cadence, it would introduce a second
ownership model and confuse an Agent responsibility with a platform mechanism;
role-specific guidance therefore composes the ordinary Reminder capability
instead.

Removing the Channel Manager Role durably notifies the Agent but does not
cancel any Reminder on the Agent's behalf. The Agent checks its current role,
cancels ordinary Reminders that no longer serve an active responsibility, and
does no channel-management work if a Reminder Wake races with role removal.
The service never identifies role-related Reminders by title, Anchor, or target.
