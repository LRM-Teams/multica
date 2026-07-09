// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { useEntryAroundSeq } from "./use-entry-around-seq";

describe("useEntryAroundSeq (#340)", () => {
  it("freezes the entry read cursor as the around anchor", () => {
    const { result, rerender } = renderHook(
      ({ id, seq }: { id: string; seq: number | null }) =>
        useEntryAroundSeq(id, seq),
      { initialProps: { id: "c1", seq: 42 } },
    );
    expect(result.current).toBe(42);

    // The list cursor advancing after entry (mark-read echo) must NOT move the
    // anchor — it stays frozen for the visit.
    rerender({ id: "c1", seq: 99 });
    expect(result.current).toBe(42);
  });

  it("re-freezes when switching conversations", () => {
    const { result, rerender } = renderHook(
      ({ id, seq }: { id: string; seq: number | null }) =>
        useEntryAroundSeq(id, seq),
      { initialProps: { id: "c1", seq: 42 } },
    );
    expect(result.current).toBe(42);
    rerender({ id: "c2", seq: 7 });
    expect(result.current).toBe(7);
  });

  it("returns null when there is nothing unread (cursor <= 0 or absent)", () => {
    const zero = renderHook(() => useEntryAroundSeq("c1", 0));
    expect(zero.result.current).toBeNull();
    const absent = renderHook(() => useEntryAroundSeq("c2", null));
    expect(absent.result.current).toBeNull();
    const undef = renderHook(() => useEntryAroundSeq("c3", undefined));
    expect(undef.result.current).toBeNull();
  });
});
