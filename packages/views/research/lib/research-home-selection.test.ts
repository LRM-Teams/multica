import { describe, expect, it } from "vitest";
import type { ResearchSession } from "@multica/core/types/research";
import {
  activeResearchSessions,
  knownResearchAttentionKind,
  selectedResearchSession,
} from "./research-home-selection";

function session(
  id: string,
  status: ResearchSession["status"],
  updatedAt: string,
): ResearchSession {
  return {
    id,
    status,
    updated_at: updatedAt,
    created_at: updatedAt,
    title: id,
    goal: id,
    current_stage: "s1_plan",
  } as ResearchSession;
}

describe("research home selection", () => {
  const sessions = [
    session("failed", "failed", "2026-08-14T12:00:00Z"),
    session("older", "running", "2026-08-14T10:00:00Z"),
    session("newer", "running", "2026-08-14T11:00:00Z"),
  ];

  it("keeps terminal sessions out of the active command surface", () => {
    expect(activeResearchSessions(sessions).map((item) => item.id)).toEqual([
      "newer",
      "older",
    ]);
  });

  it("uses a valid manual selection and otherwise falls back to active work", () => {
    expect(selectedResearchSession(sessions, "older")?.id).toBe("older");
    expect(selectedResearchSession(sessions, "failed")?.id).toBe("newer");
  });

  it("treats unknown attention values as neutral", () => {
    expect(knownResearchAttentionKind("future_state")).toBeNull();
    expect(knownResearchAttentionKind("stalled")).toBe("stalled");
  });
});
