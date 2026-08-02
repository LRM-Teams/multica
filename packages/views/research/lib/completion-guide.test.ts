import { afterEach, describe, expect, it, vi } from "vitest";
import {
  completionGuideStorageKey,
  dismissCompletionGuide,
  isCompletionGuideDismissed,
  resolveCompletionGuideKind,
} from "./completion-guide";

describe("completion-guide (LRM-832)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    try {
      window.localStorage.clear();
    } catch {
      /* ignore */
    }
  });

  it("maps terminal statuses to done / failed / null", () => {
    expect(resolveCompletionGuideKind("completed")).toBe("done");
    expect(resolveCompletionGuideKind("archived")).toBe("done");
    expect(resolveCompletionGuideKind("failed")).toBe("failed");
    expect(resolveCompletionGuideKind("error")).toBe("failed");
    expect(resolveCompletionGuideKind("cancelled")).toBe("failed");
    expect(resolveCompletionGuideKind("running")).toBeNull();
    expect(resolveCompletionGuideKind(null)).toBeNull();
  });

  it("persists dismiss so the card does not reappear", () => {
    const id = "sess-1";
    expect(isCompletionGuideDismissed(id)).toBe(false);
    dismissCompletionGuide(id);
    expect(isCompletionGuideDismissed(id)).toBe(true);
    expect(window.localStorage.getItem(completionGuideStorageKey(id))).toBe("1");
  });
});
