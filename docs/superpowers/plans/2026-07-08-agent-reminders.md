# Agent Reminders — Task Plan

Design: `docs/superpowers/specs/2026-07-08-agent-reminders-design.md`. Worktree:
`.worktrees/agent-reminders` (branch `feat/agent-reminders`, base `origin/dev`). Go module
root is `server/`. TDD per task: failing test → minimal impl → targeted `go test` → commit.

## T1 — Schema + queries
- `server/migrations/154_reminder.up.sql` / `.down.sql` per design (bare CREATE TABLE, named
  indexes; down = DROP TABLE IF EXISTS).
- `server/pkg/db/queries/reminder.sql`: CreateReminder :one, GetReminderForAgent :one (by id),
  ListRemindersForAgent :many (status filter), CountScheduledRemindersForAgent :one,
  ClaimDueReminders :many (atomic status transition, RETURNING *),
  MarkReminderFired :exec, SnoozeReminder :one, UpdateReminderSchedule :one,
  CancelReminder :one,
  FindReminderByIDPrefix :many (workspace+agent scoped, `id::text LIKE $prefix || '%'`).
- `make sqlc`; commit `feat(reminder): schema + queries`.

## T2 — Fire path + scheduler (depends T1)
- `server/internal/handler/reminder_fire.go`: historical V1 due-row scanner (removed by V3) — claim,
  per row: resolve channel+agent (gone → cancel + activity event `anchor_gone`); insert system
  receipt via `insertChannelMessageWithParts`; `ensureChannelAgentSession` → reminder prompt
  chat_message → `TaskService.EnqueueChatTask` (priority 2) → tag task_id → MarkReminderFired →
  activity event `reminder_fired`.
- System-message contract (#329, verified findings in design doc): human timeline already
  shows system rows (no change); FIX the ambient unread-bundle exclusion
  (`channel_ambient_wake.go:193`) so agents receive system rows with a system marker in the
  bundle format; receipt path never calls wake dispatch (by construction) — add regression
  test. Tests: receipt visible on timeline read; receipt present in an agent unread bundle
  marked system; receipt alone causes zero new tasks and no ambient pending bump.
- `server/cmd/server/reminder_scheduler.go`: historical V1 30s ticker + startup recovery (removed by V3),
  modeled on `autopilot_scheduler.go`. Wire in `main.go` background block (~:354); expose the
  handler from router construction in the least invasive way.
- Tests (`reminder_fire_test.go`, DB-backed like channel_ambient wake tests): due row fires
  exactly once across two concurrent claims; receipt row exists; task priority 2; fired status;
  anchor-gone cancels; stuck-firing recovery.
- Commit `feat(reminder): fire path + scheduler`.

## T3 — Agent transport endpoints (depends T1; after T2 to serialize router/main edits)
- `server/internal/handler/reminder.go`: schedule/list/snooze/update/cancel per design —
  `requireAgentTransportTask` auth, delay/fire_at validation (60s..90d), cap 25 → 409
  `reminder_cap_exceeded`, required explicit message_id, id prefix resolution,
  activity events. Follow UUID-parsing convention (`parseUUIDOrBadRequest` for body UUIDs).
- Routes in `router.go` beside `/api/agent/messages/*`.
- Handler tests: happy paths, cap, bounds, prefix ambiguity, wrong-agent 404, non-task-token 403.
- Commit `feat(reminder): agent transport endpoints`.

## T4a — CLI (depends T3)
- `server/cmd/multica/cmd_reminder.go` per design; register in `main.go` (groupCore).
- Tests in `cmd_compat_test.go`-style: subcommands exist, required flags enforced.
- Commit `feat(cli): multica reminder command family`.

## T4b — Runtime brief (depends T3, parallel with T4a)
- `runtime_config.go`: reminder capability bullet (CLI-transport-available branch only).
- Extend `runtime_config_test.go` want-lists; ensure CLI-unavailable branch bans it.
- Commit `feat(reminder): teach reminder commands in runtime brief`.

## T5 — Verify + review
- `cd server && go build ./... && go vet ./cmd/... ./internal/...`; targeted test run for all
  touched packages; gofmt on touched files only (baseline has pre-existing drift — do not
  reformat untouched files).
- Adversarial review vs design acceptance list + repo conventions (UUID rules, fail-soft
  activity events, no workspace leakage in queries — all queries filter workspace_id).

## Known baseline issues (do not chase)
- `TestPrepareCodexHomeAddsAgentMemoryWritableRoot` fails on this machine (codex version
  detection) — pre-existing on dev.
- gofmt drift exists in untouched files — leave them.
