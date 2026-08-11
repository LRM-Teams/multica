---
status: accepted
---

# Recurring Reminders collapse offline gaps into one overdue fire attempt

When an owner daemon reconnects after missing multiple ideal times for one
recurring Reminder, its owner-scoped snapshot restores one current due version,
which may produce at most one overdue fire attempt. An accepted overdue fire
makes one idle-only transient Wake attempt and advances directly to the first
future cadence time; Multica neither replays one attempt per missed time nor
records the skipped times as fired occurrences. If the owner is busy, that Wake
is dropped under ADR 0018 and is not replayed. The same no-makeup rule applies
when idle runtime injection fails or post-commit transport is lost: cadence
still advances to its next ordinary future time instead of scheduling an
immediate compensation fire. This avoids a reconnect or recovery wake storm
while keeping `fired` tied to the accepted Reminder version.
