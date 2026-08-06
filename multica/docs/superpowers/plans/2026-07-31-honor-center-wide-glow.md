# Honor Center Wide Layout and Progressive Identity Effects

## Goal

- Let the honor tab use the available settings content width without changing the width of other settings tabs.
- Replace underline-based honor names with level-driven color, glow, and slow breathing effects.
- Make equipped honor badges visibly luminous on every shared actor surface.
- Preserve readable text, compact inline rendering, and reduced-motion behavior.

## Constraints

- Keep server state in React Query and reuse the existing honor dashboard and public snapshot data.
- Implement shared identity effects in `packages/ui` and shared product composition in `packages/views`.
- Do not introduce app-specific copies or browser globals.
- Inline chat/member surfaces remain capped to avoid excessive animation density.

## Verification

- Add unit coverage for level tiers, surface caps, pulse timing, and display props.
- Run affected Vitest suites, package typechecks, ESLint, React Doctor, and the web production build.
- Let CI run the repository-wide pipeline after the focused local checks pass.
- Open a PR to `dev`, wait for CI, merge, wait for deployment, and check production health.

## Progress

- [x] Located the shared `max-w-3xl` settings constraint.
- [x] Located underline-based honor name CSS and static badge frame.
- [x] Implemented the layout and shared visual system changes.
- [x] Passed focused unit tests, package typechecks, ESLint, React Doctor, and web build.
- [ ] Publish, merge, deploy, and verify.
