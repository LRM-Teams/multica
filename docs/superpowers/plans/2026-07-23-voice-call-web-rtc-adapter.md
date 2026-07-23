# Voice call Web RTC adapter

## Goal

Provide one browser/Electron-renderer media session that joins the server-issued
Volcengine RTC room, publishes microphone audio, receives the agent's audio,
supports mute and browser autoplay recovery, and releases every acquired
resource.

## Interface evidence

- The official Web SDK package is `@volcengine/rtc`; the current package
  declaration exposes `createEngine`, `joinRoom`, audio capture, explicit audio
  publish, automatic audio subscription, manual `play`, `leaveRoom`, and
  `destroyEngine`.
- The installed SDK version is `~4.68.5`, which includes the Chrome
  compatibility changes documented by Volcengine.
- Runtime calls use the installed package's TypeScript declarations. The SDK is
  dynamically imported so importing shared views during server rendering does
  not execute browser media code.

## Delivery record

- [x] Installed `@volcengine/rtc` in the shared views package without changing
  unrelated lockfile entries.
- [x] Added a Web/Electron-renderer media adapter that checks SDK support before
  constructing an engine.
- [x] Joined with server-issued app, room, user, and short-lived token values;
  the token is never logged or persisted.
- [x] Disabled automatic local publication, enabled automatic remote audio
  subscription, and disabled video subscription.
- [x] Started microphone capture only after a successful room join, then
  explicitly published audio.
- [x] Implemented mute by both unpublishing audio and releasing microphone
  capture; either successful operation blocks outgoing audio if the other
  cleanup step fails.
- [x] Implemented unmute with rollback so a failed publish does not leave the
  microphone open unnecessarily, and reused the user's selected microphone.
- [x] Exposed autoplay-blocked events and an explicit user-gesture playback
  method using the SDK's `play` API.
- [x] Mapped connection loss to a reconnecting state and provider terminal
  errors to a typed failure.
- [x] Serialized terminal-error and user-disconnect cleanup to prevent duplicate
  stop, leave, or destroy calls.
- [x] Made disconnect idempotent and released capture, publication, room, event
  listeners, and engine resources.
- [x] Added ten tests covering startup order, unsupported browsers, permission
  failure cleanup, mute/unmute, partial mute cleanup, autoplay recovery,
  idempotent disconnect, typed provider errors, and concurrent cleanup.
- [x] Ran focused tests, views typecheck, and views lint; lint reported four
  existing warnings outside the changed files and no errors.
- [x] Ran the full `@multica/views` suite: 256 files passed, 2,500 tests
  passed, and 5 were skipped.
- [x] Verified the workspace lockfile with `--frozen-lockfile`.
- [x] Ran React Doctor: 0 issues.
- [x] Committed, pushed, and opened independent ready PR
  [#1102](https://github.com/LRM-Teams/multica/pull/1102), stacked on
  [#1100](https://github.com/LRM-Teams/multica/pull/1100).

## Boundary

This adapter owns RTC media resources. It does not create or stop the server
call record, render call controls, select a conversation agent, or keep the
short-lived media token in React Query or Zustand.
