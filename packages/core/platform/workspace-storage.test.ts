// @vitest-environment jsdom
// jsdom gives us a real localStorage so the #210 last-workspace persistence
// tests exercise the actual defaultStorage path.
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import {
  createWorkspaceAwareStorage,
  setCurrentWorkspace,
  getPersistedLastWorkspaceSlug,
  registerForWorkspaceRehydration,
} from "./workspace-storage";
import type { StorageAdapter } from "../types/storage";

function mockAdapter(): StorageAdapter {
  const store = new Map<string, string>();
  return {
    getItem: vi.fn((k) => store.get(k) ?? null),
    setItem: vi.fn((k, v) => store.set(k, v)),
    removeItem: vi.fn((k) => store.delete(k)),
  };
}

afterEach(() => {
  setCurrentWorkspace(null, null);
});

describe("workspace-aware storage", () => {
  it("drops writes and returns null for reads when no workspace is set", () => {
    const adapter = mockAdapter();
    setCurrentWorkspace(null, null);
    const storage = createWorkspaceAwareStorage(adapter);

    storage.setItem("draft", "data");
    expect(adapter.setItem).not.toHaveBeenCalled();

    expect(storage.getItem("draft")).toBeNull();
    expect(adapter.getItem).not.toHaveBeenCalled();

    storage.removeItem("draft");
    expect(adapter.removeItem).not.toHaveBeenCalled();
  });

  it("namespaces key with slug when workspace is set", () => {
    const adapter = mockAdapter();
    setCurrentWorkspace("acme", "ws_abc");
    const storage = createWorkspaceAwareStorage(adapter);

    storage.setItem("draft", "data");
    expect(adapter.setItem).toHaveBeenCalledWith("draft:acme", "data");

    storage.getItem("draft");
    expect(adapter.getItem).toHaveBeenCalledWith("draft:acme");
  });

  it("follows workspace changes dynamically", () => {
    const adapter = mockAdapter();
    const storage = createWorkspaceAwareStorage(adapter);

    setCurrentWorkspace("team-a", "ws_1");
    storage.setItem("draft", "v1");
    expect(adapter.setItem).toHaveBeenCalledWith("draft:team-a", "v1");

    setCurrentWorkspace("team-b", "ws_2");
    storage.setItem("draft", "v2");
    expect(adapter.setItem).toHaveBeenCalledWith("draft:team-b", "v2");
  });

  it("removeItem uses current workspace slug", () => {
    const adapter = mockAdapter();
    setCurrentWorkspace("dev", "ws_x");
    const storage = createWorkspaceAwareStorage(adapter);

    storage.removeItem("draft");
    expect(adapter.removeItem).toHaveBeenCalledWith("draft:dev");
  });
});

describe("setCurrentWorkspace — rehydrate side effect", () => {
  const flush = () => new Promise((resolve) => queueMicrotask(() => resolve(null)));

  it("runs registered fns once when slug changes", async () => {
    const fn = vi.fn();
    registerForWorkspaceRehydration(fn);

    setCurrentWorkspace("team-a", "ws_a");
    await flush();

    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("is a no-op when slug is unchanged — repeat calls with same slug skip the side effect", async () => {
    const fn = vi.fn();
    registerForWorkspaceRehydration(fn);

    setCurrentWorkspace("team-a", "ws_a");
    await flush();
    setCurrentWorkspace("team-a", "ws_a");
    setCurrentWorkspace("team-a", "ws_a");
    setCurrentWorkspace("team-a", "ws_a");
    await flush();

    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("runs again on real workspace switch", async () => {
    const fn = vi.fn();
    registerForWorkspaceRehydration(fn);

    setCurrentWorkspace("team-a", "ws_a");
    await flush();
    setCurrentWorkspace("team-b", "ws_b");
    await flush();

    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("runs again after logout → re-entry into same workspace", async () => {
    const fn = vi.fn();
    registerForWorkspaceRehydration(fn);

    setCurrentWorkspace("team-a", "ws_a");
    await flush();
    setCurrentWorkspace(null, null);
    await flush();
    setCurrentWorkspace("team-a", "ws_a");
    await flush();

    expect(fn).toHaveBeenCalledTimes(3);
  });
});

describe("setCurrentWorkspace — last-workspace persistence (#210)", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    localStorage.clear();
    setCurrentWorkspace(null, null);
  });

  it("returns null when nothing has been recorded", () => {
    expect(getPersistedLastWorkspaceSlug()).toBeNull();
  });

  it("persists the active slug and reads it back across a reset of the in-memory slug", () => {
    setCurrentWorkspace("acme", "ws_a");
    expect(getPersistedLastWorkspaceSlug()).toBe("acme");
  });

  it("updates the persisted slug on a real workspace switch", () => {
    setCurrentWorkspace("acme", "ws_a");
    setCurrentWorkspace("beta", "ws_b");
    expect(getPersistedLastWorkspaceSlug()).toBe("beta");
  });

  it("does NOT wipe the persisted slug on logout (null) — survives for the next login", () => {
    setCurrentWorkspace("acme", "ws_a");
    setCurrentWorkspace(null, null);
    expect(getPersistedLastWorkspaceSlug()).toBe("acme");
  });
});
