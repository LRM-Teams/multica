// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { computeNewArrivals, useNewMessagesPill } from "./use-new-arrivals-pill";

const authored = (...items: Array<[number, string]>) =>
  items.map(([seq, author_id]) => ({ id: `m${seq}`, seq, author_id }));

function handleWithSpy() {
  const scrollToIndex = vi.fn();
  const ref = { current: { scrollToIndex } as unknown as VirtuosoHandle };
  return { scrollToIndex, ref };
}

// The hook only reads id/seq/channel_id/author_id; keep fixtures minimal.
function withSeq(items: Array<[string, number, string | null]>): ChannelMessage[] {
  return items.map(
    ([id, seq, author_id]) =>
      ({ id, seq, author_id, channel_id: "c1" }) as unknown as ChannelMessage,
  );
}

describe("computeNewArrivals", () => {
  it("returns null when the seen-through boundary is unknown", () => {
    expect(computeNewArrivals(authored([1, "o"]), null, "u1")).toBeNull();
  });

  it("returns null when nothing arrived past what you've seen", () => {
    expect(computeNewArrivals(authored([1, "o"], [2, "o"]), 2, "u1")).toBeNull();
  });

  it("counts others' messages past the boundary and reports the first id", () => {
    expect(
      computeNewArrivals(authored([1, "o"], [2, "o"], [3, "o"], [4, "o"]), 2, "u1"),
    ).toEqual({ count: 2, firstMessageId: "m3" });
  });

  it("excludes the viewer's own arrivals", () => {
    // past 2: m3 (other), m4 (own), m5 (other) → count 2, first m3
    const messages = authored([1, "o"], [2, "o"], [3, "o"], [4, "u1"], [5, "o"]);
    expect(computeNewArrivals(messages, 2, "u1")).toEqual({
      count: 2,
      firstMessageId: "m3",
    });
  });

  it("ignores the viewer id when null (counts everyone's arrivals)", () => {
    expect(computeNewArrivals(authored([1, "a"], [2, "b"]), 0, null)).toEqual({
      count: 2,
      firstMessageId: "m1",
    });
  });
});

describe("useNewMessagesPill onPillClick (#1194 index-contract regression)", () => {
  it("scrolls to the pill target's LOCAL index, never offset by a large firstItemIndex", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const initial = withSeq([
      ["m1", 1, "self"],
      ["m2", 2, "self"],
    ]);
    const { result, rerender } = renderHook(
      ({ messages }) =>
        useNewMessagesPill({ messages, currentUserId: "self", virtuosoRef: ref }),
      { initialProps: { messages: initial } },
    );
    expect(result.current.pill).toBeNull();

    // Simulate a large-offset channel (base ~1,000,000 in production) receiving
    // a live arrival from someone else past the entry high-water mark. The
    // hook itself never sees firstItemIndex — it only ever deals in the local
    // `messages` array — so the offset must NOT leak into the scrollToIndex call.
    const withArrival = withSeq([
      ["m1", 1, "self"],
      ["m2", 2, "self"],
      ["m3", 3, "other"],
    ]);
    rerender({ messages: withArrival });
    expect(result.current.pill).toEqual({ count: 1, firstMessageId: "m3" });

    act(() => {
      result.current.onPillClick();
    });

    // m3 is at local index 2 in `withArrival` — never `firstItemIndex + 2`.
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "start", behavior: "smooth" });
  });

  it("dismisses the pill after a click (caught up to the latest seq)", () => {
    const { ref } = handleWithSpy();
    const initial = withSeq([["m1", 1, "self"]]);
    const { result, rerender } = renderHook(
      ({ messages }) =>
        useNewMessagesPill({ messages, currentUserId: "self", virtuosoRef: ref }),
      { initialProps: { messages: initial } },
    );
    const withArrival = withSeq([
      ["m1", 1, "self"],
      ["m2", 2, "other"],
    ]);
    rerender({ messages: withArrival });
    expect(result.current.pill).not.toBeNull();

    act(() => {
      result.current.onPillClick();
    });
    rerender({ messages: withArrival });
    expect(result.current.pill).toBeNull();
  });
});
