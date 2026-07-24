# Voice call callback ingress

## Goal

Accept and authenticate Volcengine RTC server callbacks, decode the documented
conversation-status payload, and hand validated events to a lifecycle
processor without exposing the endpoint to unauthenticated state changes.

## Provider contract checked

- Volcengine `StartVoiceChat` configures the callback URL and shared callback
  signature:
  <https://www.volcengine.com/docs/6348/1807452?lang=zh>
- The official BytePlus RTC mirror documents the direct callback JSON envelope
  and the `conv` + big-endian length + JSON binary frame:
  <https://docs.byteplus.com/id/docs/byteplus-rtc/docs-1415216>
- The provider status reference defines conversation stages 0 through 5 and
  error details:
  <https://docs.byteplus.com/ko/docs/byteplus-rtc/docs-1928198>

## Delivery record

- [x] Added a bounded Base64/TLV decoder for conversation status callbacks.
- [x] Validated the `conv` magic, exact payload length, task/user identity,
  event time, supported stage range, and required provider error details.
- [x] Added a public POST callback route because the external RTC provider
  cannot hold a Multica member session.
- [x] Required the provider's configured shared signature and compared it in
  constant time before decoding or processing the callback payload.
- [x] Accepted callbacks without a `Content-Type` header, matching the provider
  example.
- [x] Rejected malformed, trailing, oversized, incorrectly signed, and invalid
  binary payloads with explicit HTTP status codes.
- [x] Returned 503 while the callback processor or signature is not configured,
  so a partially configured runtime cannot acknowledge and discard events.
- [x] Added handler, decoder, error-stage, size-limit, and route-presence tests.
- [x] Ran targeted tests with a disposable database migrated through version
  215, then dropped it.
- [x] Committed, pushed, and opened independent ready PR
  [#1086](https://github.com/LRM-Teams/multica/pull/1086), stacked on
  [#1084](https://github.com/LRM-Teams/multica/pull/1084).

## Boundary

This PR establishes authenticated ingress and a typed processor interface. It
does not mutate call sessions yet. The next PR implements idempotent session
state changes for duplicate and out-of-order provider events and wires that
processor into the runtime.

The repository's shared local database still has an inconsistent migration 204
state. Handler tests therefore used a disposable database; no shared database
or product code was modified to conceal that test-environment defect.
