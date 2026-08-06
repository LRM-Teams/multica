# Device-code CLI login — implementation plan

Task #36. Design doc: `docs/superpowers/specs/2026-07-31-device-code-login-design.md`.

## Step 1 — Migration + queries

- [ ] `server/migrations/258_device_authorization.up.sql` / `.down.sql` —
  `device_authorization` table per the spec's schema section.
- [ ] `server/pkg/db/queries/device_auth.sql`: `CreateDeviceAuthorization`,
  `GetDeviceAuthorizationByUserCode`, `GetDeviceAuthorizationByDeviceCodeHash`,
  `ApproveDeviceAuthorization` (sets status/approved_by/issued_token_id,
  `WHERE status='pending'` guard for the idempotent-approve case),
  `DenyDeviceAuthorization` (same guard), `MarkDeviceAuthorizationPolled`
  (updates `last_polled_at`, returns old value so the handler can compute
  slow_down), `ClaimDeviceAuthorizationToken` (sets `claimed_at`, `WHERE
  claimed_at IS NULL` guard), `DeleteExpiredDeviceAuthorizations`.
- [ ] `sqlc generate` scoped to just these new query results — do not run a
  blanket regen (established gotcha this session: unrelated schema drift
  gets swept in). Diff the generated file before committing; if drift
  appears, hand-edit instead of taking the full regen.

## Step 2 — Handler + tests (RED → GREEN each)

`server/internal/handler/device_auth.go`:
- [ ] `RequestDeviceCode` (`POST /api/device/code`, no auth): generate
  device_code (32 bytes) + user_code (8-char disambiguated alphabet), hash
  device_code with `auth.HashToken`, insert row, return the RFC 8628-shaped
  body. Test: two calls never collide (extremely unlikely but assert
  uniqueness constraint surfaces cleanly, not a 500).
- [ ] `GetPendingDeviceAuthorization` (`GET /api/device/pending`, auth
  required): 404 on missing/expired/wrong-status, 200 with `client_hint` +
  `created_at` otherwise. Test: expired-but-still-pending-status row still
  404s (expiry checked by time, not just status column).
- [ ] `ConfirmDeviceAuthorization` (`POST /api/device/confirm`, auth
  required): approve path mints via existing
  `h.Queries.CreatePersonalAccessToken` (mirror
  `personal_access_token.go`'s exact param construction, including prefix
  truncation), deny path just flips status. Test: approve mints a real,
  usable PAT (round-trip: mint here, then hit `/api/me` with it in the same
  test, matching how `personal_access_token_test.go` already verifies
  minted PATs elsewhere). Test: confirming an already-approved row twice is
  a 200 no-op, not a double-mint (RED this first — write the test, watch it
  fail against a naive first-pass implementation that re-mints, then add the
  `WHERE status='pending'` guard).
- [ ] `IssueDeviceToken` (`POST /api/device/token`, no auth): the four RFC
  error codes + success. Test each transition explicitly: pending →
  `authorization_pending`; polled twice within `interval` seconds →
  `slow_down`; denied → `access_denied`; expired → `expired_token`;
  approved-unclaimed → 200 + token; approved-and-already-claimed → second
  call is `expired_token` (RED this one too — the natural first
  implementation returns the token again on replay; the single-claim
  guard is the point of the test).
- [ ] Wire into `server/cmd/server/router.go`: `/api/device/code` and
  `/api/device/token` go in the **unauthenticated** route group (mirrors
  where `/api/tokens`'s sibling unauthenticated routes like
  `/api/auth/send-code` live); `/api/device/pending` and
  `/api/device/confirm` go in the authenticated group.
- [ ] No per-row attempt counter (see spec's Security notes — `user_code`
  is the lookup key itself here, not a guess compared against one known
  target, so a counter wouldn't throttle a blind guesser trying a
  different code/row each time; protection is keyspace size + TTL).

## Step 3 — CLI

`server/cmd/multica/cmd_auth.go`:
- [ ] `runAuthLoginDevice(cmd)`: mirrors `runAuthLoginBrowser`'s structure
  (resolve serverURL/appURL, call, open browser, wait) but against the new
  endpoints instead of the loopback listener. Poll loop shaped like
  `waitForWorkspaceCreation` (`cmd_login.go:136`) — respect the server's
  `interval`, back off on `slow_down`, hard deadline = `expires_in`.
- [ ] Wire as the new default in `runLogin`/`runAuthLogin`'s dispatch (find
  the exact branch point during implementation — likely a flag-absence
  check currently routing to `runAuthLoginBrowser`).
- [ ] Leave `runAuthLoginToken` (`--token` path) untouched.
- [ ] `multica setup <workspace>`: extend `loginCmd`'s `Args`/`RunE` (or add
  a thin wrapper) to accept an optional positional workspace id/slug,
  threading it into `autoWatchWorkspaces` to resolve+set instead of
  picking `workspaces[0]`.
- [ ] CLI-side tests: table-driven test for the poll loop's slow_down
  backoff and deadline behavior against a fake HTTP server (matching the
  existing test style for `cmd_auth.go`/`cmd_login.go`, check for a
  `cmd_auth_test.go` or `cmd_login_test.go` precedent before inventing a new
  fixture pattern).

## Step 4 — Cleanup sweep + PR

- [ ] `go build ./... && go vet ./...` clean.
- [ ] Full relevant test packages green:
  `./internal/handler/... ./cmd/multica/... ./pkg/db/...`.
- [ ] `gh pr create --base dev`, writer≠reviewer — hand off to whoever's
  free per Barry's queue.

## Verification

- [ ] Migration up → down → up round-trip clean on local test DB.
- [ ] Manual smoke (if feasible in this environment): `multica login`
  against a local server, confirm via a second terminal hitting
  `/api/device/confirm` with a valid session token, confirm the CLI's poll
  picks up the minted PAT within one `interval` tick.
