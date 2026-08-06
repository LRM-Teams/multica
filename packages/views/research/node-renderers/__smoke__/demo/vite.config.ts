import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/**
 * Dev-only Vite server for the LRM-1475 node-card demo (screenshot tooling).
 * Not part of the production bundle. Run:
 *   npx vite --config packages/views/research/node-renderers/__smoke__/demo/vite.config.ts
 */
export default defineConfig({
  root: __dirname,
  plugins: [react(), tailwindcss()],
  server: { port: 5199, host: "127.0.0.1" },
  preview: { port: 5200, host: "127.0.0.1" },
  resolve: {
    preserveSymlinks: true,
  },
});
