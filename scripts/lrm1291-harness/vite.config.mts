import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// LRM-1291 gate-shot harness (temporary tooling).
// Mounts the REAL `ResearchStageTimeline` in a real browser. The stage energy
// track is unverifiable in jsdom: dark band hues are authored `oklch()`, the
// upcoming hatch and the current wash use `color-mix()`, "one moving part" is a
// computed `animation-name`, and the reduced-motion downgrade is a media query.
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5291, strictPort: true },
});
