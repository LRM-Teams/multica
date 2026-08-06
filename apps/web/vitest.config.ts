import { defineConfig } from "vitest/config";
import path from "path";

export default defineConfig({
  test: {
    // Mirrors packages/views/vitest.config.ts: threads pool + happy-dom.
    pool: "threads",
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
