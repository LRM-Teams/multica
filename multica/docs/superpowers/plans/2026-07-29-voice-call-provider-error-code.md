# Voice call provider error-code repair

## Goal

Preserve the bounded Volcengine Web RTC error code shown to a member when a
call fails, without exposing arbitrary provider error messages.

## Evidence

- Production call sessions create a provider task and room but never receive a
  connected callback; all observed audio durations remain zero.
- `@volcengine/rtc` 4.68.5 declares failure codes such as `INVALID_TOKEN` and
  `JOIN_ROOM_FAILED`.
- SDK method rejections carry the code on `error.code`.
- The current adapter accepts only numeric strings and drops codes from rejected
  `joinRoom`, microphone, and publish promises.

## Checklist

- [x] Confirm the installed SDK contract from its checked-in TypeScript types.
- [x] Add a failing regression for enum callback codes and rejected SDK calls.
- [x] Parse only bounded numeric or uppercase enum codes at the SDK boundary.
- [x] Run focused tests, typecheck, lint, and React Doctor.
- [x] Push an independent PR to `dev`: [#1371](https://github.com/LRM-Teams/multica/pull/1371).
- [x] Rebase onto `dev` after the ringback PR merged, then repeat focused tests,
  typecheck, lint, and React Doctor.

## Boundary

The UI may show a provider code only when it is either a short signed integer or
an uppercase underscore-delimited enum. Raw messages, stack traces, credentials,
and arbitrary object values remain hidden.
