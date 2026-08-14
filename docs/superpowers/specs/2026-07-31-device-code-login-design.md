# Device-code CLI login — design

Task #36 (Parker, 2026-07-31, high priority — weekend push). Replace the two
painful CLI login paths (loopback-browser OAuth that requires browser+CLI on
the same machine, and manual `--token` PAT paste) with an OAuth 2.0 Device
Authorization Grant (RFC 8628) flow — the same shape GitHub CLI, `gcloud`,
and `az` use. Also add a one-step way to pin the workspace during setup
(Frank, 2026-07-31) — see the CLI section below for why this landed as
`--workspace` rather than the positional originally sketched here.

## Sources used

Per the IP boundary the team locked down today in `#prj-daemon` (thread
`6cb04a5c`): public sources and our own code only, no reverse engineering of
Raft's closed-source binary.

1. **RFC 8628** (OAuth 2.0 Device Authorization Grant) — the standard itself.
2. **Raft's public docs** (`raft manual get` — external-agent "Connect it"
   section, `raft-cli-overview`, `raft agent login --help`) — confirms Raft's
   CLI uses this same device-code shape (`agent login start`/`wait`, a short
   code, browser confirmation by anyone with permission, no same-machine
   requirement) but does not document Raft's internal implementation.
3. **Our own code** — existing verification-code flow
   (`server/internal/handler/auth.go`) and PAT minting
   (`server/internal/handler/personal_access_token.go`,
   `server/internal/auth`), which this design reuses rather than
   reinventing.

## Current state (what's painful)

- **Browser login** (`cmd_auth.go:296` `runAuthLoginBrowser`): CLI opens a
  local HTTP listener (`/callback`), opens `${appURL}/login?cli_callback=...`
  in a browser, waits for the browser to redirect back with a JWT, then
  exchanges that JWT for a PAT via `POST /api/tokens`. **Requires the browser
  and the CLI process to be on the same machine** — breaks entirely for
  headless/remote/SSH'd machines (exactly Ronan's s144 case today).
- **Token login** (`cmd_auth.go:421` `runAuthLoginToken`): user manually
  copies a PAT from the web UI and pastes it via `multica login --token`.
  Works headless, but is the "还得自己设 token" friction Frank flagged —
  no discovery, no guided flow, easy to fat-finger.

Both converge on the same result: a PAT (`mul_...`) saved to
`cli.SaveCLIConfigForProfile`. The device-code flow's whole job is to produce
that same PAT through a friendlier path — it does not change what's stored
or how the rest of the CLI consumes it.

## Reusable pieces (confirmed, not reinvented)

- **Short-code generation + TTL + rate limiting**: `generateCode()`
  (`auth.go:116`, crypto/rand, 6 digits) plus the existing
  `CreateVerificationCode`/`GetLatestVerificationCode`/
  `IncrementVerificationCodeAttempts`/`DeleteExpiredVerificationCodes` query
  pattern (`auth.go:317-373`) is the direct precedent for user-code
  generation, expiry, and brute-force throttling. New table, same pattern —
  not the same table, because verification codes are keyed by email and
  device codes aren't.
- **PAT minting**: `auth.GeneratePATToken()` + `auth.HashToken()` +
  `h.Queries.CreatePersonalAccessToken` (`personal_access_token.go:60-100`)
  is exactly what "confirm" needs to call once a human approves — the device
  flow produces a PAT through this same path, it does not mint a new token
  type.
- **CLI-side config save + verify**: `cli.SaveCLIConfigForProfile` +
  `GetJSON(ctx, "/api/me", ...)` (`cmd_auth.go:395-417`) is reused verbatim
  for the final "save and confirm identity" step.
- **CLI polling pattern**: `waitForWorkspaceCreation` (`cmd_login.go:136`)
  already implements "poll an endpoint on an interval with a deadline,
  handle transient errors by continuing" — the device-token poll loop
  follows the same shape.
- **Browser-open helper**: `openBrowser()` (`cmd_auth.go:138`) reused as-is.

## New: device authorization endpoints

Three endpoints, `server/internal/handler/device_auth.go` (new file):

### `POST /api/device/code` — no auth required

RFC 8628 §3.1. `Content-Type: application/x-www-form-urlencoded`.
Required `client_id` (the official public client is `multica-cli`);
optional `scope` is accepted and ignored. Missing `client_id` is
`invalid_request`; unknown `client_id` is `invalid_client`. JSON bodies
are rejected — there is no `{client_hint}` compatibility.

