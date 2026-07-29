import { availableParallelism } from "node:os";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Vitest's default `maxWorkers` is `availableParallelism() - 1`. That reserves a
// core for the main process, which is the right call on a workstation but
// collapses to a single worker on a 2-vCPU CI runner — measured on run
// 30442811673, where this package's phase totals (645.3s) matched its wall time
// (672.5s), i.e. no parallelism at all, against 88s locally on ten cores.
//
// Using every core instead of every-core-but-one costs nothing and does not
// change the billed job count. The main process is mostly idle while workers
// run test files, so oversubscribing it by one is cheap here.
const maxWorkers = Math.max(1, availableParallelism());

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
