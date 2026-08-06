import { availableParallelism } from "node:os";
import { defineConfig } from "vitest/config";

// Environment is happy-dom (2026-08-06), replacing jsdom. The suite's cost was
// never the assertions: per-file environment construction + module re-import
// dominated (see docs/superpowers/specs/2026-07-29-frontend-test-cost-design.md
// §2). happy-dom builds its DOM an order of magnitude cheaper and its DOM ops
// are faster, so the whole suite moved ~3x locally (192.7s → 56.3s wall, same
// machine, identical pass set) without giving up per-file isolation — the
// property that keeps this suite deterministic (isolate:false and vmThreads
// were both measured and rejected for load-order-dependent failures).
//
// Files that genuinely assert jsdom-only behavior (CSSOM cascade in
// getComputedStyle) opt out per file with `// @vitest-environment jsdom`,
// which is why jsdom stays in devDependencies.
//
// Worker count: at least 2 workers even on the 2-vCPU runner (where vitest's
// default `availableParallelism() - 1` would give 1). The jsdom-era
// measurement (run 30445676119) showed a second worker doubling every phase
// for zero wall gain — that was jsdom saturating both cores from one worker.
// Re-measured on the happy-dom + threads stack in a --cpus=2 container:
// 1 worker 119.1s, 2 workers 88.5s (-26%), 3 workers 103.3s (oversubscribed).
// Multi-core dev machines keep the default (availableParallelism - 1).

export default defineConfig({
  test: {
    // threads (worker_threads) over the default forks (child processes):
    // same per-file isolation, lower worker spawn/IPC cost. Measured locally
    // on the full suite: 56.3s → 46.1s wall, identical pass set.
    pool: "threads",
    maxWorkers: Math.max(2, availableParallelism() - 1),
    environment: "happy-dom",
    environmentOptions: {
      happyDOM: {
        settings: {
          navigator: {
            // happy-dom's default UA contains "Windows NT", which flips
            // platform detection (e.g. defaultDaemonSetupMode) to Windows.
            // Pin a Linux UA so navigator.platform stays non-Mac/non-Windows,
            // matching what the suite observed under jsdom.
            userAgent:
              "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HappyDOM/20.0.0",
          },
        },
      },
    },
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["**/*.test.{ts,tsx}"],
  },
});
