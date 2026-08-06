# Voice call proactive greeting repair

## Goal

Prepare caller media credentials first, join the browser to the RTC room, and
only then start the Volcengine agent with its welcome message. Keep the UI in
the connecting state until the provider start request succeeds.

## Boundaries

- Keep voice messages and voice calls as separate media paths.
- Do not add retry providers, browser speech fallbacks, or synthetic greetings.
- Keep Volcengine credentials on the server.
- Preserve the existing one-to-one DM call scope.
- Apply the fix to the dynamic workspace route used by
  `https://leagent.me/{workspaceSlug}/channels/{channelId}`.

## Production evidence

- The reported call connected in the UI but Beckham did not play the configured
  welcome message.
- Session `d2e0a372-918f-4e4b-aca2-3263e689eb2c` used room
  `voice-call-c3ff4409-23be-48eb-bebe-460a8b82319e` and task
  `voice-task-c3ff4409-23be-48eb-bebe-460a8b82319e`.
- The session ran from `2026-07-29 06:23:18Z` to `06:23:36Z`, ended by the
  caller, and recorded no provider callback, connected timestamp, input audio,
  or output audio.
- Volcengine `ListUserInfo` and `ListRoomInfo` returned no room/user record for
  that exact room and time window. This proves the current UI's local
  `connected` state is not proof that the provider agent joined.
- Production uses the intended RTC AIGC App ID/AppKey and the legacy
  `2024-12-01` API required by that application. The current API version is not
  changed in this repair.

## Source comparison

- Volcengine documents `AgentConfig.WelcomeMessage` as the greeting emitted
  when the agent starts.
- The official `volcengine/rtc-aigc-demo` performs these operations in order:
  create engine, register listeners, join the RTC room, then call
  `StartVoiceChat`.
- Multica currently calls `StartVoiceChat` inside the create-call request,
  returns RTC credentials afterwards, and only then lets the browser join.
- The repair now makes the same ordering explicit: create the durable session
  and sign caller media credentials, join the browser, atomically claim the
  provider start, then call `StartVoiceChat` with `WelcomeMessage`.

## Hypotheses

1. **Confirmed implementation mismatch:** provider startup occurred before
   caller room join; the official flow starts it after join.
2. **Confirmed observability defect:** local capture/publish completion is
   displayed as a connected call even without provider evidence.
3. **Ruled out:** missing welcome-message configuration. The field is present
   in the provider payload and is the documented startup greeting field.
4. **Not independently reproduced in this environment:** live browser SDK
   console events, because no authenticated in-app browser is connected.

## Implementation checklist

- [x] Read the repository rules and voice-call contracts.
- [x] Inspect the production session, provider callbacks, room history, and
      safe runtime configuration.
- [x] Compare Multica's call sequence and token format with the official demo.
- [x] Add a failing service/API/controller regression that requires browser
      room join before provider startup.
- [x] Split caller media preparation from provider startup without exposing
      provider credentials.
- [x] Start the provider with the welcome message only after the browser media
      session has joined the RTC room.
- [x] Atomically claim provider startup so duplicate connect requests cannot
      start duplicate agents.
- [x] Keep cancellation, failure recording, compensation, and stop behavior
      idempotent across the new boundary.
- [x] Run focused Go and TypeScript tests, lint/typecheck, React Doctor, and the
      repository verification pipeline.
- [ ] Push one independent PR, inspect CI, merge it, and verify the deployed
      service before asking for another live call.

## Verification record

- Initial baseline: `origin/dev@b244095e4`; the branch is rebased before
  publication.
- Official demo version inspected: `1.6.0`, Web SDK `~4.66.20`.
- Multica Web SDK: `~4.68.5`.
- Red proof: the previous service called provider `StartVoiceChat` during the
  create request; no post-room-join provider-start operation existed.
- Focused Go verification passed for the Volcengine client, voice-call service,
  provider adapter, HTTP handler, and server router.
- Focused frontend verification passed: 43 tests across the API client,
  mutations, controller, and RTC media session.
- TypeScript passed for all web workspace packages.
- Changed-file ESLint passed. React Doctor reported zero issues.
- The frontend repository test command passed all six packages, including
  2,968 view tests and 950 core tests.
- The full Go command reached unrelated existing failures in `server/pkg/agent`
  (daemon timing/path tests). Every changed Go package passed independently.
- A sqlc 1.31.1 generation exposed unrelated duplicate manual agent-skill
  outputs. Only the generated `voice_call.sql.go` result was retained; all
  unrelated generated files were restored.
- The deployment target was independently checked: Caddy on
  `101.200.210.144` routes `leagent.me` to the Multica frontend/backend, and the
  supplied workspace channel URL returns HTTP 200 from that host.
- Browser verification is still required after deployment; unit tests cannot
  prove that Volcengine delivered audible remote audio.
