import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// LRM-1170 gate-shot harness (temporary tooling).
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  // No package aliases: pnpm's workspace links let each package resolve its
  // own `exports` map (e.g. `@multica/ui/i18n-types`), which a directory alias
  // would flatten and break.
  server: { port: 5199, strictPort: true },
});