Generates:
- `device_code` — 32 random bytes, base64url-encoded (~43 chars). Long,
  unguessable, **never shown to a human** — held only by the polling CLI
  process. This is the actual bearer secret of the flow.
- `user_code` — human-typable, format `XXXX-XXXX` (8 chars from a
  disambiguated alphabet excluding 0/O/1/I/L, generated via crypto/rand —
  same rand source as `generateCode`, different alphabet/length since this
  code is typed by a human who may not be the same identity being verified,
  see Security below).

Persists a new `device_authorization` row (`status='pending'`,
`expires_at = now() + 10 minutes` — matches the existing verification-code
TTL), returns:
```json
{
  "device_code": "…",
  "user_code": "XXXX-XXXX",
  "verification_uri": "https://leagent.me/device",
  "verification_uri_complete": "https://leagent.me/device?user_code=XXXX-XXXX",
  "expires_in": 600,
  "interval": 5
}
```
Field names match RFC 8628 §3.2 exactly. The confirmation page stores
`client_hint` as the official client display name (`Multica CLI`).

### `GET /api/device/pending?user_code=XXXX-XXXX` — auth required (any logged-in user)

For the App's confirmation page. Returns the pending row's display info
(`client_hint`, `created_at`) if `status='pending'` and not expired; 404
otherwise (expired/already-resolved/unknown code all collapse to 404 — do
not distinguish "wrong code" from "expired code" to a prober, same
non-enumeration discipline `VerifyCode` already follows for email codes).

### `POST /api/device/confirm` — auth required

Body: `{"user_code": "XXXX-XXXX", "approve": true}` (`approve: false` = deny,
folds the deny action into the same endpoint rather than a second route).

