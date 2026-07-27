import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { PointerEvent as ReactPointerEvent } from "react";
import {
  CHANNEL_DETAIL_SIDE_WIDTH_DEFAULT,
  CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY,
  PROFILE_PANEL_WIDTH_DEFAULT,
  PROFILE_PANEL_WIDTH_MAX,
  PROFILE_PANEL_WIDTH_MIN,
  PROFILE_PANEL_WIDTH_STORAGE_KEY,
  useProfilePanelWidth,
} from "./use-profile-panel-width";

describe("useProfilePanelWidth (LRM-481)", () => {
  beforeEach(() => {
    window.localStorage.removeItem(PROFILE_PANEL_WIDTH_STORAGE_KEY);
    window.localStorage.removeItem(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY);
  });

  it("defaults to 520 when nothing is stored", () => {
    const { result } = renderHook(() => useProfilePanelWidth());
    expect(result.current.width).toBe(PROFILE_PANEL_WIDTH_DEFAULT);
  });

  it("defaults channel/thread dock to 520 when nothing is stored", () => {
    const { result } = renderHook(() =>
      useProfilePanelWidth(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY),
    );
    expect(result.current.width).toBe(CHANNEL_DETAIL_SIDE_WIDTH_DEFAULT);
  });

  it("hydrates and clamps stored widths after mount", () => {
    window.localStorage.setItem(PROFILE_PANEL_WIDTH_STORAGE_KEY, "200");
    const narrow = renderHook(() => useProfilePanelWidth());
    expect(narrow.result.current.width).toBe(PROFILE_PANEL_WIDTH_MIN);

    window.localStorage.setItem(PROFILE_PANEL_WIDTH_STORAGE_KEY, "9999");
    const wide = renderHook(() => useProfilePanelWidth());
    expect(wide.result.current.width).toBe(PROFILE_PANEL_WIDTH_MAX);

    window.localStorage.setItem(PROFILE_PANEL_WIDTH_STORAGE_KEY, "500");
    const mid = renderHook(() => useProfilePanelWidth());
    expect(mid.result.current.width).toBe(500);
  });

  it("keeps channel dock width separate from the global overlay key (LRM-400)", () => {
    window.localStorage.setItem(PROFILE_PANEL_WIDTH_STORAGE_KEY, "500");
    window.localStorage.setItem(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY, "400");
    const profile = renderHook(() => useProfilePanelWidth());
    const channel = renderHook(() =>
      useProfilePanelWidth(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY),
    );
    expect(profile.result.current.width).toBe(500);
    expect(channel.result.current.width).toBe(400);
  });

  it("persists width after a left-edge drag", () => {
    const { result } = renderHook(() => useProfilePanelWidth());
    const el = document.createElement("button");
    document.body.appendChild(el);

    act(() => {
      result.current.onResizePointerDown({
        button: 0,
        clientX: 1000,
        pointerId: 1,
        preventDefault: () => {},
        currentTarget: el,
      } as unknown as ReactPointerEvent<HTMLElement>);
    });

    act(() => {
      el.dispatchEvent(new PointerEvent("pointermove", { clientX: 900, bubbles: true }));
      el.dispatchEvent(new PointerEvent("pointerup", { clientX: 900, bubbles: true }));
    });

    expect(result.current.width).toBe(620);
    expect(window.localStorage.getItem(PROFILE_PANEL_WIDTH_STORAGE_KEY)).toBe("620");
    el.remove();
  });
});
