import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// LRM-1252 gate-shot harness (temporary tooling).
// Mounts the REAL `ResearchStageTimeline` + `ExplorationRail` in a real browser.
// jsdom cannot resolve `oklch()`/`color-mix()` token values, and it never
// composites an ancestor `opacity-*` onto the text color — which is exactly the
// mechanism that pushed these two 11px labels under WCAG AA. Only a live
// Chromium can produce the real effective color for a contrast measurement.
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5252, strictPort: true },
});
