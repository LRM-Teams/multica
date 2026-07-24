# Voice call dev integration

## Goal

Move the completed realtime Agent voice-call stack onto the current `dev`
history so the existing deployment workflow can actually ship it.

## Integration findings

- [x] Confirmed that the previously merged stacked PRs landed in feature
      branches, while the final call entry commit was not an ancestor of
      `origin/dev`.
- [x] Created `agent/voice-call-dev-integration` from the latest `origin/dev`.
- [x] Merged the final voice-call feature branch.
- [x] Resolved the API schema conflict by retaining both web-push and voice-call
      response types.
- [x] Resolved the router conflict by retaining current memory-curation routes
      and adding authenticated voice-call routes.
- [x] Preserved the current aliased interaction-DAG completion query; the
      conflict was unrelated to voice calls.
- [x] Verified that duplicate numeric migration prefixes are safe because the
      migration ledger uses the complete basename as its version.
- [x] Removed two stale deployment-test assertions for values that are
      intentionally derived by runtime configuration.

## Deployment boundary

The `aliyun-dev` GitHub Environment currently has no `VOLCENGINE_RTC_*`
secrets. Code deployment can expose the Agent DM call entry, but creating a
call remains disabled until AppID, AppKey, access credentials, exactly one Ark
model selector, and a callback signature are configured.

## Verification

- [x] Deployment configuration scripts passed.
- [x] Volcengine integration, lifecycle service, runtime wiring, and migration
      tests passed.
- [x] Handler/context/authorization tests passed against a disposable database
      migrated from zero; the database was removed after the run.
- [x] Go vet passed for the RTC integration, voice-call service, handler, and
      API server packages.
- [x] Core suite: 91 files, 884 tests passed.
- [x] Views suite: 278 files, 2655 passed, 5 skipped.
- [x] Monorepo typecheck: 6 tasks passed.
- [x] Monorepo lint: 8 tasks passed with 0 errors; 8 pre-existing warnings
      outside the changed files.
- [x] React Doctor: 0 issues.
- [x] Final merge-tree and diff checks against `origin/dev`.
