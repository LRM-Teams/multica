import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  composerQuotePayloadScope,
  useComposerQuote,
  type ComposerQuoteTarget,
} from "./use-composer-quote";

const target: ComposerQuoteTarget = {
  messageId: "message-1",
  selectedText: "selected excerpt",
  author: "Alice",
  summary: "selected excerpt",
};

describe("useComposerQuote", () => {
  it("keeps quote state scoped to its conversation", () => {
    const { result, rerender } = renderHook(
      ({ scope }) => useComposerQuote(scope),
      { initialProps: { scope: "channel-1" } },
    );

    act(() => result.current.select(target));
    expect(result.current.input).toEqual({
      messageId: "message-1",
      selectedText: "selected excerpt",
    });

    rerender({ scope: "channel-2" });
    expect(result.current.target).toBeNull();
    expect(result.current.input).toBeUndefined();
  });

  it("does not clear a newer quote when an older send commits", () => {
    const { result } = renderHook(() => useComposerQuote("channel-1"));
    act(() => result.current.select(target));
    act(() =>
      result.current.select({
        ...target,
        messageId: "message-2",
        selectedText: "new excerpt",
      }),
    );
    act(() => result.current.clear(target));

    expect(result.current.target?.messageId).toBe("message-2");
  });
});

describe("composerQuotePayloadScope", () => {
  it("changes idempotency identity when the source or excerpt changes", () => {
    expect(composerQuotePayloadScope("channel-1", target)).not.toBe(
      composerQuotePayloadScope("channel-1", {
        messageId: "message-1",
        selectedText: "other excerpt",
      }),
    );
  });
});
