# Obsolete Multica bridge executor deploy cleanup

## Goal

Make the s89 deploy workflow match the current direct AReaL-to-Multica
topology and stop reporting a removed service as unhealthy.

## Evidence and execution log

- [x] Observed the deploy workflow pull, start, and health-check
  `db-bridge-executor-multica`, while the merged Compose model has no such
  service.
- [x] Traced the removal to commit `501dc2861`, whose code and comments state
  that AReaL now calls Multica directly and no Multica-side executor is needed.
- [x] Confirmed the retained `db-bridge-stub-multica` still serves the opposite
  Multica-to-AReaL direction and must continue to deploy.
- [x] Added a regression assertion that the removed executor cannot return to
  the deploy workflow and reproduced the current failure with the static test.
- [x] Removed only the obsolete pull/start/health-check workflow paths.
- [x] Validated static and rendered self-host configuration, actionlint v1.7.7,
  `git diff --check`, and confirmed the workflow/Compose files contain no
  `db-bridge-executor-multica` or `BRIDGE_MULTICA_UPSTREAM_URL` reference.
- [x] Pushed `fix/deploy-obsolete-bridge-executor` and opened ready-for-review
  PR #957 into `dev`: <https://github.com/LRM-Teams/multica/pull/957>.
- [x] After #948/#951/#952 changed the same deployment block on `dev`,
  reproduced GitHub's merge conflict in `.github/workflows/deploy.yml`.
- [x] Merged current `dev`, retained the repository-managed Caddy/HTTPS startup,
  and reapplied only the obsolete-executor removal around the surviving bridge
  stub.
- [x] Re-ran the self-host configuration test, workflow lint, and diff checks
  after resolving the conflict.

## Boundaries

- The retained bridge stub and AReaL-side executor are unchanged.
- No server service is created, stopped, or edited directly.
- This cleanup does not change application, voice, or HTTPS behavior.
