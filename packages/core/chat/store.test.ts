import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types";
import { createChatStore } from "./store";

function makeStorage(initial: Record<string, string> = {}): StorageAdapter & {
  snapshot: () => Record<string, string>;
} {
  const data = { ...initial };
  return {
    getItem: (k) => data[k] ?? null,
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
    snapshot: () => ({ ...data }),
  };
}

describe("createChatStore note bubble open state", () => {
  it("starts closed even when a leftover open page id is in storage", () => {
    const storage = makeStorage({
      "multica:chat:noteBubbleOpenPageId": "page-stale",
    });
    const store = createChatStore({ storage });
    expect(store.getState().noteBubbleOpenPageId).toBeNull();
  });

  it("does not persist open so a refresh cannot reopen the rail", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    store.getState().toggleNoteBubble("page-1");
    expect(store.getState().noteBubbleOpenPageId).toBe("page-1");
    expect(storage.snapshot()["multica:chat:noteBubbleOpenPageId"]).toBeUndefined();

    const reloaded = createChatStore({ storage });
    expect(reloaded.getState().noteBubbleOpenPageId).toBeNull();
  });

  it("persists rail width so the notes dock and overlay share one value", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    expect(store.getState().noteBubbleSidebarWidth).toBe(384);

    store.getState().setNoteBubbleSidebarWidth(520);
    expect(store.getState().noteBubbleSidebarWidth).toBe(520);
    expect(storage.snapshot()["multica:chat:noteBubbleSidebarWidth"]).toBe("520");

    const reloaded = createChatStore({ storage });
    expect(reloaded.getState().noteBubbleSidebarWidth).toBe(520);
  });
});
