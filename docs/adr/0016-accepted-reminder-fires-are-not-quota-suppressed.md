---
status: accepted
---

# Accepted Reminder fires are not quota-suppressed

Every first accepted `(Reminder ID, version)` makes exactly one post-commit
attempt to deliver a Reminder Wake. Multica does not skip that attempt through
a per-Agent, per-channel, or daily fire quota, and `quota_coalesced` is not a
valid successful fire outcome. The attempt remains subject to the idle-only
transient delivery policy in ADR 0018: an owner that is busy receives no busy
notification or queued Wake. Multica also imposes no fixed per-Agent active
Reminder count; operational capacity must not become a product-visible quota
that rejects an otherwise valid Reminder. `fired` means the due identity
committed and its single transient delivery was attempted, not that the Agent
received or acted on it.
