# Voice call ringback

## Goal

Play outgoing call progress audio from the moment a member starts a voice call
until RTC media connects. Stop immediately on connect, failure, cancellation,
hang-up, terminal server state, or component teardown.

## Evidence and execution log

- [x] Inspect the deployed call UI and controller: connecting status had no
  audio source, Web Audio session, or ringback lifecycle.
- [x] Confirm proactive Agent speech already uses the RTC
  `AgentConfig.WelcomeMessage` built from the member and Agent identity;
  the context, provider, and RTC configuration regression tests passed.
- [x] Add a controller regression that starts a call with a delayed create
  response. It failed because no ringback factory was called.
- [x] Implement a local 450 Hz, one-second-on/four-seconds-off outgoing
  ringback and bind its lifetime to the call controller.
- [x] Verify controller lifecycle and Web Audio cleanup: 12 focused tests
  passed; the full `@multica/views` suite passed 2,818 tests with 5 skipped.
- [x] Verify TypeScript, focused ESLint, and React Doctor: all passed with
  zero new diagnostics.
- [x] Push one independent PR into `dev`: [#1345](https://github.com/LRM-Teams/multica/pull/1345).

## Boundaries

- Ringback is local progress feedback. Failure to initialize browser audio does
  not fail the RTC call.
- Ringback never continues over remote Agent audio.
- No server API or provider configuration changes are required.
