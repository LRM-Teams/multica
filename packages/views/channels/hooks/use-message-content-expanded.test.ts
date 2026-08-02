// @vitest-environment jsdom
import { describe, expect, it, afterEach } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  resetMessageContentExpandedMemoryForTests,
  useMessageContentExpanded,
} from "./use-message-content-expanded";

describe("useMessageContentExpanded (LRM-987)", () => {
  afterEach(() => {
    resetMessageContentExpandedMemoryForTests();
  });

  it("keeps expand across remount with the same identity", () => {
    const identity = ["m1", "body", "1", "0"].join("\u0000");
    const first = renderHook(() => useMessageContentExpanded("m1", identity));
    act(() => {
      first.result.current.expand();
    });
    expect(first.result.current.contentExpanded).toBe(true);
    first.unmount();

    const second = renderHook(() => useMessageContentExpanded("m1", identity));
    expect(second.result.current.contentExpanded).toBe(true);
  });

  it("invalidates expand when body fingerprint changes", () => {
    const first = renderHook(
      ({ id, identity }) => useMessageContentExpanded(id, identity),
      { initialProps: { id: "m1", identity: ["m1", "old", "1", "0"].join("\u0000") } },
    );
    act(() => {
      first.result.current.expand();
    });
    first.rerender({ id: "m1", identity: ["m1", "new", "1", "0"].join("\u0000") });
    expect(first.result.current.contentExpanded).toBe(false);
  });
});
