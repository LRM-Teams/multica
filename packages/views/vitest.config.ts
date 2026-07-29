import { availableParallelism } from "node:os";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Vitest's default `maxWorkers` is `availableParallelism() - 1`. Reserving a
// core for the main process is right on a workstation, where an editor and
// language server want it, but it collapses to a single worker on a 2-vCPU CI
// runner — measured on run 30442811673, where this package's phase totals
// (645.3s) matched its wall time (672.5s), i.e. no parallelism at all, against
// 88s locally on ten cores for the same suite.
//
// So override on CI only: there the box does nothing else, and using every core
// costs no extra billed minutes because it is the same job on the same machine.
// Locally the default stands, which is what the paragraph above argues for.
const maxWorkers = process.env.CI ? Math.max(1, availableParallelism()) : undefined;

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["**/*.test.{ts,tsx}"],
    maxWorkers,
  },
});
