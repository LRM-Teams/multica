import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const here = import.meta.dirname;

/**
 * LRM-1348 gate-shot harness (temporary tooling).
 *
 * Mounts the REAL `ChannelPresenceCluster`, including the REAL Base UI
 * `HoverCard` (desktop) / `Popover` (narrow) Portal overlay, because the defect
 * only exists in a real browser: Chromium moves focus to `<body>` when the
 * focused element becomes natively `disabled`, and the overlay reads that
 * focus-out as a dismiss and unmounts its whole subtree. jsdom emulates neither
 * step, so the unit spec can only guard attributes.
 *
 * Only the data/identity dependencies are stubbed (workspace id, actor
 * directory, member-profile query, activity projection, avatar chrome). The
 * component under test, the Button, and the overlay primitives are the shipped
 * ones — nothing in the focus path is faked.
 */
function stubModules() {
  const rules = [
    [/^@multica\/core\/hooks$/, resolve(here, "stubs/core-hooks.ts")],
    [/^@multica\/core\/workspace\/hooks$/, resolve(here, "stubs/workspace-hooks.ts")],
    [/^@multica\/core\/workspace\/queries$/, resolve(here, "stubs/workspace-queries.ts")],
    [/^\.\.\/\.\.\/common\/actor-avatar$/, resolve(here, "stubs/actor-avatar.tsx")],
    [/^\.\.\/\.\.\/agents\/use-agent-live-status$/, resolve(here, "stubs/agent-live-status.ts")],
  ];
  return {
    name: "lrm1348-stubs",
    enforce: "pre" as const,
    resolveId(source: string) {
      for (const [pattern, file] of rules) {
        if ((pattern as RegExp).test(source)) return file as string;
      }
      return null;
    },
  };
}

export default defineConfig({
  root: resolve(here, "."),
  plugins: [stubModules(), react(), tailwindcss()],
  server: { port: 5348, strictPort: true },
});
