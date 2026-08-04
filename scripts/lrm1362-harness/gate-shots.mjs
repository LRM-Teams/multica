// LRM-1362 gate shots — real Chromium, real compiled Tailwind CSS.
//
// Proves the two `infinite` custom keyframes actually stop under
// `prefers-reduced-motion: reduce`, and that the fallback keeps state readable
// (FAB stays brand-tinted; nav bar goes full width instead of freezing at 33%).
//
// Why the class strings are read out of the components instead of retyped here:
// a future edit to the components must be able to break this gate.
//
// Usage (deps installed):
//   cd apps/web && node ../../scripts/lrm1362-harness/build-css.mjs
//   node scripts/lrm1362-harness/gate-shots.mjs

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright";
import { twMerge } from "tailwind-merge";

// Same class composition the components use (`cn` = clsx + twMerge), so
// conflicting utilities resolve exactly as they do in the product. Composing
// raw strings here would let `text-muted-foreground` survive next to
// `text-brand` and silently under-report the fallback.
const cn = (...parts) => twMerge(parts.filter(Boolean).join(" "));

const root = resolve(import.meta.dirname, "../..");
const outDir = resolve(root, "artifacts/lrm1362");
await mkdir(outDir, { recursive: true });

const css = await readFile(resolve(outDir, "app.css"), "utf8");

/** Assert a literal appears in a component, so the gate tracks the real source. */
async function assertInSource(relPath, needle) {
  const src = await readFile(resolve(root, relPath), "utf8");
  if (!src.includes(needle)) {
    throw new Error(`expected ${JSON.stringify(needle)} in ${relPath}`);
  }
  return needle;
}

const FAB_BASE =
  "absolute bottom-2 right-2 z-50 flex size-10 cursor-pointer items-center justify-center rounded-full ring-1 ring-foreground/10 bg-card text-muted-foreground shadow-sm transition-transform hover:scale-110 hover:text-accent-foreground active:scale-95";

await assertInSource("packages/views/chat/components/chat-fab.tsx", FAB_BASE);
const fabMotion = await assertInSource(
  "packages/views/chat/components/chat-fab.tsx",
  "animate-chat-impulse",
);
const fabReduced = await assertInSource(
  "packages/views/chat/components/chat-fab.tsx",
  "text-brand ring-brand/40",
);
await assertInSource(
  "packages/views/chat/components/dm-agent-bubble.tsx",
  "text-brand ring-brand/40",
);
const navMotion = await assertInSource(
  "packages/views/layout/navigation-progress.tsx",
  "h-full w-1/3 animate-nav-progress-sweep bg-brand",
);
const navReduced = await assertInSource(
  "packages/views/layout/navigation-progress.tsx",
  "h-full w-full bg-brand",
);

// Both branches are rendered in both media states. Under `reduce` only the
// reduced branch is what the component actually emits; the motion branch is
// rendered alongside purely to show WHY the JS gate is needed — Tailwind's
// `motion-reduce:` variant cannot beat a base.css class, so the motion branch
// keeps animating even under `reduce`.
const specimens = [
  { id: "fab-idle", cls: FAB_BASE, label: "Chat FAB · idle" },
  { id: "fab-motion", cls: cn(FAB_BASE, fabMotion), label: "Chat FAB · running (motion branch)" },
  { id: "fab-reduced", cls: cn(FAB_BASE, fabReduced), label: "Chat FAB · running (reduced branch)" },
  { id: "nav-motion", cls: navMotion, label: "Nav bar (motion branch)" },
  { id: "nav-reduced", cls: navReduced, label: "Nav bar (reduced branch)" },
];

const html = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><style>${css}</style>
<style>
  body { margin: 0; padding: 24px; background: var(--background); color: var(--foreground);
         font: 13px/1.5 system-ui, sans-serif; }
  .row { display: flex; align-items: center; gap: 16px; margin-bottom: 16px; }
  .stage { position: relative; width: 220px; height: 52px; border: 1px dashed var(--border);
           border-radius: 8px; }
  .stage > [data-specimen] { position: absolute; }
  .cap { color: var(--muted-foreground); }
