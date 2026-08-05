import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/**
 * LRM-1346 gate-shot harness — message shell / group edges BEFORE (origin/dev)
 * vs AFTER (working tree). Both class sets are extracted from the component
 * source by `scripts/lrm1346-deborder-shots.mjs`, so the harness cannot drift
 * into photographing a fiction.
 */
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5346, strictPort: true },
});
