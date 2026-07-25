// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import type { ChannelMessagesPage } from "@multica/core/types";
import { bssRecord, bssTraceEnabled, deriveDiagSourceTailComplete } from "./bss-diagnostic";

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

describe("deriveDiagSourceTailComplete (three-state source contract)", () => {
  // Never conflate "latest page not returned yet" with "tail complete" — that
  // first link in the causal chain must stay honest so the trace can classify
  // data-not-ready vs Virtuoso measurement-lag (Barry).
  it("is null while the latest page has not returned (data not ready)", () => {
    expect(deriveDiagSourceTailComplete(undefined)).toBeNull();
  });

  it("is false when the loaded around window has newer messages beyond it", () => {
    expect(
      deriveDiagSourceTailComplete({ has_more_after: true } as ChannelMessagesPage),
    ).toBe(false);
  });

  it("is true when the loaded window contains the real tail", () => {
    // around mode, no newer messages beyond the window
    expect(
      deriveDiagSourceTailComplete({ has_more_after: false } as ChannelMessagesPage),
    ).toBe(true);
    // default/before-cursor page — has_more_after absent, the page IS the tail
    expect(deriveDiagSourceTailComplete({} as ChannelMessagesPage)).toBe(true);
  });
});
