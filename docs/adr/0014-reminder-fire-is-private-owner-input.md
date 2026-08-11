---
status: accepted
---

# Reminder fire is private owner input, not a conversation Message

When a Reminder comes due, Multica records its lifecycle transition and
attempts a private system input only to the Reminder owner, carrying the
immutable Message Anchor and return surface. The input follows the idle-only
transient delivery policy in ADR 0018. It does not create or broadcast a
canonical Message in channel, DM, or thread history; after waking, the Agent
decides whether the anchored context merits sending a normal Message. This
matches Raft's owner-scoped wake while preserving the domain boundary between a
communication fact and a runtime wake signal.
