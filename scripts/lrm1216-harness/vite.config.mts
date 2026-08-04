import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const harnessDir = resolve(import.meta.dirname, ".");
const mockOpenDm = resolve(harnessDir, "mocks/use-open-dm.ts");

// LRM-1216 gate-shot harness (temporary tooling).
export default defineConfig({
  root: harnessDir,
  plugins: [
    {
      name: "lrm1216-mock-open-dm",
      enforce: "pre",
      resolveId(id) {
        if (
          id.endsWith("/common/use-open-dm") ||
          id.endsWith("/common/use-open-dm.ts") ||
          id === "../../common/use-open-dm" ||
          id.includes("views/common/use-open-dm")
        ) {
          return mockOpenDm;
        }
        return null;
      },
    },
    react(),
    tailwindcss(),
  ],
  server: { port: 5216, strictPort: true },
});
