// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { useEntryAnchor } from "./use-entry-around-seq";

describe("useEntryAnchor (#340)", () => {
  it("freezes the entry read cursor and unread count as the anchor", () => {
    const { result, rerender } = renderHook(
      ({ id, seq, count }: { id: string; seq: number | null; count: number | null }) =>
        useEntryAnchor(id, seq, count),
      { initialProps: { id: "c1", seq: 42, count: 486 } },
    );
    expect(result.current).toEqual({ aroundSeq: 42, unreadCount: 486 });

    // The list advancing after entry (mark-read echo / new messages) must NOT
    // move the anchor or the divider count — both stay frozen for the visit.
    rerender({ id: "c1", seq: 99, count: 3 });
    expect(result.current).toEqual({ aroundSeq: 42, unreadCount: 486 });
  });

  it("re-freezes when switching conversations", () => {
    const { result, rerender } = renderHook(
      ({ id, seq, count }: { id: string; seq: number | null; count: number | null }) =>
        useEntryAnchor(id, seq, count),
      { initialProps: { id: "c1", seq: 42, count: 5 } },
    );
    expect(result.current).toEqual({ aroundSeq: 42, unreadCount: 5 });
    rerender({ id: "c2", seq: 7, count: 2 });
    expect(result.current).toEqual({ aroundSeq: 7, unreadCount: 2 });
  });

  it("returns nulls when there is nothing unread (cursor / count <= 0 or absent)", () => {
    expect(renderHook(() => useEntryAnchor("c1", 0, 0)).result.current).toEqual({
      aroundSeq: null,
      unreadCount: null,
    });
    expect(
      renderHook(() => useEntryAnchor("c2", null, undefined)).result.current,
    ).toEqual({ aroundSeq: null, unreadCount: null });
  });

  it("carries a real count even when the read cursor is absent, and vice versa", () => {
    // Defensive: the two freeze independently.
    expect(renderHook(() => useEntryAnchor("c1", 10, null)).result.current).toEqual({
      aroundSeq: 10,
      unreadCount: null,
    });
    expect(renderHook(() => useEntryAnchor("c2", null, 4)).result.current).toEqual({
      aroundSeq: null,
      unreadCount: 4,
    });
  });
});
