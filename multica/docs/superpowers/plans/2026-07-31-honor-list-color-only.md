# Dense Honor Lists Use Name Color Only

## Goal

- Remove honor and fleet badge icons from dense conversation and member lists.
- Keep the earned username color, glow tier, truncation, and profile/message badge displays.

## Scope

- Direct-message search results.
- Existing direct-message conversation rows.
- Channel member rows.

## Verification

- Add unit coverage proving name styling remains while the equipped badge is omitted.
- Run the focused unit test, `@multica/views` typecheck, affected-file ESLint, and React Doctor.
- Publish a separate PR to `dev`, wait for CI, merge, and verify the resulting deployment.

## Progress

- [x] Added a shared dense-list badge visibility switch.
- [x] Applied the switch to the three list rendering paths.
- [x] Passed focused local verification.
- [ ] Publish, merge, deploy, and verify.
