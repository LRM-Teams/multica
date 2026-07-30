# Research session stop + delete

## Goal

Research list rows support **停止** (pause) and **删除** (hard delete). Stopped sessions remain openable; chatting again resumes them.

## Status model

Add `paused` to `research_session.status` (DB CHECK + TS enum + i18n).

| Action | From | To | Side effects |
|---|---|---|---|
| Stop | `running`, `awaiting_user_confirm`, `drafting` | `paused` | Cancel in-flight inbox tasks on `chat_session.title = research:<id>` |
| Resume | `paused` | `running` | On user `POST .../messages` before wake |
| Delete | any | row removed | Cancel wakes first; `DELETE research_session` (CASCADE children) |

Completed sessions: stop is hidden; delete still allowed.

## API

- `POST /api/research/sessions/{id}/stop` → session JSON
- `DELETE /api/research/sessions/{id}` → 204
- Auth: workspace member (same as list/create)

## UI

- List row: `⋯` menu (stop event propagation) — 停止 / 删除
- Delete → `AlertDialog` confirm
- Badge shows `paused` →「已暂停」
- Resume via chat (no separate resume button)

## Out of scope

- Soft-archive / recycle bin
- Stopping individual fleet agents outside this session
