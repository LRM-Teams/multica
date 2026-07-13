import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./activity-event";
import {
  activityPresentation,
  isNarrativeActivityEvent,
  type ActivityLabelKey,
} from "./activity-event";

function toolEvent(tool: string, status = "running"): ActivityEvent {
  return {
    id: "t1",
    agent_id: "agent-1",
    activity_kind: "tool_call",
    detail_kind: "tool_use",
    occurred_at: "2026-07-11T00:00:00Z",
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

  it("never leaks a raw slug for an unknown provider tool (projection stays label-safe)", () => {
    // An un-mapped tool never reaches the user-facing timeline (see
    // isNarrativeActivityEvent filter below); if the projection is ever invoked
    // it still yields the neutral union labelKey, never the raw slug.
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

describe("activityPresentation — mention markdown in narrative subtext (#387)", () => {
  // Output/thinking/subagent subtext is authored message/model text: it still
  // carries mention markdown. The row is a plain-text preview, so the projection
  // normalizes `[@Name](mention://…)` to the display name and never leaks the
  // raw `mention://` URI (Frank's screenshot: the Output preview showed the raw
  // link).
  function narrativeEvent(activity_kind: ActivityEvent["activity_kind"], text: string): ActivityEvent {
    return {
      id: "n1",
      agent_id: "agent-1",
      activity_kind,
      detail_kind: activity_kind,
      occurred_at: "2026-07-11T00:00:00Z",
      text,
      target_ref: { kind: "agent", id: "agent-1" },
    } as ActivityEvent;
  }

  it("strips a member mention from the Output preview to its display name", () => {
    const event = narrativeEvent(
      "text",
      "在的，请问有什么需要我帮您处理的吗？ [@Frank An](mention://member/92f85fa1-dd03-4242-8d3a-c3a80fb35149)",
    );
    const subtext = activityPresentation(event).subtext ?? "";
    expect(subtext).toContain("@Frank An");
    expect(subtext).not.toContain("mention://");
    expect(subtext).not.toContain("](");
  });

  it("strips mentions from thinking prose too", () => {
    const event = narrativeEvent("thinking", "Replying to [@Frank An](mention://member/abc) now.");
    expect(activityPresentation(event).subtext).toBe("Replying to @Frank An now.");
  });

  it("leaves a real markdown link untouched", () => {
    const event = narrativeEvent("text", "See [docs](https://example.com/x) for details.");
    expect(activityPresentation(event).subtext).toBe(
      "See [docs](https://example.com/x) for details.",
    );
  });
});

describe("isNarrativeActivityEvent — un-mapped tool filter (#384)", () => {
  // A tool_call only enters the user-facing timeline when its slug maps to a
  // canonical Raft action. An un-mapped tool (BE didn't canonicalize it, or a
  // parse artifact like a status leaking into `tool`) is dropped — no fake
  // "Working" row, no raw slug. The source-side fix is a BE `unmapped_tool_name`
  // gap event; the FE just refuses to render the un-mapped row.
  it("keeps a mapped tool_call in the narrative", () => {
    expect(isNarrativeActivityEvent(toolEvent("bash"))).toBe(true);
    expect(isNarrativeActivityEvent(toolEvent("read_file"))).toBe(true);
    expect(isNarrativeActivityEvent(toolEvent("Read"))).toBe(true); // case-insensitive
  });

  it("drops an un-mapped tool_call from the narrative", () => {
    expect(isNarrativeActivityEvent(toolEvent("some_mystery_tool"))).toBe(false);
    // A status string leaking into `tool` (parse artifact) is also un-mapped.
    expect(isNarrativeActivityEvent(toolEvent("running"))).toBe(false);
    expect(isNarrativeActivityEvent(toolEvent(""))).toBe(false);
  });

  it("drops raft diagnostic kinds (internal_progress / runtime_diagnostic) from the mainline", () => {
    expect(isNarrativeActivityEvent({ ...toolEvent("bash"), activity_kind: "internal_progress" })).toBe(
      false,
    );
    expect(isNarrativeActivityEvent({ ...toolEvent("bash"), activity_kind: "runtime_diagnostic" })).toBe(
      false,
    );
  });
});

describe("activityPresentation — subtext kind classification (#v0 照实显示)", () => {
  // The row renders the subtext by `subtextKind`: a file path gets the
  // basename-preserving middle-ellipsis; a shell command gets a plain clip +
  // the full redacted command on hover/copy; a search pattern / anything else
  // is plain text. This is the fix for the "命令看不全 / 云里雾里" bug where a
  // bash command (which contains `/`) was wrongly middle-ellipsised as a path.
  it("classifies a shell command as 'command' and exposes the full command from entries", () => {
    const event: ActivityEvent = {
      ...toolEvent("bash"),
      tool_target: "cd /Users/x/workdir && multica message send…",
      entries: [
        { kind: "tool_call", tool: "bash", command: 'cd /Users/x/workdir && multica message send --target "#c"' },
      ],
    };
    const p = activityPresentation(event);
    expect(p.subtextKind).toBe("command");
    expect(p.subtextFull).toBe('cd /Users/x/workdir && multica message send --target "#c"');
  });

  it("does NOT treat a command containing '/' as a path (the mangling bug)", () => {
    const p = activityPresentation({ ...toolEvent("bash"), tool_target: "cd /a/b && ls" });
    expect(p.subtextKind).toBe("command");
    expect(p.subtextKind).not.toBe("path");
  });

  it("classifies a file tool's target as 'path' (keeps the basename-preserving treatment)", () => {
    expect(activityPresentation({ ...toolEvent("read_file"), tool_target: "src/app.ts" }).subtextKind).toBe(
      "path",
    );
    expect(activityPresentation({ ...toolEvent("write_file"), tool_target: "a/b.ts" }).subtextKind).toBe(
      "path",
    );
  });

  it("classifies a search pattern as plain 'text' (no path treatment, no command tooltip)", () => {
    const p = activityPresentation({ ...toolEvent("glob"), tool_target: "**/*.ts" });
    expect(p.subtextKind).toBe("text");
    expect(p.subtextFull).toBeUndefined();
  });

  it("leaves subtextFull undefined for a command with no entries command", () => {
    expect(activityPresentation({ ...toolEvent("bash"), tool_target: "ls" }).subtextFull).toBeUndefined();
  });
});

describe("isNarrativeActivityEvent — Radar actions", () => {
  function radarEvent(
    eventType: "radar_action_executed" | "radar_action_failed",
    reasonCode = "create_issue",
  ): ActivityEvent {
    return {
      id: eventType,
      agent_id: "agent-1",
      activity_kind: "custom",
      detail_kind: eventType,
      occurred_at: "2026-07-11T00:00:00Z",
      text: eventType === "radar_action_failed" ? "Radar failed: create issue" : "Radar executed: create issue",
      reason_code: reasonCode,
      target_ref: { kind: "agent", id: "agent-1" },
    };
  }

  it.each(["radar_action_executed", "radar_action_failed"] as const)(
    "keeps %s in the narrative",
    (eventType) => {
      expect(isNarrativeActivityEvent(radarEvent(eventType))).toBe(true);
    },
  );

  it("drops no_action from the narrative", () => {
    expect(isNarrativeActivityEvent(radarEvent("radar_action_executed", "no_action"))).toBe(false);
  });

  it("drops an action whose execution target was not verified", () => {
    expect(
      isNarrativeActivityEvent(radarEvent("radar_action_failed", "radar_untrusted_target")),
    ).toBe(false);
  });

  it("presents an executed action as settled and a failed action as a failure", () => {
    expect(activityPresentation(radarEvent("radar_action_executed"))).toMatchObject({
      labelKey: "completed",
      tone: "neutral",
    });
    expect(activityPresentation(radarEvent("radar_action_failed"))).toMatchObject({
      labelKey: "failed",
      tone: "failure",
    });
  });
});