On approve: locks the row (`SELECT ... FOR UPDATE`), verifies
`status='pending'` and not expired, mints a PAT via the existing
`CreatePersonalAccessToken` path (name `"CLI (<client_hint>)"`,
`expires_in_days` — reuse the browser flow's existing 90-day default),
stores `issued_token_id` + `approved_by_user_id`, sets `status='approved'`.
On deny: sets `status='denied'`. Idempotent double-confirm/double-deny on an
already-resolved row is a no-op 200, not an error — the approver's back
button/double-click shouldn't surface a scary failure.

### `POST /api/device/token` — no auth required

RFC 8628 §3.4. `Content-Type: application/x-www-form-urlencoded` with
`grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`,
and `client_id`. Wrong grant is `unsupported_grant_type`. CLI polls this.
Response mirrors RFC 8628 §3.5's error vocabulary:
- `status='pending'` → `400 {"error": "authorization_pending"}`
- polled again within `interval` seconds of the last poll on this row →
  `400 {"error": "slow_down"}` (and the server-tracked interval backs off,
  same as the RFC recommends) — tracked via `last_polled_at` on the row, no
  new table.
- `status='denied'` → `400 {"error": "access_denied"}`
- expired → `400 {"error": "expired_token"}`
- `status='approved'` and not yet claimed → RFC 6749 success
  `200 {"access_token": "mul_...", "token_type": "Bearer", "expires_in": 7776000}`,
  and the row is marked `claimed_at = now()` — **single-claim**: a second
  call after a successful claim returns `expired_token`, not the token
  again. `access_token` is the existing user PAT. There is no
  `{token, expires_in_days}` compatibility body.

## Schema

New migration, `device_authorization` table:
```sql
CREATE TABLE device_authorization (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash  TEXT NOT NULL UNIQUE,   -- hashed like PATs (auth.HashToken), not stored raw
    user_code         TEXT NOT NULL UNIQUE,
    client_hint       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','approved','denied')),
    approved_by_user_id UUID REFERENCES "user"(id),
    issued_token_id   UUID REFERENCES personal_access_token(id),
    last_polled_at    TIMESTAMPTZ,
    claimed_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON device_authorization (expires_at); -- cleanup sweep, same DeleteExpired* pattern
```
`device_code` is hashed at rest (`auth.HashToken`, same function PATs use) —
the raw value only ever exists in the `POST /api/device/code` response body
and the polling CLI's memory, mirroring how PAT raw tokens are never
persisted. `/api/device/token` looks up by `HashToken(device_code)`.

## CLI changes

`server/cmd/multica/cmd_auth.go` / `cmd_login.go`:
- New `runAuthLoginDevice(cmd)` replaces `runAuthLoginBrowser` as the
  **default** path for `multica login` (no flags) — this is the fix for
  Frank's actual complaint, not an opt-in third mode.
  - `POST /api/device/code` as form-urlencoded `client_id=multica-cli`,
    print `verification_uri` + `user_code` (RFC figure 2), open
    `verification_uri_complete` as the §3.3.1 shortcut, print the URL as
    a fallback for headless machines.
  - Poll `POST /api/device/token` as form-urlencoded
    `grant_type=urn:ietf:params:oauth:grant-type:device_code` +
    `device_code` + `client_id` on `interval` (honoring `slow_down` by
    increasing the local interval), deadline = `expires_in`.
  - On success: persist `access_token` via the same save-and-verify tail
    (`cli.SaveCLIConfigForProfile` + `GET /api/me`).
- `--token` flag path (`runAuthLoginToken`) is **kept as-is** — still the
  right answer for CI/scripted/non-interactive setups where there's no human
  to approve a device code at all.
- `multica setup --workspace <id-or-slug>`: **implemented as a flag, not the
  positional originally sketched here.** `setupCmd`/`setupCloudCmd`/
  `setupSelfHostCmd` already forward their positional `args` into
  `runLogin(cmd, args)`, which `loginCmd` also uses for its own
  `--token mul_...` (space form) recovery — reusing that same positional for
  a workspace identifier would make `multica setup mul_xxx` (a real recovery
  invocation) ambiguous with `multica setup my-workspace-slug`. A dedicated
  `--workspace` flag (plus `MULTICA_WORKSPACE` env, same `FlagOrEnv`
  precedent every other CLI setting uses) avoids that collision outright and
  is self-documenting. Threaded into `autoWatchWorkspaces`: when set,
  resolves against `GET /api/workspaces` (match by id or slug) instead of
  picking `workspaces[0]`, and errors clearly on no match — otherwise
  identical to today's auto-watch behavior.

## Frontend (coordination point, not this PR)

A confirmation page at `/device`:
- Bare `verification_uri` (no query): the user types `user_code` and
  submits (RFC 8628 §3.3).
- `verification_uri_complete`: the page displays the `user_code` and
  requires the user to confirm it matches the device before Approve/Deny
  (RFC 8628 §3.3.1 / §5.4).
`GET /api/device/pending?user_code=...` loads display info; Confirm/Deny
hit `POST /api/device/confirm`.

## Security notes

- `device_code` is the actual bearer secret (RFC 8628 explicitly relies on
  it being unguessable) — 32 random bytes, hashed at rest, single-claim.
- `user_code` is short/human-typable by design (that's the point of the
  flow) but is **not** itself a secret an attacker gaining it can silently
  exploit: confirming it requires being logged in as *some* Multica user,
  and the resulting PAT belongs to whoever clicks confirm, not whoever typed
  the code — same trust model RFC 8628 assumes (the code is a low-stakes
  pairing token, not an auth credential). Unlike the email verification-code
  flow (which compares a submitted guess against one known-email's stored
  code, so a per-row attempt counter throttles guessing against that one
  target), `user_code` **is** the lookup key here — there's no target to
  guess against, only the whole keyspace. Protection is keyspace size (8
  chars, ~32-letter disambiguated alphabet ≈ 10^12 combinations) against a
  thin attack surface (at most a handful of rows are ever `pending`
  simultaneously) within the 10-minute TTL — no separate attempt counter
  needed; one wouldn't throttle a blind guesser trying a different code (and
  therefore a different row) on every attempt anyway.
- 10-minute TTL matches the existing verification-code precedent exactly —
  no new expiry policy to justify.

## Non-goals (this task)

- No change to the loopback-browser code path's *existence* — deleting
  `runAuthLoginBrowser` outright is a follow-up cleanup once device-code has
  soaked, not bundled into this PR (smaller diff, easier revert if the new
  flow has an issue).
- No workspace-scoping on the PAT itself — `multica setup <workspace>` only
  changes which workspace the CLI *defaults to*, same as today's
  first-workspace auto-pick; it does not change what the PAT can access.
