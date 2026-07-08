// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { useHighlightScroll } from "./use-highlight-scroll";

function handleWithSpy() {
  const scrollToIndex = vi.fn();
  const ref = { current: { scrollToIndex } as unknown as VirtuosoHandle };
  return { scrollToIndex, ref };
}

// The hook only reads `.id`; keep fixtures minimal.
function messages(ids: string[]): ChannelMessage[] {
  return ids.map((id) => ({ id }) as unknown as ChannelMessage);
}

describe("useHighlightScroll", () => {
  it("scrolls to and centers the highlighted message (Virtuoso index + DOM row)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const scrollIntoView = vi.fn();
    const messageRefMap = new Map<string, HTMLDivElement>([
      ["m3", { scrollIntoView } as unknown as HTMLDivElement],
    ]);
    const { result } = renderHook(() =>
      useHighlightScroll({
        messages: messages(["m1", "m2", "m3", "m4"]),
        highlightMessageId: "m3",
        firstItemIndex: 100,
        virtuosoRef: ref,
        messageRefMap,
      }),
    );
    expect(result.current.highlightIndex).toBe(2);
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 102, align: "center", behavior: "smooth" });
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center", behavior: "smooth" });
  });

  it("reports -1 and never scrolls when nothing is highlighted", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useHighlightScroll({
        messages: messages(["m1", "m2"]),
        highlightMessageId: null,
        firstItemIndex: 0,
        virtuosoRef: ref,
        messageRefMap: new Map(),
      }),
    );
    expect(result.current.highlightIndex).toBe(-1);
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("reports -1 and never scrolls when the highlighted id is not in the list", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useHighlightScroll({
        messages: messages(["m1", "m2"]),
        highlightMessageId: "missing",
        firstItemIndex: 0,
        virtuosoRef: ref,
        messageRefMap: new Map(),
      }),
    );
    expect(result.current.highlightIndex).toBe(-1);
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("tolerates a highlighted row that has no mapped DOM node (Virtuoso scroll only)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    renderHook(() =>
      useHighlightScroll({
        messages: messages(["m1", "m2"]),
        highlightMessageId: "m2",
        firstItemIndex: 0,
        virtuosoRef: ref,
        messageRefMap: new Map(),
      }),
    );
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 1, align: "center", behavior: "smooth" });
  });
});
