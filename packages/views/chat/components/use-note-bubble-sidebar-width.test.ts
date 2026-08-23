/**
 * @vitest-environment happy-dom
 */
import { createChatStore, registerChatStore, useChatStore } from "@multica/core/chat";
import { act, renderHook } from "@testing-library/react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { beforeEach, describe, expect, it } from "vitest";
import {
  CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH,
  CHAT_WINDOW_SIDEBAR_MAX_WIDTH,
  CHAT_WINDOW_SIDEBAR_MIN_WIDTH,
  CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY,
} from "./chat-window-layout";
import { useNoteBubbleSidebarWidth } from "./use-note-bubble-sidebar-width";

function makeStorage(initial: Record<string, string> = {}) {
  const data = { ...initial };
  return {
    getItem: (k: string) => data[k] ?? null,
    setItem: (k: string, v: string) => {
      data[k] = v;
    },
    removeItem: (k: string) => {
      delete data[k];
    },
  };
}

function dragHandle(
  onResizePointerDown: (event: ReactPointerEvent<HTMLElement>) => void,
  fromX: number,
  toX: number,
) {
  const target = document.createElement("button");
  document.body.appendChild(target);
  act(() => {
    onResizePointerDown({
      button: 0,
      preventDefault: () => undefined,
      clientX: fromX,
      currentTarget: target,
      pointerId: 1,
    } as unknown as ReactPointerEvent<HTMLElement>);
  });
  act(() => {
    target.dispatchEvent(new PointerEvent("pointermove", { clientX: toX }));
    target.dispatchEvent(new PointerEvent("pointerup", { clientX: toX }));
  });
  document.body.removeChild(target);
}

describe("useNoteBubbleSidebarWidth", () => {
  beforeEach(() => {
    registerChatStore(createChatStore({ storage: makeStorage() }));
  });

  it("starts at the default rail width and persists a left-edge drag", () => {
    const { result } = renderHook(() => useNoteBubbleSidebarWidth());
    expect(result.current.width).toBe(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);

    dragHandle(result.current.onResizePointerDown, 400, 280);

    expect(result.current.width).toBe(504);
    expect(useChatStore.getState().noteBubbleSidebarWidth).toBe(504);
  });

  it("keeps a second subscriber in lockstep when the rail narrows", () => {
    const rail = renderHook(() => useNoteBubbleSidebarWidth());
    const dock = renderHook(() => useNoteBubbleSidebarWidth());

    dragHandle(rail.result.current.onResizePointerDown, 400, 200);
    expect(rail.result.current.width).toBe(584);
    expect(dock.result.current.width).toBe(584);

    dragHandle(rail.result.current.onResizePointerDown, 200, 400);
    expect(rail.result.current.width).toBe(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);
    expect(dock.result.current.width).toBe(CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH);
  });

  it("reads a stored width and clamps it", () => {
    registerChatStore(
      createChatStore({
        storage: makeStorage({ [CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY]: "80" }),
      }),
    );
    const { result } = renderHook(() => useNoteBubbleSidebarWidth());
    expect(result.current.width).toBe(CHAT_WINDOW_SIDEBAR_MIN_WIDTH);

    registerChatStore(
      createChatStore({
        storage: makeStorage({ [CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY]: "900" }),
      }),
    );
    const second = renderHook(() => useNoteBubbleSidebarWidth());
    expect(second.result.current.width).toBe(CHAT_WINDOW_SIDEBAR_MAX_WIDTH);
  });
});
