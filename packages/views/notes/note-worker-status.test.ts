import { describe, expect, it } from "vitest";
import { isNoteWorkerJobActive, noteWorkerRunHref, noteWorkerStatusMessageKey } from "./note-worker-status";

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

  it("builds agent run deep links", () => {
    const paths = { agentDetail: (id: string) => `/acme/members?member=agent%3A${id}` };
    const append = (href: string, params: Record<string, string>) => {
      const qs = Object.entries(params)
        .map(([k, v]) => `${k}=${v}`)
        .join("&");
      return href.includes("?") ? `${href}&${qs}` : `${href}?${qs}`;
    };
    expect(noteWorkerRunHref("a1", null, paths, append)).toBe("/acme/members?member=agent%3Aa1");
    expect(noteWorkerRunHref("a1", "run-9", paths, append)).toBe(
      "/acme/members?member=agent%3Aa1&run=run-9",
    );
  });
});
