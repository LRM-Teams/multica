# Honor fleet sample threshold fix

Date: 2026-08-04

## Delivery record

- [x] Confirmed that fleet calculation reads the workspace rule while the API response drops it.
- [x] Added failing service, response-mapping, API compatibility, and UI regressions.
- [x] Return the configured minimum sample count from every fleet rank response.
- [x] Render the response value in the Agent detail card.
- [x] Run focused tests, typecheck, lint, and React Doctor.

## Verification

- Go service threshold regression: passed.
- Go handler response-mapping regression: passed against the worktree-isolated database.
- Core schema tests: 4 passed.
- Views fleet-card and identity consumer tests: 110 passed, 2 skipped.
- TypeScript typecheck: passed.
- React Doctor: 0 issues in changed Core and Views files.
- Core lint: passed; Views lint reported only 12 pre-existing warnings outside the changed files.
