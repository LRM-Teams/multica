import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// LRM-1164 AC4 gate-shot harness (temporary tooling).
// Mounts the REAL ReportReader / ResearchSessionRow / ResearchSessionRowSkeleton
// in a real browser so Tailwind `md:` (768) actually resolves — jsdom cannot
// evaluate media queries, which is why AC1/AC2/AC3 unit coverage is not enough
// for the 700 vs 768 gate.
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5201, strictPort: true },
});
