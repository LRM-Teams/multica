import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
const css = readFileSync(new URL("./research-canvas.css", import.meta.url), "utf8");
test("semantic matrix is complete", () => {
  for (const name of ["phase-plan", "phase-search", "phase-verify", "phase-delivery", "state-queued", "state-running", "state-succeeded", "state-failed", "state-stale", "path-main", "path-detour", "path-conflict", "layer-canvas", "layer-edge", "layer-node", "layer-active", "layer-overlay", "motion-fast", "motion-normal", "motion-structural", "ease-research-standard", "ease-research-emphasized"]) assert.match(css, new RegExp(`--research-${name}:|--${name}:`));
});
test("status has non-color cues and motion degradation", () => {
  for (const pair of [["running", "▶"], ["succeeded", "✓"], ["failed", "×"], ["stale", "◷"]]) assert.match(css, new RegExp(`data-state=\\"${pair[0]}\\"[\\s\\S]*content: \\"${pair[1]}\\"`));
  assert.match(css, /prefers-reduced-motion: reduce/); assert.match(css, /forced-colors: active/);
});
test("only explicitly active edge loops", () => {
  const loops = [...css.matchAll(/animation:[^;]*infinite/g)]; assert.equal(loops.length, 1); assert.match(css, /edge\[data-active="true"\][^{]*\{[^}]*animation:/);
});
