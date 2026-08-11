---
status: accepted
---

# Messages have no delete operation

A canonical Message is an immutable communication fact and has no user-facing
or public API delete operation. Multica removes
`DELETE /api/channels/{channelId}/messages/{messageId}` and does not replace it
with another per-Message deletion path. Existing `deleted_at` data, tombstone
rendering, and defensive filters remain for historical compatibility and
administrative data lifecycle; they do not imply a supported Message mutation.
Channel or Workspace lifecycle remains a separate aggregate-level operation.
