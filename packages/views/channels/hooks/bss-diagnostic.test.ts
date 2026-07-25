// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { bssRecord, bssTraceEnabled } from "./bss-diagnostic";

type W = { __bssTraceEnabled?: boolean; __bssTrace?: Array<Record<string, unknown>> };
const w = window as unknown as W;

afterEach(() => {
  delete w.__bssTraceEnabled;
  delete w.__bssTrace;
});

describe("bss-diagnostic (opt-in trace recorder)", () => {
  it("is a zero-cost no-op when the opt-in flag is unset (default path)", () => {
    expect(bssTraceEnabled()).toBe(false);
    bssRecord("effect", { msgLen: 3 });
    expect(w.__bssTrace).toBeUndefined(); // no array is even allocated
  });

  it("records entries only once opted in, tagging kind + fields", () => {
    w.__bssTraceEnabled = true;
    expect(bssTraceEnabled()).toBe(true);
    bssRecord("effect", { msgLen: 0 });
    bssRecord("write", { frame: 1, stAfter: 263 });
    expect(w.__bssTrace).toHaveLength(2);
    expect(w.__bssTrace?.[0]).toMatchObject({ kind: "effect", msgLen: 0 });
    expect(w.__bssTrace?.[1]).toMatchObject({ kind: "write", frame: 1, stAfter: 263 });
    expect(typeof w.__bssTrace?.[0]?.t).toBe("number"); // performance.now() stamp
  });

  it("bounds the first-visit trace so it stays a fixed one-shot sample", () => {
    w.__bssTraceEnabled = true;
    for (let i = 0; i < 500; i++) bssRecord("tick", { frame: i });
    expect(w.__bssTrace).toHaveLength(200); // MAX_ENTRIES cap
    // The cap keeps the earliest frames (the mount / data-arrival window we care
    // about), not the tail.
    expect(w.__bssTrace?.[0]).toMatchObject({ frame: 0 });
  });
});
