import { resolve } from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// LRM-1339 gate-shot harness (temporary tooling).
// Mounts the REAL `ResearchProductRoundCardView` in a real browser.
// The defect is an alpha stack on top of a semantic tone: the summary spans
// inherit `text-brand` / `text-success` / `text-warning` / `text-muted-foreground`
// from `decisionTone` and then multiply it by `opacity-80` / `opacity-70` while
// sitting on the matching low-alpha wash. jsdom resolves neither the token nor
// the composited alpha, so only live Chromium can report the color the user
// actually sees.
export default defineConfig({
  root: resolve(import.meta.dirname, "."),
  plugins: [react(), tailwindcss()],
  server: { port: 5339, strictPort: true },
});
