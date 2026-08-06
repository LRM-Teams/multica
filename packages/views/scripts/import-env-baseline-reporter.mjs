/**
 * Vitest reporter for LRM-694 / #1364 Step 0.
 *
 * Emits per-file `collectDuration` (module import / "import" in the design
 * doc) and `environmentSetupDuration` ("environment") so later D/fixture
 * work can be measured against a committed baseline — not asserted from
 * vibes. Behaviour-neutral: does not change which tests run.
 *
 * Design: docs/superpowers/specs/2026-07-29-frontend-test-cost-design.md §5 Step 0.
 */
import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { cpus } from "node:os";

const defaultOut = resolve(
  fileURLToPath(new URL("../test/baselines/import-env-baseline.json", import.meta.url)),
);

function outPath() {
  return resolve(process.env.VIEWS_TEST_COST_BASELINE_OUT || defaultOut);
}

function collectFromModule(testModule, root) {
  const d = testModule.diagnostic?.() ?? testModule.diagnostic;
  if (!d || typeof d !== "object") return null;
  const moduleId = testModule.moduleId || testModule.filepath || "";
  return {
    file: relative(root, moduleId),
    // Design-doc names → Vitest 4 ModuleDiagnostic fields.
    import_ms: d.collectDuration ?? 0,
    environment_ms: d.environmentSetupDuration ?? 0,
    setup_ms: d.setupDuration ?? 0,
    prepare_ms: d.prepareDuration ?? 0,
    tests_ms: d.duration ?? 0,
  };
}

/** @type {import('vitest/node').Reporter} */
export default class ImportEnvBaselineReporter {
  constructor() {
    this.files = [];
    this.startedAt = Date.now();
    this.root = process.cwd();
  }

  onInit(ctx) {
    this.ctx = ctx;
    this.root = ctx?.config?.root || process.cwd();
  }

  onTestModuleEnd(testModule) {
    const row = collectFromModule(testModule, this.root);
    if (row) this.files.push(row);
  }

  onTestRunEnd(testModules) {
    // Prefer the per-module stream; fall back to the final module list if empty.
    if (this.files.length === 0 && Array.isArray(testModules)) {
      for (const mod of testModules) {
        const row = collectFromModule(mod, this.root);
        if (row) this.files.push(row);
      }
    }
    this.files.sort((a, b) => a.file.localeCompare(b.file));
    const sum = (key) => this.files.reduce((acc, f) => acc + (f[key] || 0), 0);
    const dest = outPath();
    const artifact = {
      schema: "multica.views.test-cost-baseline/v1",
      purpose:
        "Step 0 baseline for frontend test cost (#1364 / LRM-694). Per-file import (collect) + environment costs. Not a CI-seconds claim.",
      design_doc: "docs/superpowers/specs/2026-07-29-frontend-test-cost-design.md",
      recorded_at: new Date().toISOString(),
      shape: {
        mode: "cache-miss",
        runner_like: {
          nproc: cpus().length,
          maxWorkers: this.ctx?.config?.maxWorkers ?? null,
          isolate: this.ctx?.config?.isolate ?? true,
        },
        note: "Local machine approximating runner shape (cache cleared before run). Re-measure on CI before claiming any wall-time delta.",
      },
      totals: {
        files: this.files.length,
        import_ms: sum("import_ms"),
        environment_ms: sum("environment_ms"),
        setup_ms: sum("setup_ms"),
        prepare_ms: sum("prepare_ms"),
        tests_ms: sum("tests_ms"),
        wall_ms: Date.now() - this.startedAt,
      },
      files: this.files,
    };
    mkdirSync(dirname(dest), { recursive: true });
    writeFileSync(dest, `${JSON.stringify(artifact, null, 2)}\n`);
    console.log(`[import-env-baseline] wrote ${dest} (${artifact.totals.files} files)`);
  }
}
