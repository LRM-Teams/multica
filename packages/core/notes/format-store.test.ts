// @vitest-environment jsdom
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_NOTE_FORMAT } from "./format";
import { NOTE_FORMAT_STORAGE_KEY, useNoteFormatStore } from "./format-store";

const flush = () => new Promise((resolve) => queueMicrotask(() => resolve(null)));

beforeAll(() => {
  if (typeof globalThis.localStorage?.clear !== "function") {
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (k) => values.get(k) ?? null,
      key: (i) => Array.from(values.keys())[i] ?? null,
      removeItem: (k) => { values.delete(k); },
      setItem: (k, v) => { values.set(k, v); },
    };
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  }
});

beforeEach(() => {
  localStorage.clear();
  useNoteFormatStore.setState({ ...DEFAULT_NOTE_FORMAT });
});

afterEach(() => {
  useNoteFormatStore.setState({ ...DEFAULT_NOTE_FORMAT });
});

describe("useNoteFormatStore", () => {
  it("starts at the built-in defaults", () => {
    expect(useNoteFormatStore.getState()).toMatchObject(DEFAULT_NOTE_FORMAT);
  });

  it("persists only typography fields under a global key", async () => {
    useNoteFormatStore.getState().setFontFamily("serif");
    useNoteFormatStore.getState().setFontSize("18");
    useNoteFormatStore.getState().setColor("blue");
    await flush();

    const raw = localStorage.getItem(NOTE_FORMAT_STORAGE_KEY);
    expect(raw).not.toBeNull();
    const parsed = JSON.parse(raw as string);
    expect(parsed.state).toEqual({
      fontFamily: "serif",
      fontSize: "18",
      color: "blue",
    });
  });

  it("resetFormat restores the built-in defaults", () => {
    useNoteFormatStore.getState().setFormat({ fontFamily: "mono", fontSize: "24", color: "red" });
    useNoteFormatStore.getState().resetFormat();
    expect(useNoteFormatStore.getState()).toMatchObject(DEFAULT_NOTE_FORMAT);
  });
});