</style></head>
<body>
${specimens
  .map(
    (s) => `<div class="row"><div class="stage">
  <div data-specimen="${s.id}" class="${s.cls}">${
    s.id.startsWith("nav") ? "" : "<span>&#9679;</span>"
  }</div>
</div><span class="cap">${s.label}</span></div>`,
  )
  .join("\n")}
</body></html>`;

await writeFile(resolve(outDir, "specimens.html"), html, "utf8");

const browser = await chromium.launch();
const results = {};

for (const theme of ["light", "dark"]) {
  for (const motion of ["no-preference", "reduce"]) {
    const page = await browser.newPage({
      viewport: { width: 520, height: 360 },
      reducedMotion: motion === "reduce" ? "reduce" : "no-preference",
      colorScheme: theme,
    });
    await page.setContent(html, { waitUntil: "load" });
    if (theme === "dark") {
      await page.evaluate(() => document.documentElement.classList.add("dark"));
    }
    await page.waitForTimeout(120);

    results[`${theme}/${motion}`] = await page.evaluate(() =>
      Object.fromEntries(
        [...document.querySelectorAll("[data-specimen]")].map((el) => {
          const cs = getComputedStyle(el);
          return [
            el.dataset.specimen,
            { animationName: cs.animationName, width: cs.width, color: cs.color },
          ];
        }),
      ),
    );

    await page.screenshot({ path: resolve(outDir, `${theme}-${motion}.png`), fullPage: true });
    await page.close();
  }
}

await browser.close();
await writeFile(resolve(outDir, "measurements.json"), JSON.stringify(results, null, 2), "utf8");

// ---- assertions -----------------------------------------------------------
const fail = [];
for (const theme of ["light", "dark"]) {
  const on = results[`${theme}/no-preference`];
  const off = results[`${theme}/reduce`];

  // 1. The animations really do run when motion is allowed (BEFORE state).
  if (on["fab-motion"].animationName !== "chat-impulse") {
    fail.push(`${theme}: fab motion branch not animating (${on["fab-motion"].animationName})`);
  }
  if (on["nav-motion"].animationName !== "nav-progress-sweep") {
    fail.push(`${theme}: nav motion branch not animating (${on["nav-motion"].animationName})`);
  }

  // 2. Root-cause evidence: the base.css class keeps animating even under
  //    `reduce`, which is exactly why the fix cannot be a `motion-reduce:`
  //    utility. If this ever stops holding, base.css grew its own reset and
  //    this whole slice should be reconsidered.
  if (off["fab-motion"].animationName !== "chat-impulse") {
    fail.push(
      `${theme}: base.css .animate-chat-impulse now resets under reduce — revisit the JS gate`,
    );
  }

  // 3. What the components actually emit under `reduce` is motionless.
  for (const id of ["fab-reduced", "nav-reduced"]) {
    if (off[id].animationName !== "none") {
      fail.push(`${theme} ${id}: still animating under reduce (${off[id].animationName})`);
    }
  }

  // 4. Running state stays distinguishable from idle without motion.
  if (off["fab-reduced"].color === off["fab-idle"].color) {
    fail.push(`${theme}: reduced-motion running colour equals idle — state lost`);
  }

  // 5. Indeterminate bar goes full width, not a frozen 33%. Measured against
  //    each other rather than an absolute px so the harness stage size is not
  //    baked into the assertion.
  const reducedWidth = parseFloat(off["nav-reduced"].width);
  const motionWidth = parseFloat(on["nav-motion"].width);
  if (Math.abs(reducedWidth - motionWidth * 3) > 2) {
    fail.push(
      `${theme}: reduced nav bar ${reducedWidth}px is not ~3x the ${motionWidth}px sweep (expected full width)`,
    );
  }
}

console.log(JSON.stringify(results, null, 2));
if (fail.length) {
  console.error("\nFAIL:\n" + fail.map((f) => ` - ${f}`).join("\n"));
  process.exit(1);
}
console.log("\nPASS — reduced-motion assertions hold in light and dark.");
