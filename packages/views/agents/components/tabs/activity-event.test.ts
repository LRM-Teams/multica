import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./activity-event";
import { activityPresentation } from "./activity-event";

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
  const cases: Array<[string, string]> = [
    ["bash", "Running command…"],
    ["exec_command", "Running command…"], // Codex
    ["shell", "Running command…"],
    ["write", "Writing file…"], // OpenCode
    ["write_file", "Writing file…"],
    ["patch_apply", "Editing file…"], // Codex
    ["edit_file", "Editing file…"],
    ["multi_edit", "Editing file…"],
    ["read", "Reading file…"], // OpenCode
    ["read_file", "Reading file…"], // Grok
    ["Read", "Reading file…"], // Claude (capitalized — normalized case-insensitively)
    ["glob", "Searching files…"],
    ["grep", "Searching code…"],
    ["rg", "Searching code…"],
    ["web_search", "Searching web…"],
    ["send_message", "Sending message…"],
  ];

  it.each(cases)("maps provider slug %s -> %s while active", (tool, label) => {
    expect(activityPresentation(toolEvent(tool)).label).toBe(label);
  });

  it("falls back to a neutral working row for an unknown provider tool without leaking the raw slug in label OR subtext", () => {
    const p = activityPresentation(toolEvent("some_mystery_tool"));
    expect(p.label).toBe("Working…");
    expect(p.label).not.toContain("mystery");
    // The raw slug must not leak via subtext either (no safe tool_target here).
    expect(p.subtext ?? "").not.toContain("mystery");
  });

  it("never uses the raw tool slug as subtext when no safe tool_target is present", () => {
    // Known tool, no tool_target → label conveys the action, subtext stays empty
    // rather than echoing the raw slug.
    expect(activityPresentation(toolEvent("bash")).subtext).toBeUndefined();
  });

  it("uses the BE-provided safe tool_target as subtext when present", () => {
    const event: ActivityEvent = { ...toolEvent("read_file"), tool_target: "src/app.ts" };
    expect(activityPresentation(event).subtext).toBe("src/app.ts");
  });

  it("keeps the trailing … only while the tool is active, drops it once settled", () => {
    expect(activityPresentation(toolEvent("bash", "running")).label).toBe("Running command…");
    expect(activityPresentation(toolEvent("bash", "completed")).label).toBe("Running command");
  });

  it("tones a running tool active and a settled tool neutral", () => {
    expect(activityPresentation(toolEvent("bash", "running")).tone).toBe("active");
    expect(activityPresentation(toolEvent("bash", "completed")).tone).toBe("neutral");
  });
});
