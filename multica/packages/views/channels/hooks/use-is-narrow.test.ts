// @vitest-environment jsdom

import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useIsNarrow } from "./use-is-narrow";

const originalWidth = window.innerWidth;

function setWidth(px: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: px, writable: true });
}

afterEach(() => setWidth(originalWidth));

describe("useIsNarrow", () => {
  // The whole point of the board-local hook (vs shared `useIsMobile`'s < 768):
  // 768 itself must count as narrow, because the board is cramped at exactly
  // that width. renderHook flushes the mount effect, so `result.current`
  // reflects the measured viewport.
  it("is true at exactly 768px (board switches to segmented at ≤768)", () => {
    setWidth(768);
    const { result } = renderHook(() => useIsNarrow());
    expect(result.current).toBe(true);
  });

  it("is false at 769px (>768 keeps the horizontal columns)", () => {
    setWidth(769);
    const { result } = renderHook(() => useIsNarrow());
    expect(result.current).toBe(false);
  });
});
