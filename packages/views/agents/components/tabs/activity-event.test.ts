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
    ["create", "writing_file"], // native file-create tool folded into write family (#413)
    ["create_file", "writing_file"],
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
  it("tones a command amber `running` regardless of status; a non-command tool by status (#404)", () => {
    // Command rows are amber `running` — type-based (raft parity), settled or not,
    // so they never read as a grey idle row.
    expect(activityPresentation(toolEvent("bash", "running")).tone).toBe("running");
    expect(activityPresentation(toolEvent("bash", "completed")).tone).toBe("running");
    // A non-command tool keeps the status-driven tone.
    expect(activityPresentation(toolEvent("read_file", "running")).tone).toBe("active");
    expect(activityPresentation(toolEvent("read_file", "completed")).tone).toBe("neutral");
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
    // A native file-create tool must NOT be dropped (#413): the un-mapped path
    // would vanish the row entirely, worse than the old "Running command create".
    expect(isNarrativeActivityEvent(toolEvent("create"))).toBe(true);
  });

  it("renders a native `create` tool as Writing file with the source path (#413)", () => {
    const p = activityPresentation({ ...toolEvent("create"), tool_target: "/w/hello_world_2.txt" });
    expect(p.labelKey).toBe("writing_file");
    expect(p.subtext).toBe("/w/hello_world_2.txt");
    expect(p.subtextKind).toBe("path"); // basename-preserving path, NOT a command clip
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

  it("drops a `message_sent` event from the mainline — the send already shows as its command row (#404)", () => {
    // The sent content lives in chat + the `multica message send` CLI shows as a
    // "Running command" row, so a `message_sent` (Output) row is a redundant
    // duplicate. Field-driven on `detail_kind`, regardless of activity_kind.
    expect(isNarrativeActivityEvent({ ...evtBase("text"), detail_kind: "message_sent" })).toBe(false);
    expect(isNarrativeActivityEvent({ ...evtBase("custom"), detail_kind: "message_sent" })).toBe(false);
    // A non-send text/Output (e.g. a radar decision) is NOT message_sent → stays.
    expect(isNarrativeActivityEvent({ ...evtBase("text"), detail_kind: "text" })).toBe(true);
  });
});

// Minimal narrative event of a given activity_kind (for the message_sent filter).
function evtBase(activity_kind: ActivityEvent["activity_kind"]): ActivityEvent {
  return {
    id: "m1",
    agent_id: "agent-1",
    occurred_at: "2026-07-13T00:00:00Z",
    activity_kind,
    detail_kind: "text",
    target_ref: { kind: "agent", id: "agent-1" },
  } as ActivityEvent;
}

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

