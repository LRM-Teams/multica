/**
 * @vitest-environment happy-dom
 */
import { act, renderHook, waitFor } from "@testing-library/react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { afterEach, describe, expect, it } from "vitest";
import {
  CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH,
  CHAT_WINDOW_SIDEBAR_MAX_WIDTH,
  CHAT_WINDOW_SIDEBAR_MIN_WIDTH,
  CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY,
} from "./chat-window-layout";
import { useNoteBubbleSidebarWidth } from "./use-note-bubble-sidebar-width";

describe("useNoteBubbleSidebarWidth", () => {
  afterEach(() => {
    window.localStorage.removeItem(CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY);
  });

  it("starts at the default rail width and persists a left-edge drag", () => {
    const { result } = renderHook(() => useNoteBubbleSidebarWidth());
    expect(result.current.width).toBe(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);

    const target = document.createElement("button");
    document.body.appendChild(target);
    act(() => {
      result.current.onResizePointerDown({
        button: 0,
        preventDefault: () => undefined,
        clientX: 400,
        currentTarget: target,
        pointerId: 1,
      } as unknown as ReactPointerEvent<HTMLElement>);
    });
    act(() => {
      target.dispatchEvent(new PointerEvent("pointermove", { clientX: 280 }));
      target.dispatchEvent(new PointerEvent("pointerup", { clientX: 280 }));
    });

    expect(result.current.width).toBe(504);
    expect(window.localStorage.getItem(CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY)).toBe("504");
    document.body.removeChild(target);
  });

  it("reads a stored width and clamps it", async () => {
    window.localStorage.setItem(CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY, "80");
    const { result } = renderHook(() => useNoteBubbleSidebarWidth());
    await waitFor(() => expect(result.current.width).toBe(CHAT_WINDOW_SIDEBAR_MIN_WIDTH));

    window.localStorage.setItem(CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY, "900");
    const second = renderHook(() => useNoteBubbleSidebarWidth());
    await waitFor(() => expect(second.result.current.width).toBe(CHAT_WINDOW_SIDEBAR_MAX_WIDTH));
  });
});
