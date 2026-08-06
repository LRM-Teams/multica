import { availableParallelism } from "node:os";
import { defineConfig } from "vitest/config";
import path from "path";

export default defineConfig({
  test: {
    // Mirrors packages/views/vitest.config.ts: threads pool + happy-dom +
    // at-least-2 workers (see the worker-count note there).
    pool: "threads",
    maxWorkers: Math.max(2, availableParallelism() - 1),
    environment: "happy-dom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["**/*.test.{ts,tsx}"],
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "."),
      "@core": path.resolve(__dirname, "core"),
    },
  },
});