describe("activityPresentation — CLI commands show as Running command, no invented label (#v0 ⑤)", () => {
  // Frank's rule: anything run as a CLI command (bash, and any multica subcommand
  // the daemon canonicalized to a semantic tool like `send_message`) is shown
  // FAITHFULLY as "Running command · <command>", never a product-invented label
  // ("Sending message"). The signal is `entries[].command` (the redacted CLI).
  it("renders a `send_message` (the `multica message send` CLI) as Running command with the real command", () => {
    const event: ActivityEvent = {
      ...toolEvent("send_message"),
      // #503: raft-CLI parse puts the message target in tool_target; the real
      // command is in entries[].command.
      tool_target: "#multica",
      entries: [
        { kind: "tool_call", tool: "send_message", command: 'multica message send --target "#multica"' },
      ],
    };
    const p = activityPresentation(event);
    expect(p.labelKey).toBe("running_command"); // NOT "sending_message"
    expect(p.subtextKind).toBe("command");
    expect(p.subtext).toBe('multica message send --target "#multica"'); // real command inline, not "#multica"
    expect(p.subtextFull).toBe('multica message send --target "#multica"');
  });

  it("passes the full command inline — CSS line-clamp-2 does the 2-line truncation, no manual slice (#404)", () => {
    const long = `multica message send --target "#multica" --body ${"x".repeat(200)}`; // ~240 < 500
    const p = activityPresentation({
      ...toolEvent("send_message"),
      entries: [{ kind: "tool_call", tool: "send_message", command: long }],
    });
    expect(p.subtext).toBe(long); // full inline — no manual "…"; the row clamps to 2 lines in CSS
    expect(p.subtext).not.toContain("…");
    expect(p.subtextFull).toBe(long);
  });

  it("caps a pathologically long command inline at 500 chars (DOM safety) while the full stays for copy (#404)", () => {
    const huge = `bash -lc "${"y".repeat(800)}"`;
    const p = activityPresentation({
      ...toolEvent("bash"),
      entries: [{ kind: "tool_call", tool: "bash", command: huge }],
    });
    expect(p.subtext).toBe(huge.slice(0, 500));
    expect(p.subtext).not.toContain("…"); // ellipsis is CSS, never in the value
    expect(p.subtextFull).toBe(huge);
  });

  it("a bash command carrying entries.command also renders via the same command path", () => {
    const p = activityPresentation({
      ...toolEvent("bash"),
      tool_target: "cd /w && ls…",
      entries: [{ kind: "tool_call", tool: "bash", command: "cd /w && ls -la" }],
    });
    expect(p.labelKey).toBe("running_command");
    expect(p.subtext).toBe("cd /w && ls -la");
    expect(p.subtextKind).toBe("command");
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

  it("presents an executed radar action with its own radar label/tone, and a failed action as a failure", () => {
    expect(activityPresentation(radarEvent("radar_action_executed"))).toMatchObject({
      labelKey: "radar_executed",
      tone: "radar",
    });
    expect(activityPresentation(radarEvent("radar_action_failed"))).toMatchObject({
      labelKey: "failed",
      tone: "failure",
    });
  });
});

describe("agent status transitions — Working ↔ Idle rows (#411/#525)", () => {
  function statusEvent(status: "working" | "idle"): ActivityEvent {
    return {
      ...evtBase("custom"),
      id: `status-${status}`,
      detail_kind: "agent_status_changed",
      status,
      text: status === "idle" ? "Idle" : "Working",
    } as ActivityEvent;
  }

  it("keeps IDLE in the timeline but drops the redundant WORKING status row", () => {
    // "Idle" (end-of-round) is independent info worth a row; a bare "Working"
    // duplicates the wake / actual-work rows that already show the agent working
    // (Frank: "为什么突然多了一个working"). The working EVENT still exists for the
    // header latest-state — it just gets no timeline row.
    expect(isNarrativeActivityEvent(statusEvent("idle"))).toBe(true);
    expect(isNarrativeActivityEvent(statusEvent("working"))).toBe(false);
  });

  it("labels working as active and idle as a settled neutral row", () => {
    expect(activityPresentation(statusEvent("working"))).toMatchObject({
      labelKey: "working",
      tone: "active",
    });
    expect(activityPresentation(statusEvent("idle"))).toMatchObject({
      labelKey: "idle",
      tone: "neutral",
    });
  });

  it("carries no subtext — the label IS the state (no invented detail)", () => {
    expect(activityPresentation(statusEvent("idle")).subtext).toBeUndefined();
    expect(activityPresentation(statusEvent("working")).subtext).toBeUndefined();
  });
});

describe("activityPresentation — held-freshness projection (#441)", () => {
  function holdStatus(): ActivityEvent {
    return { ...evtBase("blocked"), reason_code: "send_freshness_hold" };
  }
  function holdDetail(details?: Record<string, unknown>): ActivityEvent {
    return { ...evtBase("text"), reason_code: "send_freshness_hold_detail", details };
  }

  it("status row (blocked) → canonical label, waiting tone, no subtext", () => {
    const p = activityPresentation(holdStatus());
    expect(p.labelKey).toBe("send_held_by_freshness");
    expect(p.tone).toBe("waiting");
    expect(p.subtext).toBeUndefined();
    // The paired detail row, not the status row, carries the specifics.
    expect(p.subtextKey).toBeUndefined();
  });

  it("detail row (text) → same label + English subtext composed from details", () => {
    const p = activityPresentation(
      holdDetail({
        target: "#general",
        new_message_count: 3,
        shown_message_count: 2,
        omitted_message_count: 1,
        // internal keys that must NOT leak into the reader-facing subtext:
        seen_up_to_seq: 41,
        latest_seq: 44,
        producer_fact_id: "freshness_decision_fact:abc",
        transport_id: "t-9",
        decision: "local_hold",
        reason: "newer_messages_available",
      }),
    );
    expect(p.labelKey).toBe("send_held_by_freshness");
    expect(p.tone).toBe("waiting");
    expect(p.subtext).toBe(
      "3 newer messages in #general (2 shown, 1 not yet shown) — send held until the newer context is reviewed.",
    );
    // No internal ids / seqs / codes bleed into the visible string.
    expect(p.subtext).not.toContain("freshness_decision_fact");
    expect(p.subtext).not.toContain("local_hold");
    expect(p.subtext).not.toContain("44");
  });

  it("singular new_message_count reads 'message', not 'messages'", () => {
    expect(activityPresentation(holdDetail({ target: "dm:@ann", new_message_count: 1 })).subtext).toBe(
      "1 newer message in dm:@ann — send held until the newer context is reviewed.",
    );
  });

  it("degrades gracefully when details are partial or absent", () => {
    // Only a target, no counts.
    expect(activityPresentation(holdDetail({ target: "#general" })).subtext).toBe(
      "Newer messages in #general — send held until the newer context is reviewed.",
    );
    // No details at all → status label only, no subtext (never a fabricated one).
    const bare = activityPresentation(holdDetail(undefined));
    expect(bare.labelKey).toBe("send_held_by_freshness");
    expect(bare.subtext).toBeUndefined();
  });

  it("does not hijack ordinary blocked / text events", () => {
    expect(activityPresentation(evtBase("blocked")).labelKey).toBe("waiting");
    expect(activityPresentation({ ...evtBase("text"), text: "Hello" }).labelKey).toBe("output");
  });
});
