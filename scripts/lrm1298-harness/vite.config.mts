import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/** LRM-1298 focus-contract harness — real AttachmentPreviewModal vs the
 *  verbatim origin/dev frame, driven by real Chromium Tab keys. */
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5298, strictPort: true },
});
