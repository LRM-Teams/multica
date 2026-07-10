import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./activity-event";
import { activityPresentation, type ActivityLabelKey } from "./activity-event";

function toolEvent(tool: string, status = "running"): ActivityEvent {
  return {
    id: "t1",
    agent_id: "agent-1",
    kind: "tool_call",
    event_type: "tool_call",
    occurred_at: "2026-07-11T00:00:00Z",
    visibility: "user_facing",
    tool,
    status,
    target_ref: { kind: "agent", id: "agent-1" },
  };
}

describe("activityPresentation — tool normalization", () => {
  // event.tool is a provider slug, not a stable Raft key (#382): normalize to a
  // Raft semantic action, then the source-backed gerund label.
  const cases: Array<[string, ActivityLabelKey]> = [
    ["bash", "running_command"],
    ["exec_command", "running_command"], // Codex
    ["shell", "running_command"],
    ["write", "writing_file"], // OpenCode
    ["write_file", "writing_file"],
    ["patch_apply", "editing_file"], // Codex
    ["edit_file", "editing_file"],
    ["multi_edit", "editing_file"],
    ["read", "reading_file"], // OpenCode
    ["read_file", "reading_file"], // Grok
    ["Read", "reading_file"], // Claude (capitalized — normalized case-insensitively)
    ["glob", "searching_files"],
    ["grep", "searching_code"],
    ["rg", "searching_code"],
    ["web_search", "searching_web"],
    ["send_message", "sending_message"],
  ];

  it.each(cases)("normalizes provider slug %s to labelKey %s", (tool, labelKey) => {
    expect(activityPresentation(toolEvent(tool)).labelKey).toBe(labelKey);
  });

  it("falls back to the neutral working labelKey for an unknown provider tool (never a raw slug)", () => {
    const p = activityPresentation(toolEvent("some_mystery_tool"));
    expect(p.labelKey).toBe("working");
    // labelKey is a fixed union; the raw slug must not leak via subtext either.
    expect(p.subtext ?? "").not.toContain("mystery");
  });

  it("never uses the raw tool slug as subtext when no safe tool_target is present", () => {
    // Known tool, no tool_target → the label conveys the action, subtext stays
    // empty rather than echoing the raw slug.
    expect(activityPresentation(toolEvent("bash")).subtext).toBeUndefined();
  });

  it("uses the BE-provided safe tool_target as subtext when present", () => {
    const event: ActivityEvent = { ...toolEvent("read_file"), tool_target: "src/app.ts" };
    expect(activityPresentation(event).subtext).toBe("src/app.ts");
  });

  // The trailing "…" active/settled treatment lives in the render layer
  // (ActivityTimeline strips it when tone !== "active"); here we lock the tone
  // that drives it.
  it("tones a running tool active and a settled tool neutral", () => {
    expect(activityPresentation(toolEvent("bash", "running")).tone).toBe("active");
    expect(activityPresentation(toolEvent("bash", "completed")).tone).toBe("neutral");
  });
});
