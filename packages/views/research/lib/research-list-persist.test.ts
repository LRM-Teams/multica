import { describe, expect, it, beforeEach, afterEach } from "vitest";
import {
  RESEARCH_LIST_FILTER_STORAGE_KEY,
  clearResearchListPersist,
  readResearchListPersist,
  writeResearchListPersist,
} from "./research-list-persist";

describe("research-list-persist (LRM-1115 D-IX)", () => {
  beforeEach(() => {
    sessionStorage.clear();
  });
  afterEach(() => {
    sessionStorage.clear();
  });

  it("round-trips q/status/scroll/sessionId", () => {
    writeResearchListPersist({
      q: "Alpha",
      status: "in_progress",
      scroll: 120,
      sessionId: "s-1",
    });
    expect(readResearchListPersist()).toEqual({
      q: "Alpha",
      status: "in_progress",
      scroll: 120,
      sessionId: "s-1",
    });
    expect(sessionStorage.getItem(RESEARCH_LIST_FILTER_STORAGE_KEY)).toBeTruthy();
  });

  it("clears storage", () => {
    writeResearchListPersist({
      q: "x",
      status: null,
      scroll: 0,
      sessionId: null,
    });
    clearResearchListPersist();
    expect(readResearchListPersist()).toBeNull();
  });

  it("rejects unknown status values", () => {
    sessionStorage.setItem(
      RESEARCH_LIST_FILTER_STORAGE_KEY,
      JSON.stringify({ q: "a", status: "bogus", scroll: 1, sessionId: "s" }),
    );
    expect(readResearchListPersist()).toEqual({
      q: "a",
      status: null,
      scroll: 1,
      sessionId: "s",
    });
  });
});
