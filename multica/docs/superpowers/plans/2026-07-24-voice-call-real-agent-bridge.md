# Voice call real-agent bridge

## Goal

Route every RTC speech turn through the existing Multica agent execution
runtime so the caller talks to the configured Beckham agent with its normal
memory, tools, permissions, and channel history.

## Boundaries

- Volcengine RTC owns media transport, ASR, interruption, and TTS.
- Multica owns agent identity, context, execution, tools, and persisted replies.
- Voice turns use the existing authenticated member-agent DM. They are not a
  second conversation store.
- Provider requests are authenticated with a dedicated server-only bearer
  token. RTC credentials and that token stay in deployment secrets.
- An interrupted HTTP stream does not roll back a durable agent task.

## Architecture findings

- [x] The current provider configuration uses `LLMConfig.Mode = "ArkV3"`.
  That calls Ark directly and only gives it a one-time context snapshot; it
  does not execute the Multica agent.
- [x] Volcengine `CustomLLM` calls a configured HTTPS URL using an
  OpenAI-compatible chat request and requires an OpenAI-compatible SSE
  response.
- [x] `LLMConfig.Custom` can carry a JSON business identifier and
  `EnableRoundId` adds a provider round identifier to each request.
- [x] A direct DM message already creates a durable `agent_inbox_event`. Its
  ID is copied to the prompt `chat_message.task_id`, and completion stores the
  exact assistant result with the same task ID.
- [x] Agent inbox execution is globally serial per agent. The bridge must wait
  for the event it created rather than treating the newest channel reply as
  its response.

## Delivery slices

- [x] PR 1 (#1166): configure RTC `CustomLLM`, per-call identity, and deployment
  settings.
- [x] PR 1b (#1172): reject partial RTC secret configuration before deployment
  changes the running containers.
- [x] PR 2 (#1168): authenticate and validate the OpenAI-compatible RTC
  endpoint.
- [x] PR 3 (#1169): persist an idempotent DM speech turn and dispatch the
  selected agent.
- [x] PR 4 (#1171): wait for the exact durable completion and stream it as SSE.
- [ ] PR 5: configure deployment secrets and verify a production call.

## Failure semantics

- Invalid bearer token: reject before parsing conversation data.
- Unknown, ended, or mismatched call: reject without creating a message.
- Duplicate `(call, round)` request: reuse the original channel message and
  inbox event.
- Request cancellation: stop waiting; do not delete or fabricate completion.
- Agent `failed`, `held`, or explicit `no_reply`: return a typed terminal
  response; do not substitute a second model.
- Wait timeout: return an SSE error completion and retain the durable task for
  channel delivery.

## Verification log

- [x] `go test ./internal/integrations/volcenginertc`
- [x] `go test ./internal/service/voicecall`
- [x] Voice-call wiring tests in `./cmd/server`
- [x] Self-host deployment contract now checks the CustomLLM URL and API key;
  the obsolete Ark endpoint assertion was removed.
- [x] `bash scripts/selfhost-config.test.sh`
- [x] RTC deployment preflight tests cover disabled, complete, partial,
  optional-only opt-in, blank, and secret-value non-disclosure cases.
- [x] RTC LLM endpoint tests against a fresh, fully migrated PostgreSQL
  database: bearer authentication runs before transcript parsing, numeric and
  string round IDs normalize, malformed/oversized requests fail, internal
  errors stay out of HTTP responses, and successful replies use
  OpenAI-compatible SSE content/final chunks plus `[DONE]`.
- [x] Voice-call Agent bridge tests against a fresh, fully migrated PostgreSQL
  database: the message and inbox event commit atomically, duplicate
  `(call, round)` requests reuse both durable IDs, the live-call prompt keeps
  the normal Multica runtime/tools boundary, and inactive/provider-mismatched/
  DM-agent-mismatched calls write nothing.
- [x] Exact-result tests against a fresh, fully migrated PostgreSQL database:
  the bridge waits for its own inbox event, prefers exact transport-audit
  messages, falls back only to assistant messages with the same task ID, and
  never speaks unrelated channel output.
- [x] Terminal and timeout tests: `no_reply`, `held`, `failed`, and replied
  without content return typed errors; the configurable 90-second timeout
  preserves the durable inbox task for later channel delivery.
- [x] `go vet ./internal/handler ./cmd/server`
- [ ] Full `./cmd/server` currently fails in the pre-existing
  `TestAgentsThroughRouter`: the local integration database returns 500 while
  loading an agent detail. The focused voice-call tests pass, and the failing
  path does not reference this change. It must be checked separately against a
  freshly migrated database before being classified as a product regression.
