// LRM-1362 harness — compile the app's REAL Tailwind CSS (same entry as
// apps/web/app/globals.css, so tokens.css + base.css keyframes and the
// `motion-reduce:` variant come from production config), then measure the three
// live components' computed animation under prefers-reduced-motion in real
// Chromium.
//
// Usage: node scripts/lrm1362-harness/build-css.mjs

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import postcss from "postcss";
import tailwind from "@tailwindcss/postcss";

const root = resolve(import.meta.dirname, "../..");
const entry = resolve(root, "apps/web/app/globals.css");
const outDir = resolve(root, "artifacts/lrm1362");

await mkdir(outDir, { recursive: true });

const css = await readFile(entry, "utf8");
const result = await postcss([tailwind()]).process(css, {
  from: entry,
  to: resolve(outDir, "app.css"),
});

await writeFile(resolve(outDir, "app.css"), result.css, "utf8");
console.log("wrote", resolve(outDir, "app.css"), result.css.length, "bytes");
console.log("dirname check", dirname(entry));
