# Honor Name Light-Theme Contrast

## Goal

- Keep earned color and glow effects readable on white message and list surfaces.
- Preserve brighter gold and prismatic treatments in dark mode.

## Change

- Replace pale light-theme gold gradient stops with deep amber and bronze stops.
- Darken light-theme prismatic amber, violet, and magenta stops.
- Remove white text-shadow wash from solid names, tier-II glow, and animated glow.

## Verification

- Add static CSS regression coverage for the light-theme gradient and glow rules.
- Run the focused honor test, package typechecks, affected-file ESLint, and React Doctor.
- Publish to `dev`, wait for CI, merge, and verify the resulting deployment.

## Progress

- [x] Reworked light-theme name colors and glow shadows.
- [x] Passed focused local verification.
- [ ] Publish, merge, deploy, and verify.
