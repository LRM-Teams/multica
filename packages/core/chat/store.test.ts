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

describe("createChatStore note selection quote", () => {
  it("opens the rail and stores the excerpt without persisting it", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    store.getState().askAboutNoteSelection("page-1", "  selected excerpt  ");

    const quote = store.getState().noteSelectionQuote;
    expect(store.getState().noteBubbleOpenPageId).toBe("page-1");
    expect(quote?.pageId).toBe("page-1");
    expect(quote?.excerpts.map((excerpt) => excerpt.text)).toEqual(["selected excerpt"]);
    expect(quote?.askedAt).toBeGreaterThan(0);
    expect(storage.snapshot()["multica:chat:noteBubbleOpenPageId"]).toBeUndefined();
    expect(Object.keys(storage.snapshot()).some((key) => key.includes("noteSelection"))).toBe(false);
  });

  it("clears the quote when the rail closes", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    store.getState().askAboutNoteSelection("page-1", "excerpt");
    store.getState().setNoteBubbleOpenPageId(null);
    expect(store.getState().noteSelectionQuote).toBeNull();
  });

  it("ignores a blank excerpt so an empty selection cannot open a quote", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    store.getState().askAboutNoteSelection("page-1", " \n ");
    expect(store.getState().noteSelectionQuote).toBeNull();
    expect(store.getState().noteBubbleOpenPageId).toBeNull();
  });

  it("appends a second selection instead of replacing the first", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    store.getState().askAboutNoteSelection("page-1", "第一段");
    store.getState().askAboutNoteSelection("page-1", "第二段");
    expect(store.getState().noteSelectionQuote?.excerpts.map((excerpt) => excerpt.text)).toEqual([
      "第一段",
      "第二段",
    ]);
  });

  it("removes one excerpt without dropping the rest", () => {
    const storage = makeStorage();
    const store = createChatStore({ storage });
    store.getState().askAboutNoteSelection("page-1", "第一段");
    store.getState().askAboutNoteSelection("page-1", "第二段");
    const firstId = store.getState().noteSelectionQuote?.excerpts[0]?.id;
    expect(firstId).toBeTruthy();
    store.getState().removeNoteSelectionExcerpt(firstId!);
    expect(store.getState().noteSelectionQuote?.excerpts.map((excerpt) => excerpt.text)).toEqual([
      "第二段",
    ]);
  });
});
