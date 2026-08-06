import { availableParallelism } from "node:os";
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // threads pool + at-least-2 workers, mirroring packages/views (see the
    // worker-count note there). Node environment — no DOM cost here; this
    // only trims worker spawn overhead on the 2-vCPU CI runner.
    pool: "threads",
    maxWorkers: Math.max(2, availableParallelism() - 1),
    globals: true,
    include: ["**/*.test.{ts,tsx}"],
    passWithNoTests: true,
  },
});
