import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/** LRM-1304 gate-shot harness — message-row reading geometry BEFORE vs SPEC. */
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5304, strictPort: true },
});
