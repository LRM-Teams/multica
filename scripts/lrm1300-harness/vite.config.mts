import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/** LRM-1300 gate-shot harness — real Sheet (BEFORE) vs real AlertDialog (AFTER). */
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5300, strictPort: true },
});
