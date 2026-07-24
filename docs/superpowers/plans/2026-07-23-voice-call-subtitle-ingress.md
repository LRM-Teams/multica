# Voice call subtitle ingress

## Goal

Route authenticated Volcengine RTC `subv` callbacks into the durable voice-call
turn store without persisting streaming fragments or acknowledging lost final
transcripts.

## Provider contract checked

The official BytePlus RTC mirror documents the same server-side subtitle
callback used by Volcengine, including the Base64/TLV envelope, `subv` magic,
speaker `userId`, dialogue `roundId`, and the `paragraph` final-turn marker:
<https://docs.byteplus.com/en/docs/byteplus-rtc/docs-1337284>.

## Delivery record

- [x] Reproduced the missing subtitle route with handler and callback-service
  tests before changing production code.
- [x] Kept callback signature verification ahead of Base64/TLV decoding.
- [x] Switched the authenticated callback handler on the typed `conv`/`subv`
  discriminator.
- [x] Preserved existing conversation-status processing.
- [x] Sent subtitle callbacks to a dedicated callback-service method.
- [x] Ignored streaming fragments where `paragraph` is false.
- [x] Ignored the provider's documented whitespace-only final marker.
- [x] Required non-empty final paragraphs to be definite.
- [x] Derived the provider task from the call-scoped member or agent RTC
  identity; rejected unknown or malformed identities.
- [x] Rejected a signed subtitle batch that mixes identities from different
  calls before persisting any turn.
- [x] Assigned deterministic member/agent call sequences from `roundId`, with
  overflow validation.
- [x] Preserved transcript whitespace and assigned a retry-stable provider turn
  identity.
- [x] Propagated store failures as HTTP 500 so the provider can retry instead
  of losing a final transcript.
- [x] Added regression coverage for authenticated subtitle routing, status
  routing, signature rejection, final-turn mapping, filtering, invalid
  identity, mixed calls, overflow, and store failures.
- [x] Ran handler and runtime-wiring tests against a disposable fully migrated
  PostgreSQL database, then dropped it.
- [x] Ran integration-decoder and callback-service tests.
- [x] Ran authenticated handler and runtime-wiring tests against a disposable
  database.
- [x] Ran `go vet` for the integration, service, handler, and server wiring
  packages.
- [x] Committed, pushed, and opened independent ready PR
  [#1091](https://github.com/LRM-Teams/multica/pull/1091), stacked on
  [#1090](https://github.com/LRM-Teams/multica/pull/1090).

## Ordering rule

For provider round `r`, the member turn sequence is `2r + 1` and the agent turn
sequence is `2r + 2`. The formula is deterministic across callback retries and
does not depend on a racing `MAX(sequence) + 1` query.

## Retry boundary

Only streaming fragments and the documented empty final marker are
acknowledged without a write. Invalid identities, mixed-call batches, invalid
final flags, overflow, unknown calls, and database failures return an error so
the callback is not silently discarded.
