# Agent context deleted-message filtering

## Goal

Prevent deleted channel and thread rows from consuming the bounded recent
message window supplied to Agents.

## Evidence

- The first regression run returned a deleted main-timeline row alongside the
  live row.
- `scanChannelMessage` correctly cleared the deleted row's content, so this was
  not a deleted-text disclosure.
- The deleted row still occupied one of the bounded context slots. Enough
  deleted rows can therefore push live messages out and leave the Agent with an
  incomplete recent conversation.
- Thread roots and replies used the same unfiltered pattern.

## Steps

- [x] Add a failing database regression covering main-timeline and thread
  context.
- [x] Filter `deleted_at IS NULL` before ordering and limiting both queries.
- [x] Run the regression against a fresh fully migrated PostgreSQL database.
- [x] Run handler vet and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1053](https://github.com/LRM-Teams/multica/pull/1053), stacked on #1049.

## Verification

- Before the change, the regression failed because the deleted main message
  remained in the returned context.
- After the change, main context contains only the live message and thread
  context contains only the live root and live reply.
- `go vet ./internal/handler` and `git diff --check` pass.
