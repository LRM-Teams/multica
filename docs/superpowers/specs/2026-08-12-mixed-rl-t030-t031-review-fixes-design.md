# Mixed-RL T030/T031 Review Fixes Design

## Goal

Close the reviewed correctness gaps around trusted turn capture, canonical action
provenance, delivery-obligation idempotency, capture-boundary coverage, and
interaction-DAG tests without expanding into the remaining T035-T043 feature work.

## Scope

This change will:

- complete the T030 wire-to-service mapping for provider calls, visible actions,
  consumptions, counts, hashes, eligibility, ordinal validation, sensitive-field
  rejection, and late-capture responses;
- complete the T031 credential-proxy observation path for successful canonical
  message and reaction responses under an active daemon-owned provider/tool context;
- make delivery-obligation creation return the database's own inserted decision rather
  than infer it from a run-level counter;
- replace capture-boundary tests that inspect fabricated provider payload metadata with
  tests against the real capture-log offset boundary;
- make reaction-edge fixtures preserve the real reaction-to-message relationship;
- ensure newly added T027 and T032-T034 suites are tracked by the MultiCA repository.

This change will not implement unrelated offline trajectory, freeze, exporter, or
training assembly work.

## Trusted Turn Capture

`turnCaptureFromProtocol` remains the single conversion boundary between daemon wire
payloads and `service.TrustedTurnCapture`. It will reject the complete batch before any
database call when:

- batch, turn, run-agent, action, consumption, or canonical IDs are invalid;
- turn, call, or action ordinals are non-positive, duplicate, or regress within the
  batch;
- required payload, request, or response hashes are empty;
- provider request JSON contains transport authentication fields such as
  `authorization`, `api_key`, cookies, or access tokens;
- action or consumption references do not identify a call in the same batch;
- timestamps are malformed or completion precedes start.

The conversion will populate calls, actions, consumptions, and all three declared batch
counts. Training eligibility remains limited to completed, response-complete calls with
`stop` or `toolUse` stop reasons.

After `AcceptTrustedTurnCapture`, the HTTP handler will construct its response through
`turnCaptureResponseFromResult`. Normal acceptance returns batch identity, turn
identity, counts, and run status. Post-freeze acceptance additionally returns
`late=true` and the immutable snapshot ID. No raw provider payload enters the response.

## Canonical Action Provenance

The credential proxy will observe only an allowlisted pair of operations:

- canonical message send;
- canonical reaction creation.

For those operations it will buffer the upstream response up to a small fixed limit,
forward the same status, headers, and body to the caller, and inspect the buffered JSON
only after receiving a 2xx status. A valid canonical UUID will be associated with the
currently active daemon-owned provider/tool context. Non-2xx responses, malformed JSON,
missing IDs, unrelated API paths, unknown action kinds, and missing active contexts will
not create associations.

The proxy will never consume producer-call or tool-call identity from the agent request
body. Association state remains keyed by daemon and agent for this focused change;
lifecycle ownership and persistence beyond the current capture batch remain part of the
later provider-capture tasks.

## Delivery-Obligation Idempotency

The delivery-obligation SQL query will expose whether its upsert inserted a pending
obligation. `EnvDispatchActivity.CreateDeliveryObligation` will use that value directly
to update its local tracker and return `created`. It will no longer compare snapshots of
`pending_delivery_count`, eliminating the concurrent replay race where another
transaction's increment is mistaken for the current transaction's insertion.

## Interaction and Capture Tests

Tests will exercise production boundaries rather than helpers in isolation:

- credential-proxy tests will make actual proxy requests and assert automatic
  association for successful canonical message/reaction responses;
- turn-capture tests will invoke the HTTP handler and verify the response produced from
  the service result, including late routing;
- capture-boundary tests will write historical records before the recorded file offset
  and current records after it, then assert only current calls are uploaded;
- reaction tests will use a schema containing `channel_message_id` and create at least
  two candidate message segments so an implementation that selects the latest or only
  producer cannot pass accidentally;
- concurrent delivery tests will submit the same `(channel_message_id, run_agent_id)`
  twice and assert exactly one `created=true`, one counter increment, and one live
  notification decision.

Hidden provider retries require an observable logical-invocation correlation signal.
Tests will not infer retry identity solely from two adjacent request records. If Pi's
hook does not expose such a signal, the capture boundary will fail closed as a capture
gap rather than silently collapse potentially distinct logical calls.

## Error Handling and Compatibility

All validation errors remain request-scoped `400` responses, scope mismatches remain
`403`, and persistence conflicts remain `409`. Existing response fields are preserved;
the newly populated acknowledgement fields were already added as optional JSON fields.
No dependency, migration, public endpoint, or legacy compatibility layer is added.

## Verification

Verification will run focused Go tests for agent capture, credential proxy, handler
transport, delivery activity, resident runtime capture, and interaction DAG segment and
edge behavior. It will then run the affected package suites, race-sensitive tests where
supported, formatting, and repository checks. Database-backed suites will use the
existing test harness and will be reported explicitly if the required PostgreSQL test
environment is unavailable.
