# Views test-cost baselines (LRM-694 / #1364 Step 0)

Committed artifacts so later shared-fixture (direction D) work can be **measured**,
not asserted. See `docs/superpowers/specs/2026-07-29-frontend-test-cost-design.md`.

| Field | Vitest 4 source | Design-doc name |
|---|---|---|
| `import_ms` | `ModuleDiagnostic.collectDuration` | import |
| `environment_ms` | `ModuleDiagnostic.environmentSetupDuration` | environment |

## Regenerate (cache-miss, runner-shaped)

From `packages/views`:

```bash
./scripts/record-import-env-baseline.sh
```

This clears Vitest/Vite caches, runs with `--maxWorkers=1` (matches the measured
GitHub-hosted `nproc=2` shape in `vitest.config.ts`), and writes
`import-env-baseline.json`.

**Do not quote these numbers as CI wall-time recovered.** Local hardware differs;
re-measure on CI before any performance claim (#1389).
