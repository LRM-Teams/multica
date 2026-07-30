// @vitest-environment node
import { describe, it, expect } from "vitest";
import type { TimelineEntry } from "@multica/core/types";
import {
  isActivityLaneEntry,
  isReactableComment,
  activityRunPointer,
} from "./timeline-isolation";

function comment(overrides: Partial<TimelineEntry>): TimelineEntry {
  return {
    type: "comment",
    id: "c1",
    actor_type: "member",
    actor_id: "user-1",
    created_at: "2026-01-16T00:00:00Z",
    comment_type: "comment",
    content: "hello",
    parent_id: null,
    ...overrides,
  };
}

function activity(overrides: Partial<TimelineEntry>): TimelineEntry {
  return {
    type: "activity",
    id: "a1",
    actor_type: "member",
    actor_id: "user-1",
    created_at: "2026-01-16T00:00:00Z",
    action: "status_changed",
    ...overrides,
  };
}

describe("timeline isolation (#243 / E1)", () => {
  describe("isReactableComment", () => {
    it("is true only for a real conversational comment (comment_type=comment)", () => {
      expect(isReactableComment(comment({ comment_type: "comment" }))).toBe(true);
    });

    it("is false for an execution-result comment (status/progress/system)", () => {
      expect(isReactableComment(comment({ comment_type: "status_change" }))).toBe(false);
      expect(isReactableComment(comment({ comment_type: "progress_update" }))).toBe(false);
      expect(isReactableComment(comment({ comment_type: "system" }))).toBe(false);
    });

    it("is false for a real activity entry", () => {
      expect(isReactableComment(activity({}))).toBe(false);
    });

    it("treats a comment with no comment_type as a real comment (safe default)", () => {
      expect(isReactableComment(comment({ comment_type: undefined }))).toBe(true);
    });
  });

  describe("isActivityLaneEntry", () => {
    it("is true for real activity entries", () => {
      expect(isActivityLaneEntry(activity({}))).toBe(true);
    });

    it("is true for execution-result comments (never a Message row)", () => {
      expect(isActivityLaneEntry(comment({ comment_type: "status_change" }))).toBe(true);
      expect(isActivityLaneEntry(comment({ comment_type: "progress_update" }))).toBe(true);
      expect(isActivityLaneEntry(comment({ comment_type: "system" }))).toBe(true);
    });

    it("is false for a real conversational comment", () => {
      expect(isActivityLaneEntry(comment({ comment_type: "comment" }))).toBe(false);
    });

    it("is the exact inverse of isReactableComment for comment entries", () => {
      for (const ct of ["comment", "status_change", "progress_update", "system"] as const) {
        const e = comment({ comment_type: ct });
        expect(isActivityLaneEntry(e)).toBe(!isReactableComment(e));
      }
    });
  });

  describe("activityRunPointer", () => {
    it("returns the run id from details when present", () => {
      expect(
        activityRunPointer(comment({ comment_type: "progress_update", details: { run_id: "run-9" } })),
      ).toBe("run-9");
    });

    it("accepts the alternate pointer keys", () => {
      expect(
        activityRunPointer(comment({ comment_type: "system", details: { activity_run_id: "ar-1" } })),
      ).toBe("ar-1");
      expect(
        activityRunPointer(activity({ details: { task_id: "task-3" } })),
      ).toBe("task-3");
    });

    it("returns null when there is no pointer", () => {
      expect(activityRunPointer(comment({ comment_type: "progress_update", details: {} }))).toBeNull();
      expect(activityRunPointer(comment({ comment_type: "comment" }))).toBeNull();
      expect(activityRunPointer(activity({ details: undefined }))).toBeNull();
    });

    it("ignores non-string / empty pointer values", () => {
      expect(activityRunPointer(activity({ details: { run_id: "" } }))).toBeNull();
      expect(activityRunPointer(activity({ details: { run_id: 42 as unknown as string } }))).toBeNull();
    });
  });
});
