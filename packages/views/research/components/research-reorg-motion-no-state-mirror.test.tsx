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
import { render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { NodeProps } from "@xyflow/react";
import {
  ResearchGitGutterNodeView,
  type ResearchGitGutterNode,
} from "./research-git-gutter-node";

const here = path.dirname(fileURLToPath(import.meta.url));

function read(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}

const GUTTER_PROPS = {
  id: "gutter",
  type: "gitGutter",
  selected: false,
  dragging: false,
  zIndex: 0,
  isConnectable: false,
  positionAbsoluteX: 0,
  positionAbsoluteY: 0,
  data: {
    gutterWidth: 72,
    gutterHeight: 400,
    gutterSegments: [
      { lane: 0, d: "M 8 0 L 8 400", color: "#4c8bf5" },
      { lane: 1, d: "M 24 0 L 24 400", color: "#f5a04c" },
    ],
  },
} as unknown as NodeProps<ResearchGitGutterNode>;

function renderInsideCanvasRoot(reorg: string) {
  const root = document.createElement("div");
  root.setAttribute("data-reorg", reorg);
  document.body.append(root);
  const view = render(<ResearchGitGutterNodeView {...GUTTER_PROPS} />, {
    container: root,
  });
  return { root, view };
}

function paths(root: HTMLElement) {
  return Array.from(root.querySelectorAll<SVGPathElement>("path"));
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

  it("applies dash growth when the canvas root flips data-reorg to running", async () => {
    const { root } = renderInsideCanvasRoot("");
    expect(paths(root).map((p) => p.style.strokeDasharray)).toEqual(["", ""]);

    root.setAttribute("data-reorg", "running");

    await waitFor(() => {
      for (const p of paths(root)) {
        expect(p.style.strokeDasharray).toBe("400px");
        expect(p.style.transition).not.toBe("none");
        expect(p.style.strokeDashoffset).toBe("0px");
      }
    });
  });

  it("clears the inline dash styles when reorg settles", async () => {
    const { root } = renderInsideCanvasRoot("running");
    await waitFor(() => {
      expect(paths(root)[0]!.style.strokeDasharray).toBe("400px");
    });

    root.setAttribute("data-reorg", "");

    await waitFor(() => {
      for (const p of paths(root)) {
        expect(p.style.strokeDasharray).toBe("");
        expect(p.style.strokeDashoffset).toBe("");
        expect(p.style.transition).toBe("");
      }
    });
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
