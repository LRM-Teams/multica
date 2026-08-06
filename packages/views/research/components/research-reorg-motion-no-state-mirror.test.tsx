/**
 * LRM-1335 — regression: the reorg motion must not mirror props/DOM into React state.
 *
 * The first CI run of #2180 was red on the React Doctor gate
 * (`react-doctor/no-adjust-state-on-prop-change` ×2): the gutter node copied the
 * canvas `data-reorg` attribute into `useState`, and the narrow list copied the
 * "which ids are new" derivation into `useState` from an effect. Both mirrors are
 * removed — the gutter animates imperatively from the MutationObserver, and the
 * list relies on the keyed row mounting once. This test locks both contracts:
 * behaviour for the gutter, source shape for the pieces a render test cannot see.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));

function read(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}




describe("LRM-1335 gutter growth is DOM-driven, not state-mirrored", () => {
  beforeEach(() => {
    // jsdom exposes no SVG geometry; the growth math only needs a positive length.
    const svgPathProto = Object.getPrototypeOf(
      document.createElementNS("http://www.w3.org/2000/svg", "path"),
    ) as { getTotalLength?: () => number };
    svgPathProto.getTotalLength = () => 400;
  });

  afterEach(() => {
    document.body.replaceChildren();
  });



  it("gutter node keeps no React state and batches style writes via cssText", () => {
    const src = read("research-git-gutter-node.tsx");
    expect(src).not.toMatch(/useState/);
    expect(src).toMatch(/new MutationObserver/);
    expect(src).toMatch(/style\.cssText/);
    // Individual property writes are what tripped the batching rule.
    expect(src).not.toMatch(/style\.strokeDashoffset\s*=/);
    expect(src).not.toMatch(/style\.strokeDasharray\s*=/);
  });

  it("narrow list relies on the keyed row mount, not an added-id state set", () => {
    const src = read("research-git-list.tsx");
    expect(src).toMatch(/NODE_ENTER_CLASS/);
    expect(src).not.toMatch(/enterIds/);
    expect(src).not.toMatch(/knownIdsRef/);
  });
});
