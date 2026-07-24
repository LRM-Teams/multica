# Authenticated voice call member API

## Goal

Expose create, get, and idempotent stop operations for one-to-one Agent calls
without accepting identity authority or provider credentials from the client.

## Contract

- `POST /api/workspaces/{workspace_id}/voice-calls`
- `GET /api/workspaces/{workspace_id}/voice-calls/{call_id}`
- `POST /api/workspaces/{workspace_id}/voice-calls/{call_id}/stop`

## Delivery record

- [x] Require the existing login, workspace membership, and human-actor route
  guards.
- [x] Derive workspace and member authority from the URL/middleware and
  authenticated request; create input accepts only `channel_id` and `agent_id`.
- [x] Reject unknown fields, multiple JSON values, malformed UUIDs, and bodies
  above 8 KiB before calling the lifecycle service.
- [x] Return call state without provider name or provider task identity.
- [x] Return only the short-lived, room-scoped RTC values needed by the client
  on create and mark every successful response `Cache-Control: no-store`.
- [x] Keep stop idempotent through the lifecycle service and use the
  server-owned `user_hangup` reason.
- [x] Map the stable service errors to sanitized 403, 404, 409, 502, and 503
  response codes; retain full wrapped errors only in server logs.
- [x] Add request scope, response exposure, strict input, error mapping,
  configuration, and authentication tests.
- [x] Run all voice-call handler tests against a fresh fully migrated
  PostgreSQL database, all lifecycle service tests, router compilation, vet,
  and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1082](https://github.com/LRM-Teams/multica/pull/1082), stacked on #1081.
