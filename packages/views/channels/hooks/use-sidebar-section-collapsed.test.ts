// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  resetSidebarSectionCollapsedMemoryForTests,
  useSidebarSectionCollapsed,
} from "./use-sidebar-section-collapsed";

describe("useSidebarSectionCollapsed (LRM-655)", () => {
  beforeEach(() => {
    resetSidebarSectionCollapsedMemoryForTests();
    window.sessionStorage.clear();
  });

  afterEach(() => {
    resetSidebarSectionCollapsedMemoryForTests();
    window.sessionStorage.clear();
  });

  it("defaults to expanded", () => {
    const { result } = renderHook(() =>
      useSidebarSectionCollapsed("dms", "ws-1"),
    );
    expect(result.current[0]).toBe(false);
  });

  it("survives remount via in-memory cache (channel select remount path)", () => {
    const first = renderHook(() => useSidebarSectionCollapsed("dms", "ws-1"));
    act(() => {
      first.result.current[1](true);
    });
    expect(first.result.current[0]).toBe(true);
    first.unmount();

    const second = renderHook(() => useSidebarSectionCollapsed("dms", "ws-1"));
    expect(second.result.current[0]).toBe(true);
  });

  it("mirrors collapse into sessionStorage for soft refresh", () => {
    const { result } = renderHook(() =>
      useSidebarSectionCollapsed("dms", "ws-1"),
    );
    act(() => {
      result.current[1]((c) => !c);
    });
    expect(
      window.sessionStorage.getItem(
        "multica:messages-sidebar:ws-1:dms:collapsed",
      ),
    ).toBe("1");
  });

  it("keeps pinned / dms / channels independent", () => {
    const dms = renderHook(() => useSidebarSectionCollapsed("dms", "ws-1"));
    const channels = renderHook(() =>
      useSidebarSectionCollapsed("channels", "ws-1"),
    );
    act(() => {
      dms.result.current[1](true);
    });
    expect(dms.result.current[0]).toBe(true);
    expect(channels.result.current[0]).toBe(false);
  });
});
