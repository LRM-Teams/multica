import { describe, expect, it } from "vitest";
import { isNoteWorkerJobActive, noteWorkerStatusMessageKey } from "./note-worker-status";

describe("note-worker-status", () => {
  it("treats pending/dispatched/running as active", () => {
    expect(isNoteWorkerJobActive("pending")).toBe(true);
    expect(isNoteWorkerJobActive("dispatched")).toBe(true);
    expect(isNoteWorkerJobActive("running")).toBe(true);
    expect(isNoteWorkerJobActive("completed")).toBe(false);
    expect(isNoteWorkerJobActive("failed")).toBe(false);
    expect(isNoteWorkerJobActive("cancelled")).toBe(false);
    expect(isNoteWorkerJobActive(undefined)).toBe(false);
  });

  it("maps known statuses and unknown drift", () => {
    expect(noteWorkerStatusMessageKey("dispatched")).toBe("dispatched");
    expect(noteWorkerStatusMessageKey("weird-new-status")).toBe("unknown");
  });
});
