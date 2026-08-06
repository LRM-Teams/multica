// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { useEntryAnchor } from "./use-entry-around-seq";

describe("useEntryAnchor (LRM-1068)", () => {
  it("freezes unread count but never anchors cold load on last_read_seq", () => {
    const { result, rerender } = renderHook(
      ({ id, seq, count }: { id: string; seq: number | null; count: number | null }) =>
        useEntryAnchor(id, seq, count),
      { initialProps: { id: "c1", seq: 42, count: 486 } },
    );
    // Latest-page open: aroundSeq stays null even when the list has a cursor.
    expect(result.current).toEqual({ aroundSeq: null, unreadCount: 486 });

    // Mark-read echo / new messages must NOT shrink the frozen entry count.
    rerender({ id: "c1", seq: 99, count: 3 });
    expect(result.current).toEqual({ aroundSeq: null, unreadCount: 486 });
  });

  it("re-freezes unread count when switching conversations", () => {
    const { result, rerender } = renderHook(
      ({ id, seq, count }: { id: string; seq: number | null; count: number | null }) =>
        useEntryAnchor(id, seq, count),
      { initialProps: { id: "c1", seq: 42, count: 5 } },
    );
    expect(result.current).toEqual({ aroundSeq: null, unreadCount: 5 });
    rerender({ id: "c2", seq: 7, count: 2 });
    expect(result.current).toEqual({ aroundSeq: null, unreadCount: 2 });
  });

  it("returns nulls when there is nothing unread (count <= 0 or absent)", () => {
    expect(renderHook(() => useEntryAnchor("c1", 0, 0)).result.current).toEqual({
      aroundSeq: null,
      unreadCount: null,
    });
    expect(
      renderHook(() => useEntryAnchor("c2", null, undefined)).result.current,
    ).toEqual({ aroundSeq: null, unreadCount: null });
  });

  it("carries a real count even when the read cursor is absent", () => {
    expect(renderHook(() => useEntryAnchor("c1", 10, null)).result.current).toEqual({
      aroundSeq: null,
      unreadCount: null,
    });
    expect(renderHook(() => useEntryAnchor("c2", null, 4)).result.current).toEqual({
      aroundSeq: null,
      unreadCount: 4,
    });
  });
});
