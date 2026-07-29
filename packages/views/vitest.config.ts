import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Worker count is left at vitest's default (`availableParallelism() - 1`) on
// purpose. Raising it to use every core was tried and measured on run
// 30445676119: effective parallelism went 0.96 → 1.89, so the extra worker
// really did start, but every phase total roughly doubled (environment
// 211.5s → 428.3s, import 205.2s → 403.8s) and wall time moved 672.5s → 657.4s,
// i.e. not at all. The runner reports `nproc=2`, and one worker already
// saturates it — jsdom, V8 and the compiler are themselves multi-threaded.
//
// So "cores - 1 wastes half the machine" is false here, and adding workers on
// one box only slices the same CPU more finely. Making CI faster needs either
// less work per file (import/environment are 62% of this suite) or more
// machines (sharding) — not a different worker count.

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["**/*.test.{ts,tsx}"],
  },
});
