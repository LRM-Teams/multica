// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import type { KeyboardEvent } from "react";
import type { ChannelMessageSearchResult } from "@multica/core/types";
import {
  handleConvSearchInputKeyDown,
  orderConvSearchResultsNewestFirst,
} from "./conv-search-navigation";

function hit(id: string): ChannelMessageSearchResult {
  return {
    message_id: id,
    channel_id: "chan-1",
    thread_root_message_id: null,
    in_thread: false,
    type: "user",
    author_id: "u1",
    author_name: "Ada",
    content: "hit",
    created_at: "2026-08-01T00:00:00Z",
  };
}

function keyEvent(
  key: string,
  init: { shiftKey?: boolean } = {},
): KeyboardEvent<HTMLInputElement> {
  return {
    key,
    shiftKey: init.shiftKey ?? false,
    preventDefault: vi.fn(),
  } as unknown as KeyboardEvent<HTMLInputElement>;
}

describe("orderConvSearchResultsNewestFirst", () => {
  it("reverses oldest-first API order so index 0 is the newest hit (LRM-753)", () => {
    expect(
      orderConvSearchResultsNewestFirst([hit("old"), hit("mid"), hit("new")]).map(
        (r) => r.message_id,
      ),
    ).toEqual(["new", "mid", "old"]);
  });

  it("leaves empty / single-hit lists unchanged", () => {
    expect(orderConvSearchResultsNewestFirst([])).toEqual([]);
    expect(orderConvSearchResultsNewestFirst([hit("only")])).toEqual([hit("only")]);
  });
});

describe("handleConvSearchInputKeyDown", () => {
  it("Esc closes; Enter next; Shift+Enter previous; no-op when empty", () => {
    const onClose = vi.fn();
    const onNext = vi.fn();
    const onPrev = vi.fn();

    const esc = keyEvent("Escape");
    handleConvSearchInputKeyDown(esc, { total: 3, onClose, onNext, onPrev });
    expect(esc.preventDefault).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);

    const enter = keyEvent("Enter");
    handleConvSearchInputKeyDown(enter, { total: 3, onClose, onNext, onPrev });
    expect(enter.preventDefault).toHaveBeenCalled();
    expect(onNext).toHaveBeenCalledTimes(1);

    const shiftEnter = keyEvent("Enter", { shiftKey: true });
    handleConvSearchInputKeyDown(shiftEnter, { total: 3, onClose, onNext, onPrev });
    expect(onPrev).toHaveBeenCalledTimes(1);

    onNext.mockClear();
    onPrev.mockClear();
    handleConvSearchInputKeyDown(keyEvent("Enter"), {
      total: 0,
      onClose,
      onNext,
      onPrev,
    });
    expect(onNext).not.toHaveBeenCalled();
    expect(onPrev).not.toHaveBeenCalled();
  });
});
