# Legacy / interim: Agent chat inbox path (do not extend)

**Status:** interim guard until [#2296](https://github.com/LRM-Teams/multica/issues/2296) hard-deletes residual server storage.  
**Label:** 仅文档（执行靠 review + 既有 residual suppress；#2296 落地后本文件改为历史指针或删除）。  
**Related:** ADR [0010](../adr/0010-direct-message-delivery-lifecycle.md), [#2282](https://github.com/LRM-Teams/multica/issues/2282), [#2295](https://github.com/LRM-Teams/multica/issues/2295) (done), [#2296](https://github.com/LRM-Teams/multica/issues/2296) (open full-delete), [#2596](https://github.com/LRM-Teams/multica/pull/2596) (hold / no batch cmid / notice-after-turn).

---

## 1. Canonical path for channel chat (new code MUST use this)

Ordinary channel / DM / thread **chat** delivery is Raft-style only:

```text
canonical Message (server)
  → agent:deliver / ack
  → MessageCoordinator Pending
  → idle body handoff  OR  content-free Notice when busy
  → Agent: multica message send|check|read|react|…
```

Rules for **new** code:

| Do | Do not |
|----|--------|
| Create / project **channel_message** (canonical Message) | Dual-write channel chat into `agent_inbox_event` as a second delivery path |
| Deliver via **MessageCoordinator** / `agent:deliver` | Enqueue residual channel reasons (`mention`, `channel_message`, `thread_reply`, `ambient`, `dm`) for ordinary chat |
| Visible replies via **`multica message send` / `react`** | Invent a parallel task-shaped chat lifecycle for channel traffic |
| Hold → local Draft + **one path** (revise / `--send-draft` / silence) | Auto-retry held drafts; mint **batch** `client_message_id` from turn coordinates |
| Busy → **Notice**; turn end does **not** auto body-handoff next batch | Assume turn completion always Flush body-handoffs the next exchange |

Authoritative design: **ADR 0010**. Runtime hold / notice behavior after #2596 matches Raft 1.0.15 intent (no batch cmid synthesis).

---

## 2. What is “legacy” here (names)

### 2.1 Residual channel dual-write reasons — **dead for execution**

Defined in `server/pkg/protocol/agent_inbox_reason.go` as residual:

- `mention`
- `channel_message`
- `thread_reply`
- `ambient`
- `dm` (legacy DM reason string)

**After #2295 hard-cut:**

- **Do not write** new rows with these reasons for ordinary channel traffic.
- Drain path **suppresses** leftover rows (`IsResidualChannelChatInboxReason`) instead of executing them.
- MessageCoordinator owns channel Message delivery.

**Do not** re-enable dual-write, “compat” enqueue, or a second wake path for these reasons. Full table/API removal is **#2296**, not an invitation to keep writing.

### 2.2 Product `agent_inbox_event` reasons — **still live, not residual channel chat**

These still use inbox / drain **by product design** (not channel dual-write):

| Reason | Surface |
|--------|---------|
| `chat_session` | Standalone FAB / bubble chat (`CreateChatTask` / EnqueueChatTask) |
| `voice_call` | Live voice-call directed turn |
| `issue_thread_backflow` | Issue → channel thread projection work |
| `collaboration_turn` | Env/collab peer wake |
| `channel_onboarding` | Membership onboarding protocol |

**New channel chat features must not be implemented by inventing another residual reason or by reusing residual strings.**  
If a new product wake is needed, either:

1. Prefer **canonical Message + deliver** when it is chat traffic, or  
2. Add an **explicit product reason** with a design review (and document it next to this file) — never a silent dual path for channel messages.

### 2.3 Shared plumbing that is not “channel dual-write”

`DrainAgentInbox*`, leases, and `agent_inbox_event` **remain** because product reasons in §2.2 still need them.  
That is why **#2296 is deferred**: hard-delete of routes/tables without migrating bubble / voice / backflow would break those surfaces.

Until #2296:

- Extend product reasons only with an explicit design decision.  
- Never use residual reasons for new work.  
- Never couple ordinary channel send/receive to inbox execution again.

---

## 3. Forbidden patterns for new PRs

Review / author checklist (fail the PR if any is introduced without an explicit issue reopening dual-write):

1. **Channel dual-write:** `insert channel_message` **and** enqueue residual inbox reason for the same human-visible chat event.
2. **New residual reason** or re-using `mention` / `channel_message` / `thread_reply` / `ambient` / `dm` for live traffic.
3. **Daemon path** that executes residual reasons instead of suppress.
4. **Batch client_message_id** from conversation/seq range (removed in #2596; do not reintroduce).
5. **Auto-resend** after freshness hold (no recovery/retry path; agent chooses one path).
6. **Turn-completion auto body-handoff** of Pending solely because the previous resident turn ended (use Notice + check / idle Accept→Flush / recovery).
7. **Server “seen cursor”** as Agent chat truth (boundary is machine-local per ADR 0010).

---

## 4. Where to look in code

| Concern | Location |
|---------|----------|
| Residual reason set + helper | `server/pkg/protocol/agent_inbox_reason.go` |
| Suppress residual on drain | `server/internal/handler/agent_inbox.go` (residual suppress paths) |
| Canonical delivery ADR | `docs/adr/0010-direct-message-delivery-lifecycle.md` |
| Hold / send identity / notice-after-turn | daemon MessageCoordinator + Credential Proxy (`message_send_proxy.go`, `message_runtime.go`); runtime brief in `execenv/runtime_config.go` |
| Product bubble enqueue | `chat_session` / `CreateChatTask` (not residual) |

---

## 5. Exit condition

When **#2296** is complete:

- Residual suppress paths and residual reason constants can go away with the storage.  
- Product wakes either stay on a **renamed non-legacy** inbox contract or move to dedicated surfaces.  
- This document should be rewritten as a short historical pointer or removed; update `docs/engineering-principles.md` the same day.

---

## 6. Why we document instead of deleting now

Hard-delete is blocked on product surfaces that still share inbox plumbing (§2.2) and on a full migration/test plan (#2296 AC).  
Until then, **behavior is: channel chat = MessageCoordinator only; residual rows = suppress only; no new dual path.**  
This file exists so that interim state is not rediscovered as “the right place to add channel wakes.”
