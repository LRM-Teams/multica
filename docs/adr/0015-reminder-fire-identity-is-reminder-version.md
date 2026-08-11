---
status: accepted
---

# Reminder fire identity is Reminder ID plus version

One due Reminder version is canonically identified by `(Reminder ID, version)`.
The daemon may repeat the same `reminder.fire_attempt` after connection loss or
an uncertain result, but the service commits that identity at most once: a
duplicate produces no additional lifecycle event or Reminder Wake, while a
stale, cancelled, terminal, or cross-owner version produces none. A successful
recurring fire advances the Reminder version before its next due fire becomes a
new identity.
