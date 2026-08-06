import { describe, expect, it } from "vitest";
import {
  CANVAS_URL_PARAMS,
  canvasUrlStateEquals,
  parseCanvasUrlState,
  serializeCanvasUrlState,
} from "./url-state";

describe("canvas URL state adapter — FE-10 AC (deep-link, no URL pollution)", () => {
  it("reads lens/node/view from a deep-link query string", () => {
    const state = parseCanvasUrlState(
      "?lens=execution&node=run-1:task:t1&view=trajectory",
    );
    expect(state).toEqual({
      lens: "execution",
      node: "run-1:task:t1",
      view: "trajectory",
    });
  });

  it("returns the empty state when no canvas param is present", () => {
    expect(parseCanvasUrlState("")).toEqual({});
    expect(parseCanvasUrlState("?workspace=abc&page=2")).toEqual({});
  });

  it("serializes ONLY lens/node/view, preserving unrelated params", () => {
    const out = serializeCanvasUrlState(
      { lens: "insight", node: "run-7:node:42" },
      "?workspace=abc&utm=x",
    );
    const params = new URLSearchParams(out);
    expect(params.get("lens")).toBe("insight");
    expect(params.get("node")).toBe("run-7:node:42");
    expect(params.get("workspace")).toBe("abc");
    expect(params.get("utm")).toBe("x");
    expect(params.has("view")).toBe(false);
  });

  it("NEVER writes viewport/fold/motion into the URL", () => {
    // Even if a caller (bug) passes non-deep-link fields, they are dropped.
    const state: Record<string, string> = {
      lens: "explore",
      node: "n1",
      view: "trajectory",
      x: "123.4",
      y: "-56.7",
      zoom: "0.8",
      folded: "a,b,c",
      motion: "run-1",
    };
    const out = serializeCanvasUrlState(state as never, "");
    const params = new URLSearchParams(out);
    for (const key of CANVAS_URL_PARAMS) {
      expect(params.has(key)).toBe(true);
    }
    expect(params.has("x")).toBe(false);
    expect(params.has("y")).toBe(false);
    expect(params.has("zoom")).toBe(false);
    expect(params.has("folded")).toBe(false);
    expect(params.has("motion")).toBe(false);
    expect(params.get("motion")).toBeNull();
  });

  it("drops cleared fields from the URL (empty -> no output)", () => {
    const out = serializeCanvasUrlState(
      {},
      "?lens=execution&node=n1&view=trajectory",
    );
    expect(out).toBe("");
  });

  it("equality ignores ordering and treats undefined/absent the same", () => {
    expect(
      canvasUrlStateEquals({ lens: "execution" }, "?lens=execution&node=n1"),
    ).toBe(false);
    expect(canvasUrlStateEquals({ lens: "execution" }, "?lens=execution")).toBe(
      true,
    );
    expect(canvasUrlStateEquals({}, "?lens=")).toBe(true);
  });

  it("exposes exactly the three deep-link param names", () => {
    expect([...CANVAS_URL_PARAMS].sort()).toEqual(["lens", "node", "view"]);
  });

  it("parsing a bare param name edge case is handled (empty value -> absent)", () => {
    expect(parseCanvasUrlState("?node=")).toEqual({});
  });
});
