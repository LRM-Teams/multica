import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// LRM-1208 gate-shot harness (temporary tooling).
// Mounts the REAL ResearchGitList in a real browser so that
// `var(--research-lane-N)` actually resolves on the SVG `stroke` attribute and
// on the lane dot's inline `style.borderColor`. jsdom returns the literal
// `var(...)` string, so unit coverage alone cannot prove the dark contrast.
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5208, strictPort: true },
});
