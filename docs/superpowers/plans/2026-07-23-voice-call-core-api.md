# Voice call core API client

## Goal

Expose the authenticated voice-call create, get, and stop endpoints through the
shared web/desktop API client with schema-checked responses and no RTC token
leakage.

## Delivery record

- [x] Checked the implemented Go handler and router contracts instead of
  inventing frontend endpoint shapes.
- [x] Added shared call, media, create-request, create-response, and
  get-response types.
- [x] Added lenient schemas that preserve unknown provider status strings for a
  later UI `default` branch.
- [x] Defaulted omitted nullable timestamps and optional terminal details.
- [x] Required non-empty app, room, participant, token, and expiry fields
  before returning a create result.
- [x] Added workspace-scoped create, get, and idempotent stop client methods.
- [x] URL-encoded workspace and call path segments.
- [x] Made malformed create responses fail closed with a typed 502 instead of
  attempting RTC with empty credentials.
- [x] Made malformed get/stop responses degrade to an explicit `unknown` call
  instead of throwing into the UI.
- [x] Found that generic schema diagnostics would log a malformed response
  containing the short-lived RTC token.
- [x] Added an optional redacted diagnostic value to `parseWithFallback` and
  used it for call creation.
- [x] Added a regression proving credential-like fields never reach schema
  logs.
- [x] Added request-path/body, enum drift, malformed response, create
  fail-closed, and stop tests.
- [x] Ran 84 targeted schema/client tests.
- [x] Ran the full `@multica/core` test suite: 85 files and 825 tests passed.
- [x] Ran core typecheck and lint.
- [x] Ran React Doctor: 0 issues.
- [x] Committed, pushed, and opened independent ready PR
  [#1093](https://github.com/LRM-Teams/multica/pull/1093), stacked on
  [#1091](https://github.com/LRM-Teams/multica/pull/1091).

## Boundary

This client does not open a microphone, join an RTC room, own call UI state, or
render a call surface. Those capabilities remain separate changes.
