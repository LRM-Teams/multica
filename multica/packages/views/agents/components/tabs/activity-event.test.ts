// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./activity-event";
import {
  activityExpansionContent,
  activityPresentation,
  collapseConsecutiveIdle,
  isDecayedFailure,
  isIdleStatusEvent,
  isNarrativeActivityEvent,
  normalizeActivityExpandedText,
  ACTIVITY_TONE_DOT_CLASS,
  type ActivityLabelKey,
} from "./activity-event";

describe("normalizeActivityExpandedText (LRM-554 / LRM-560)", () => {
  it("collapses runs of blank lines to at most one and trims ends", () => {
    expect(normalizeActivityExpandedText("  a  \n\n\n\nb  \n")).toBe("a\n\nb");
  });

  it("maps tones onto design tokens (no hex)", () => {
    expect(ACTIVITY_TONE_DOT_CLASS.running).toBe("bg-running");
    expect(ACTIVITY_TONE_DOT_CLASS.active).toBe("bg-brand");
    expect(ACTIVITY_TONE_DOT_CLASS.waiting).toBe("bg-warning");
    expect(ACTIVITY_TONE_DOT_CLASS.failure).toBe("bg-destructive");
    expect(ACTIVITY_TONE_DOT_CLASS.neutral).toBe("bg-muted-foreground/40");
    expect(Object.values(ACTIVITY_TONE_DOT_CLASS).join(" ")).not.toMatch(/#|F5B301/i);
  });
});

describe("activityExpansionContent", () => {
  function event(
    activity_kind: ActivityEvent["activity_kind"],
    text?: string,
  ): ActivityEvent {
    return {
      id: `detail-${activity_kind}`,
      agent_id: "agent-1",
      activity_kind,
      detail_kind: activity_kind,
      occurred_at: "2026-07-11T00:00:00Z",
      text,
      target_ref: { kind: "agent", id: "agent-1" },
    } as ActivityEvent;
  }

  it("keeps authored markdown intact for the expanded detail, unlike the preview", () => {
    const source = "See [@Frank](mention://member/frank-1) and **verify**.";
    const activity = event("text", source);
    expect(activityPresentation(activity).subtext).not.toContain("mention://");
    expect(activityExpansionContent(activity)).toEqual({ kind: "markdown", content: source });
  });

  it("uses the redacted full CLI command as code detail", () => {
    const activity: ActivityEvent = {
      ...event("tool_call"),
      tool: "bash",
      tool_target: "multica message send…",
      entries: [{ kind: "tool_call", command: "multica message send --token sk_agent_<redacted>" }],
    };
    expect(activityExpansionContent(activity)).toEqual({
      kind: "command",
      content: "multica message send --token sk_agent_<redacted>",
    });
  });

  it("does not create disclosure content for fixed status rows", () => {
    const idle: ActivityEvent = {
      ...event("custom", "Idle"),
      detail_kind: "agent_status_changed",
      status: "idle",
    };
    expect(activityExpansionContent(idle)).toBeUndefined();
  });

  it("expands a held-freshness row into the structured hold block (#765)", () => {
    const freshness: ActivityEvent = {
      ...event("text", "raw transport payload must not surface"),
      detail_kind: "send_freshness_hold_detail",
      details: { target: "#general", new_message_count: 3 },
    };
    expect(activityExpansionContent(freshness)).toEqual({
      kind: "freshness_hold",
      target: "#general",
      newCount: 3,
    });
  });

  it("degrades a held-freshness row to whatever facts the BE supplied (#765)", () => {
    const freshness: ActivityEvent = {
      ...event("text", "held"),
      detail_kind: "send_freshness_hold_detail",
      details: {},
    };
    expect(activityExpansionContent(freshness)).toEqual({
      kind: "freshness_hold",
      target: undefined,
      newCount: undefined,
    });
  });
});

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
    // #601 — the rest of the canonical Raft CLI family.
    ["check_messages", "checking_messages"],
    ["receive_message", "checking_messages"], // shares check_messages' label
    ["wait_for_message", "waiting_for_message"],
    ["read_history", "reading_history"],
    ["search_messages", "searching_messages"],
    ["list_server", "listing_server"],
    ["list_tasks", "listing_tasks"],
    ["create_tasks", "creating_tasks"],
    ["claim_tasks", "claiming_task"],
    ["unclaim_task", "unclaiming_task"],
    ["update_task_status", "updating_task_status"],
    ["add_channel_member", "adding_channel_member"],
    ["join_channel", "joining_channel"],
    ["leave_channel", "leaving_channel"],
    ["upload_file", "uploading_file"],
    ["view_file", "viewing_file"],
    ["list_issues", "listing_issues"],
    ["get_issue", "getting_issue"],
    ["search_issues", "searching_issues"],
    ["list_issue_comments", "listing_issue_comments"],
    ["comment_issue", "commenting_issue"],
    ["delete_issue_comment", "deleting_issue_comment"],
    ["todo_write", "updating_tasks"],
    ["schedule_reminder", "scheduling_reminder"],
    ["list_reminders", "listing_reminders"],
    ["cancel_reminder", "canceling_reminder"],
    ["collab_tool_call", "collaborating"],
    ["web_fetch", "fetching_url"],
  ];

  it.each(cases)("normalizes provider slug %s to labelKey %s", (tool, labelKey) => {
    expect(activityPresentation(toolEvent(tool)).labelKey).toBe(labelKey);
  });

  it("never leaks a raw slug for an unknown provider tool (projection stays label-safe)", () => {
    // An un-mapped tool now reaches the timeline (#601) via the generic,
    // safe "Performing an action" fallback — never the raw slug, never a
    // fabricated subtext.
    const p = activityPresentation(toolEvent("some_mystery_tool"));
    expect(p.labelKey).toBe("performing_action");
    expect(p.subtext).toBeUndefined();
  });

  it("never uses the raw tool slug as subtext when no safe tool_target is present", () => {
    // Known tool, no tool_target → the label conveys the action, subtext stays
    // empty rather than echoing the raw slug.
    expect(activityPresentation(toolEvent("bash")).subtext).toBeUndefined();
  });

  it("refuses to surface command/tool_target detail for an unknown tool, even if the event carries both (#601 security gate)", () => {
    // Parker: the mapped/semantic check must run BEFORE the command branch —
    // an unmapped tool must never reach "Running command · <raw>" just
    // because the event happened to carry entries[].command. The FE can't
    // rely on the BE having already scrubbed an unknown row; it must refuse
    // unconditionally.
    const event: ActivityEvent = {
      ...toolEvent("some_mystery_tool"),
      tool_target: "private-target",
      entries: [{ kind: "tool_call", command: "curl https://private.example/?token=secret" }],
    };
    const presentation = activityPresentation(event);
    expect(presentation.labelKey).toBe("performing_action");
    expect(presentation.subtext).toBeUndefined();
    expect(presentation.subtextFull).toBeUndefined();
    expect(presentation.subtextKind).toBeUndefined();
    expect(activityExpansionContent(event, presentation)).toBeUndefined();
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

describe("isNarrativeActivityEvent — tool_call always narrative (#601, was #384's un-mapped filter)", () => {
  // Every tool_call now reaches the mainline. A canonical tool gets its real
  // gerund label; an un-mapped tool (BE didn't canonicalize it, or a parse
  // artifact like a status leaking into `tool`) still shows, via the generic
  // "Performing an action" fallback — never faked as "Working", never the raw
  // slug, and never silently dropped (Parker: "unknown 不静默丢").
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

  it("keeps an un-mapped tool_call in the narrative — as the generic fallback, not dropped", () => {
    expect(isNarrativeActivityEvent(toolEvent("some_mystery_tool"))).toBe(true);
    // A status string leaking into `tool` (parse artifact) is also un-mapped,
    // but still shows — as "Performing an action", never as that raw string.
    expect(isNarrativeActivityEvent(toolEvent("running"))).toBe(true);
    expect(isNarrativeActivityEvent(toolEvent(""))).toBe(true);
    expect(activityPresentation(toolEvent("some_mystery_tool")).labelKey).toBe("performing_action");
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
    // A non-send text/Output event is NOT message_sent → stays.
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

  it("keeps lifecycle success (Restarted) as a narrative timeline row", () => {
    const restarted: ActivityEvent = {
      ...evtBase("custom"),
      id: "lifecycle-succeeded",
      detail_kind: "agent_lifecycle_succeeded",
      text: "Restarted",
    } as ActivityEvent;
    expect(isNarrativeActivityEvent(restarted)).toBe(true);
    expect(activityPresentation(restarted)).toMatchObject({
      labelKey: "restarted",
      tone: "neutral",
    });
    expect(activityPresentation(restarted).subtext).toBeUndefined();
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

describe("isIdleStatusEvent (#411/#525)", () => {
  it("matches a custom agent_status_changed idle row", () => {
    expect(isIdleStatusEvent(idleAt("i", "2026-07-06T09:00:00Z"))).toBe(true);
  });

  it("does not match a working status row", () => {
    const working: ActivityEvent = {
      ...evtBase("custom"),
      detail_kind: "agent_status_changed",
      status: "working",
    } as ActivityEvent;
    expect(isIdleStatusEvent(working)).toBe(false);
  });

  it("does not match an ordinary custom/text event", () => {
    expect(isIdleStatusEvent(evtBase("text"))).toBe(false);
    expect(
      isIdleStatusEvent({ ...evtBase("custom"), detail_kind: "some_other_detail" }),
    ).toBe(false);
  });
});

// Idle status events for the merge (distinct ids + ascending timestamps so the
// "latest timestamp" pick is observable).
function idleAt(id: string, iso: string): ActivityEvent {
  return {
    ...evtBase("custom"),
    id,
    detail_kind: "agent_status_changed",
    status: "idle",
    occurred_at: iso,
  } as ActivityEvent;
}

describe("collapseConsecutiveIdle (LRM-566 方案 B)", () => {
  it("returns no items for an empty stream", () => {
    expect(collapseConsecutiveIdle([])).toEqual([]);
  });

  it("passes a non-idle stream through as individual event items", () => {
    const a = evtBase("text");
    const b = toolEvent("bash");
    expect(collapseConsecutiveIdle([a, b])).toEqual([
      { kind: "event", event: a },
      { kind: "event", event: b },
    ]);
  });

  it("collapses a run of consecutive idle rows into one idle item preserving order", () => {
    const i1 = idleAt("i1", "2026-07-06T09:36:14Z");
    const i2 = idleAt("i2", "2026-07-06T09:36:30Z");
    const i3 = idleAt("i3", "2026-07-06T09:36:48Z");
    const before = evtBase("text");
    const after = toolEvent("bash");
    expect(collapseConsecutiveIdle([before, i1, i2, i3, after])).toEqual([
      { kind: "event", event: before },
      { kind: "idle", events: [i1, i2, i3] },
      { kind: "event", event: after },
    ]);
  });

  it("produces a separate idle item per non-adjacent idle run", () => {
    const run1 = [idleAt("a1", "2026-07-06T09:00:00Z"), idleAt("a2", "2026-07-06T09:00:10Z")];
    const run2 = [idleAt("b1", "2026-07-06T09:01:00Z")];
    const sep = evtBase("text");
    expect(collapseConsecutiveIdle([...run1, sep, ...run2])).toEqual([
      { kind: "idle", events: run1 },
      { kind: "event", event: sep },
      { kind: "idle", events: run2 },
    ]);
  });

  it("keeps a lone idle as its own idle item (de-emphasis applies uniformly)", () => {
    const lone = idleAt("lone", "2026-07-06T09:36:14Z");
    expect(collapseConsecutiveIdle([lone])).toEqual([{ kind: "idle", events: [lone] }]);
  });

  it("flushes a trailing idle run at the end of the stream", () => {
    const i1 = idleAt("i1", "2026-07-06T09:00:00Z");
    const i2 = idleAt("i2", "2026-07-06T09:00:05Z");
    expect(collapseConsecutiveIdle([evtBase("text"), i1, i2])).toEqual([
      { kind: "event", event: evtBase("text") },
      { kind: "idle", events: [i1, i2] },
    ]);
  });
});

describe("activityPresentation — held-freshness projection (#441)", () => {
  function holdStatus(): ActivityEvent {
    return { ...evtBase("blocked"), detail_kind: "send_freshness_hold", reason_code: "" };
  }
  function holdDetail(details?: Record<string, unknown>): ActivityEvent {
    return { ...evtBase("text"), detail_kind: "send_freshness_hold_detail", reason_code: "", details };
  }

  it("status row (blocked) → canonical label, waiting tone, no subtext", () => {
    const p = activityPresentation(holdStatus());
    expect(p.labelKey).toBe("send_held_by_freshness");
    expect(p.tone).toBe("waiting");
    expect(p.subtext).toBeUndefined();
    // The paired detail row, not the status row, carries the specifics.
    expect(p.subtextKey).toBeUndefined();
  });

  it("detail row (text) → label-only, waiting tone; specifics move to the expanded block (#765)", () => {
    // Iris #765 Raft parity: the collapsed row is just the label; target / new
    // messages / decision live in the expanded structured block.
    const p = activityPresentation(
      holdDetail({ target: "#general", new_message_count: 3, shown_message_count: 2 }),
    );
    expect(p.labelKey).toBe("send_held_by_freshness");
    expect(p.tone).toBe("waiting");
    expect(p.subtext).toBeUndefined();
    expect(p.subtextKey).toBeUndefined();
  });

  it("the expanded hold block surfaces only target + new count, never internal ids/seqs/codes (#765)", () => {
    const expansion = activityExpansionContent(
      holdDetail({
        target: "#general",
        new_message_count: 3,
        // internal keys that must NOT surface:
        seen_up_to_seq: 41,
        producer_fact_id: "freshness_decision_fact:abc",
        decision: "local_hold",
      }),
    );
    expect(expansion).toEqual({ kind: "freshness_hold", target: "#general", newCount: 3 });
    const serialized = JSON.stringify(expansion);
    expect(serialized).not.toContain("freshness_decision_fact");
    expect(serialized).not.toContain("local_hold");
    expect(serialized).not.toContain("41");
  });

  it("degrades gracefully when details are partial or absent (#765)", () => {
    // Only a target → target present, count absent.
    expect(activityExpansionContent(holdDetail({ target: "#general" }))).toEqual({
      kind: "freshness_hold",
      target: "#general",
      newCount: undefined,
    });
    // No details at all → still a hold block, all facts absent (never fabricated).
    expect(activityExpansionContent(holdDetail(undefined))).toEqual({
      kind: "freshness_hold",
      target: undefined,
      newCount: undefined,
    });
    // The collapsed presentation stays label-only regardless.
    const bare = activityPresentation(holdDetail(undefined));
    expect(bare.labelKey).toBe("send_held_by_freshness");
    expect(bare.subtext).toBeUndefined();
  });

  it("does not hijack ordinary blocked / text events", () => {
    expect(activityPresentation(evtBase("blocked")).labelKey).toBe("waiting");
    expect(activityPresentation({ ...evtBase("text"), text: "Hello" }).labelKey).toBe("output");
  });

  it("uses the canonical detail kind, not the empty reason code from the transport event", () => {
    expect(activityPresentation({ ...evtBase("text"), reason_code: "send_freshness_hold_detail" }).labelKey).toBe("output");
  });
});

describe("isDecayedFailure (task #13)", () => {
  it("is not decayed when fresh and nothing newer exists", () => {
    const now = Date.parse("2026-08-01T10:00:00Z");
    expect(isDecayedFailure("2026-08-01T09:55:00Z", false, now)).toBe(false);
  });

  it("decays once older than the 15-minute threshold", () => {
    const now = Date.parse("2026-08-01T10:00:00Z");
    expect(isDecayedFailure("2026-08-01T09:44:00Z", false, now)).toBe(true);
    expect(isDecayedFailure("2026-08-01T09:46:00Z", false, now)).toBe(false);
  });

  it("decays immediately when a newer event exists, regardless of age", () => {
    const now = Date.parse("2026-08-01T10:00:00Z");
    expect(isDecayedFailure("2026-08-01T09:59:30Z", true, now)).toBe(true);
  });

  it("does not decay on an invalid timestamp when nothing newer exists", () => {
    expect(isDecayedFailure("not-a-date", false)).toBe(false);
  });
});
