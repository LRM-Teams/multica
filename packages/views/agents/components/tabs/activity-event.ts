import type { AgentActivityTimelineEvent } from "@multica/core/types";
import { stripMentionMarkdown } from "../../../common/strip-mention-markdown";

// FE read-model for the agent-activity narrative timeline (#267 / #302 / #389).
// The BE supplies source-backed facts (`activity_kind`/`detail_kind`, `text`,
// `reason_code`, refs, `entries`); display labels and dot tone are FE projection,
// not API fields. Mainline vs diagnostic is driven by `activity_kind` semantics
// (raft-aligned #389), NOT a `visibility` flag (removed in the cutover).

// LRM-560 — Activity unified design language: node colors are full tokens
// (command=running, in-progress=brand, waiting=warning, failure=destructive,
// idle/neutral=muted). Live presence still uses the avatar pulse separately.
export type ActivityDotTone = "neutral" | "active" | "running" | "waiting" | "failure";

// SINGLE source for the tone → dot-color map. Both the Activity timeline and the
// name-row live-status header project the SAME latest Activity row, so the dot
// must read from ONE table — never two hand-kept copies that can drift.
export const ACTIVITY_TONE_DOT_CLASS: Record<ActivityDotTone, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  running: "bg-running",
  waiting: "bg-warning",
  failure: "bg-destructive",
};

/**
 * LRM-554 / LRM-560 — collapse model blank lines before `pre-wrap` / markdown
 * so expanded thinking/output doesn't paint large empty gaps.
 */
export function normalizeActivityExpandedText(text: string): string {
  return text
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((line) => line.replace(/[ \t]+$/g, ""))
    .join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

// The FE Activity read-model IS the BE #302 timeline event
// (`AgentActivityTimelineEvent`, packages/core/types/events.ts): id /
// occurred_at / activity_kind / detail_kind / text / reason / refs / entries
// drive the rendered row. Aliasing to the BE type keeps the four layers
// (daemon -> server -> API -> FE) one raw shape; presentation stays in the
// component layer.
export type ActivityEvent = AgentActivityTimelineEvent;

// Labels are i18n KEYS resolved in the component — the raft-exact source strings
// live in `agents.json` (`tab_body.activity.labels`). A fixed subtext (e.g.
// "Message received") is also a key (`…subtexts`); a dynamic subtext (tool target
// path, reply text, block reason) is passed through verbatim and NEVER translated.
export type ActivityLabelKey =
  | "thinking"
  | "output"
  | "completed"
  | "working"
  | "idle"
  | "restart_prepared"
  | "disconnected"
  | "failed"
  | "waiting"
  | "running_command"
  | "writing_file"
  | "editing_file"
  | "reading_file"
  | "searching_files"
  | "searching_code"
  | "searching_web"
  | "sending_message"
  | "send_held_by_freshness"
  // #601 — the rest of the canonical tool/CLI family Barry's BE now emits.
  // One label per canonical tool name (see TOOL_ACTION_KEY below); no raw
  // slug ever reaches this union.
  | "checking_messages"
  | "waiting_for_message"
  | "reading_history"
  | "searching_messages"
  | "listing_server"
  | "listing_tasks"
  | "creating_tasks"
  | "claiming_task"
  | "unclaiming_task"
  | "updating_task_status"
  | "adding_channel_member"
  | "joining_channel"
  | "leaving_channel"
  | "uploading_file"
  | "viewing_file"
  | "listing_issues"
  | "getting_issue"
  | "searching_issues"
  | "listing_issue_comments"
  | "commenting_issue"
  | "deleting_issue_comment"
  | "updating_tasks"
  | "scheduling_reminder"
  | "listing_reminders"
  | "canceling_reminder"
  | "collaborating"
  | "fetching_url"
  // Generic, safe fallback for a tool the FE doesn't (yet) recognize a
  // canonical label for. Never the raw slug, never a fabricated subtext —
  // diagnostic detail lives behind the existing raw/diagnostic surfaces,
  // never this mainline row (#601, Parker: "unknown 不静默丢").
  | "performing_action";

export type ActivitySubtextKey =
  | "message_received"
  | "compacting_context"
  | "compaction_finished"
  | "subagent_activity";

// Activity is ENGLISH-ONLY for now (Frank 2026-07-14): labels/subtexts render
// from these canonical maps — the raft-exact source strings — instead of a
// locale i18n lookup. The `tab_body.activity.*` locale keys are intentionally
// kept so a locale layer can be re-added later over the SAME semantic keys
// without touching this projection (Parker).
export const ACTIVITY_LABEL_EN: Record<ActivityLabelKey, string> = {
  thinking: "Thinking",
  output: "Output",
  completed: "Completed",
  working: "Working",
  idle: "Idle",
  restart_prepared: "Restart prepared",
  disconnected: "Disconnected",
  failed: "Failed",
  waiting: "Waiting",
  running_command: "Running command",
  writing_file: "Writing file",
  editing_file: "Editing file",
  reading_file: "Reading file",
  searching_files: "Searching files",
  searching_code: "Searching code",
  searching_web: "Searching web",
  sending_message: "Sending message",
  send_held_by_freshness: "Send held by freshness check",
  checking_messages: "Checking messages",
  waiting_for_message: "Waiting for message",
  reading_history: "Reading history",
  searching_messages: "Searching messages",
  listing_server: "Listing server",
  listing_tasks: "Listing tasks",
  creating_tasks: "Creating tasks",
  claiming_task: "Claiming task",
  unclaiming_task: "Unclaiming task",
  updating_task_status: "Updating task status",
  adding_channel_member: "Adding channel member",
  joining_channel: "Joining channel",
  leaving_channel: "Leaving channel",
  uploading_file: "Uploading file",
  viewing_file: "Viewing file",
  listing_issues: "Listing issues",
  getting_issue: "Getting issue",
  searching_issues: "Searching issues",
  listing_issue_comments: "Listing issue comments",
  commenting_issue: "Commenting on issue",
  deleting_issue_comment: "Deleting issue comment",
  updating_tasks: "Updating tasks",
  scheduling_reminder: "Scheduling reminder",
  listing_reminders: "Listing reminders",
  canceling_reminder: "Canceling reminder",
  collaborating: "Collaborating",
  fetching_url: "Fetching URL",
  performing_action: "Performing an action",
};

export const ACTIVITY_SUBTEXT_EN: Record<ActivitySubtextKey, string> = {
  message_received: "Message received",
  compacting_context: "Compacting context",
  compaction_finished: "Context compaction finished",
  subagent_activity: "Subagent activity",
};

// Activity chrome (non-event UI: empty state, command Copy, jump-to-latest,
// diagnostics toggle) is English-only too (Frank 2026-07-14 "整条 Activity 全英文"),
// so a zh viewer never sees English event rows framed by localized controls.
// One canonical map, shared by every Activity chrome render site (timeline empty +
// command Copy here, jump-to-latest / diagnostics toggle in their own components).
// The `locales/*/agents.json` chrome keys stay in place for a future one-layer
// re-localize of the whole timeline — same "先" (for-now) posture as the labels.
export const ACTIVITY_CHROME_EN = {
  copy_command: "Copy",
  command_copied: "Copied",
  expanded_detail_scrollable: "Expanded details, scrollable",
  timeline_empty: "No activity yet",
  // LRM-563 / LRM-558 P2 — full-page empty / error / loading chrome (English-only).
  timeline_empty_hint:
    "Activity will show up here on a timeline once this agent starts working.",
  timeline_load_failed: "Couldn't load activity",
  timeline_load_failed_hint: "Connection interrupted or the service is unavailable.",
  retry: "Retry",
  loading: "Loading",
  jump_to_latest: "Jump to latest",
  view_diagnostics: "View diagnostic details",
  hide_diagnostics: "Hide diagnostic details",
  // #765 held-freshness expanded detail — English, verbatim to the Raft hold
  // reference (d877983d): `target: … / new messages: … / decision: …`.
  hold_target_label: "target:",
  hold_new_messages_label: "new messages:",
  hold_newer_message: "newer message",
  hold_newer_messages: "newer messages",
  hold_decision_label: "decision:",
  hold_decision_value: "local hold; review the newer context before retrying",
} as const;

export interface ActivityPresentation {
  labelKey: ActivityLabelKey;
  /** Fixed subtext, resolved via i18n (Message received, Compacting context…). */
  subtextKey?: ActivitySubtextKey;
  /** Dynamic subtext (tool target, reply text, reason) — rendered verbatim. */
  subtext?: string;
  /**
   * How to render `subtext` (#v0 照实显示): a file `path` gets the
   * basename-preserving middle-ellipsis treatment; a shell `command` renders as
   * a plain single-line clip with the full redacted command on hover/copy (never
   * the path treatment — a command contains `/` but is NOT a path); everything
   * else is plain `text`. Undefined for non-tool rows.
   */
  subtextKind?: "path" | "command" | "text";
  /**
   * Full untruncated value for the hover tooltip / copy affordance when the
   * inline `subtext` is a clip — e.g. the full redacted command from
   * `entries[].command` while `subtext` shows the BE's compact clip.
   */
  subtextFull?: string;
  tone: ActivityDotTone;
}

/**
 * The full, safe detail that can sit behind a narrative Activity row. This is
 * deliberately separate from `ActivityPresentation`: the presentation's
 * `subtext` is a one-line preview and normalizes mention markdown for that
 * compact surface, while an expanded row must preserve the original authored
 * markdown and its normal message-body interactions.
 */
export type ActivityExpansionContent =
  | { kind: "markdown"; content: string }
  | { kind: "command"; content: string }
  // #765 held-freshness: a structured 3-line block (target chip / new messages /
  // decision), NOT authored markdown — composed from the BE's narrow detail
  // allowlist. Rendered by the timeline as the Raft-parity hold detail.
  | { kind: "freshness_hold"; target?: string; newCount?: number };

function reasonText(event: ActivityEvent): string {
  return event.reason_code?.trim().replaceAll("_", " ") ?? "";
}

// Free-form model/message text (Output body, thinking prose, subagent detail)
// is authored markdown — it still carries mention syntax like
// `[@Frank An](mention://member/id)`. The Activity row shows a plain-text
// preview, so normalize mentions to their display name (`@Frank An`) before
// display; real markdown links (`[docs](https://…)`) are left untouched (#387:
// the raw `mention://` URI was leaking into the Output preview). Tool targets
// are BE-provided safe summaries (basename / clipped query) and never carry
// mentions, so they skip this.
function narrativeText(text: string | null | undefined): string | undefined {
  const trimmed = text ?? undefined;
  return trimmed === undefined ? undefined : stripMentionMarkdown(trimmed);
}

export function isNarrativeActivityEvent(event: ActivityEvent): boolean {
  // A `message_sent` event is the RESULT of an agent sending a message: the sent
  // content lives in the chat stream, and the `multica message send` CLI already
  // shows as its own "Running command" row — so an "Output · <sent content>" row
  // here is a redundant duplicate (Frank/#404, raft parity: a send produces no
  // separate Output row). Keep it diagnostic. Field-driven (`detail_kind`), never
  // string-sniffed. Non-send `text`/Output (e.g. a scheduled-task decision) is NOT
  // `message_sent` and stays mainline (Frank: observability is fine).
  if (event.detail_kind === "message_sent") return false;
  switch (event.activity_kind) {
    case "tool_output":
    case "transport":
    case "telemetry":
    case "turn_end":
    case "session_init":
    case "internal_progress":
    case "runtime_diagnostic":
      return false;
    case "tool_call":
      // Every tool_call now reaches the mainline (#601, Parker: "unknown 不
      // 静默丢"). A canonical tool gets its real gerund label; a tool the FE
      // doesn't recognize still shows — as the generic, safe "Performing an
      // action" row (`toolPresentation`'s fallback), never faked as "Working"
      // and never leaking the raw slug. Superseded the pre-#601 behavior of
      // dropping an un-mapped row entirely (#384) — that made a real gap
      // invisible instead of surfacing it honestly.
      return true;
    case "custom":
      return (
        // Agent status → timeline: keep IDLE only, drop WORKING. "Working" the
        // agent already conveys via the actual work rows (thinking / Running
        // command / Writing file…) and the wake "Working · Message received" row
        // that opens the round — a bare "Working" status row right after the wake
        // row is the same transition written twice (Frank: "为什么突然多了一个
        // working"). Raft models status as presence (a dot / header projection of
        // the latest activity kind), not a text row, so a redundant "Working" line
        // is an invented duplicate. "Idle" IS kept — end-of-round is independent
        // info not otherwise visible in the timeline. The WORKING event still
        // exists in the stream for the header/hover latest-state projection; we
        // just don't give it its own timeline row.
        (event.detail_kind === "agent_status_changed" && event.status !== "working") ||
        // User-initiated lifecycle success (Parker 2026-08-02 / Frank: restart
        // must leave a visible Activity row). BE event_type
        // agent_lifecycle_succeeded — not the diagnostic_only restarted_by_user
        // kill path (#62).
        event.detail_kind === "agent_lifecycle_succeeded" ||
        event.detail_kind.includes("subagent")
      );
    default:
      return true;
  }
}

/**
 * An Idle status row (#411/#525): the end-of-round presence transition the
 * timeline keeps (a bare "Working" status row is dropped as redundant). Idle is
 * low-signal vs the action rows around it, so LRM-566 方案 B both merges runs
 * of these and de-emphasizes the result.
 */
export function isIdleStatusEvent(event: ActivityEvent): boolean {
  return (
    event.activity_kind === "custom" &&
    event.detail_kind === "agent_status_changed" &&
    event.status === "idle"
  );
}

/**
 * LRM-566 方案 B — a run of consecutive Idle status rows merged into a single
 * de-emphasized timeline line. The group carries every merged event so the row
 * can render `Idle · N` + the latest timestamp without inventing copy.
 */
export interface MergedIdleActivityItem {
  kind: "idle";
  events: ActivityEvent[];
}

/** A single (non-idle) narrative event rendered as its own row. */
export interface EventActivityItem {
  kind: "event";
  event: ActivityEvent;
}

/**
 * What the Activity timeline iterates over. Most rows are plain events; a run
 * of consecutive Idle status rows collapses to one `idle` item (LRM-566 方案 B)
 * so the timeline no longer paints a stack of empty middle-gap Idle lines.
 */
export type ActivityTimelineItem = EventActivityItem | MergedIdleActivityItem;

/**
 * Collapse consecutive Idle status rows into a single `idle` timeline item
 * (LRM-566 方案 B). Operates on already-narrative events (the caller filters
 * diagnostics first via `isNarrativeActivityEvent`); a lone Idle still becomes
 * an `idle` item so the de-emphasized Idle styling applies uniformly. Pure so
 * the merge is unit-testable independent of React.
 */
export function collapseConsecutiveIdle(events: ActivityEvent[]): ActivityTimelineItem[] {
  const items: ActivityTimelineItem[] = [];
  let idle: ActivityEvent[] = [];
  const flushIdle = () => {
    if (idle.length > 0) {
      items.push({ kind: "idle", events: idle });
      idle = [];
    }
  };
  for (const event of events) {
    if (isIdleStatusEvent(event)) {
      idle.push(event);
    } else {
      flushIdle();
      items.push({ kind: "event", event });
    }
  }
  flushIdle();
  return items;
}

function normalizedTool(event: ActivityEvent): string {
  return event.tool?.trim().toLowerCase() ?? "";
}

// Subtext is ONLY the BE-provided safe summary (`tool_target`: a path basename /
// clipped query / pattern). Never fall back to the raw `event.tool` slug — for an
// unknown provider tool that would leak the raw name into the row (#382 gate:
// unknown tools show no raw slug in label OR subtext).
function toolTarget(event: ActivityEvent): string | undefined {
  return event.tool_target?.trim() || undefined;
}

function isActiveStatus(status: string | undefined): boolean {
  return (
    status === "queued" ||
    status === "dispatched" ||
    status === "running"
  );
}

function statusTone(event: ActivityEvent): ActivityDotTone {
  return isActiveStatus(event.status) ? "active" : "neutral";
}

// Multica providers pass their own tool slugs — Codex `exec_command`/`patch_apply`,
// OpenCode `bash`/`read`/`write`/`glob`, Grok `read_file`, Claude capitalized
// `Read`, etc. — so `event.tool` is NOT a stable Raft key set (confirmed with BE,
// #382). Normalize the provider slug to a Raft semantic action, then use the
// source-backed gerund label (raft `TOOL_DISPLAY_METADATA`). An un-mapped slug
// (BE didn't canonicalize it, or a parse artifact) still reaches the timeline
// (#601) via `toolPresentation`'s generic "Performing an action" fallback —
// never faked as "Working" and never echoing the raw name (#384).
const TOOL_SEMANTIC: Record<string, string> = {
  bash: "command",
  exec_command: "command",
  exec: "command",
  shell: "command",
  terminal: "command",
  command: "command",
  write: "write",
  write_file: "write",
  // A runtime's native file-create tool (raft folds `create`/`create_file` into
  // the `write_file` family, #413). Without these aliases the slug is un-mapped →
  // the row is dropped from the timeline (worse than the old "Running command
  // create"): map to `write` so it shows "Writing file · <path>" whether the BE
  // forwards `tool=write_file` or the raw `tool=create`.
  create: "write",
  create_file: "write",
  createfile: "write",
  patch_apply: "edit",
  edit: "edit",
  edit_file: "edit",
  file_edit: "edit",
  multi_edit: "edit",
  read: "read",
  read_file: "read",
  open: "read",
  cat: "read",
  glob: "glob",
  grep: "grep",
  rg: "grep",
  search: "grep",
  web_search: "web_search",
  websearch: "web_search",
  send_message: "send_message",
  // #601 — the rest of the canonical Raft CLI family (Barry's BE alias table,
  // server/internal/handler/agent_activity.go `agentActivityToolAliases`).
  // These arrive from the BE already canonicalized (no raw provider slugs to
  // fold), but are listed here in the same semantic → label indirection as
  // everything else so a future alias only ever needs one new entry.
  check_messages: "check_messages",
  receive_message: "check_messages", // Barry: check_messages/receive_message share one label
  wait_for_message: "wait_for_message",
  read_history: "read_history",
  search_messages: "search_messages",
  list_server: "list_server",
  list_tasks: "list_tasks",
  create_tasks: "create_tasks",
  claim_tasks: "claim_tasks",
  unclaim_task: "unclaim_task",
  update_task_status: "update_task_status",
  add_channel_member: "add_channel_member",
  join_channel: "join_channel",
  leave_channel: "leave_channel",
  upload_file: "upload_file",
  view_file: "view_file",
  list_issues: "list_issues",
  get_issue: "get_issue",
  search_issues: "search_issues",
  list_issue_comments: "list_issue_comments",
  comment_issue: "comment_issue",
  delete_issue_comment: "delete_issue_comment",
  todo_write: "todo_write",
  schedule_reminder: "schedule_reminder",
  list_reminders: "list_reminders",
  cancel_reminder: "cancel_reminder",
  collab_tool_call: "collab_tool_call",
  web_fetch: "web_fetch",
};

const TOOL_ACTION_KEY: Record<string, ActivityLabelKey> = {
  command: "running_command",
  write: "writing_file",
  edit: "editing_file",
  read: "reading_file",
  glob: "searching_files",
  grep: "searching_code",
  web_search: "searching_web",
  // NOTE: no `send_message` → "sending_message" entry. A `multica message send`
  // (which the daemon canonicalizes to `send_message`) is a CLI command and is
  // shown as "Running command · <command>" via the command branch in
  // `toolPresentation` — Frank's #v0 rule: don't invent a "Sending message"
  // label, show the real command like any other CLI.
  check_messages: "checking_messages",
  wait_for_message: "waiting_for_message",
  read_history: "reading_history",
  search_messages: "searching_messages",
  list_server: "listing_server",
  list_tasks: "listing_tasks",
  create_tasks: "creating_tasks",
  claim_tasks: "claiming_task",
  unclaim_task: "unclaiming_task",
  update_task_status: "updating_task_status",
  add_channel_member: "adding_channel_member",
  join_channel: "joining_channel",
  leave_channel: "leaving_channel",
  upload_file: "uploading_file",
  view_file: "viewing_file",
  list_issues: "listing_issues",
  get_issue: "getting_issue",
  search_issues: "searching_issues",
  list_issue_comments: "listing_issue_comments",
  comment_issue: "commenting_issue",
  delete_issue_comment: "deleting_issue_comment",
  todo_write: "updating_tasks",
  schedule_reminder: "scheduling_reminder",
  list_reminders: "listing_reminders",
  cancel_reminder: "canceling_reminder",
  collab_tool_call: "collaborating",
  web_fetch: "fetching_url",
};

// The full redacted command lives in `entries[].command` (#389 two-tier: the
// compact clip is `tool_target` inline, the full command is for hover/copy).
// Pull the first entry that carries one.
function fullCommand(event: ActivityEvent): string | undefined {
  return event.entries?.find((e) => e.command?.trim())?.command?.trim() || undefined;
}

/**
 * Returns only detail that is both real and safe to reveal in the full
 * Activity timeline. Status/fact rows intentionally return nothing: they have
 * no additional source-backed content, so giving them an empty disclosure
 * would be a misleading affordance.
 */
export function activityExpansionContent(
  event: ActivityEvent,
  presentation = activityPresentation(event),
): ActivityExpansionContent | undefined {
  if (presentation.subtextKind === "command") {
    const command = presentation.subtextFull ?? presentation.subtext;
    return command ? { kind: "command", content: command } : undefined;
  }

  // #765 held-freshness: expand into a structured 3-line block (target chip /
  // new messages / decision), composed from the BE's narrow detail allowlist —
  // never raw payload or authored markdown. Any key may be absent; the timeline
  // renders only the facts present.
  if (event.detail_kind === "send_freshness_hold_detail") {
    const d = event.details;
    const target =
      typeof d?.target === "string" && d.target.trim() ? d.target.trim() : undefined;
    const raw = d?.new_message_count;
    const newCount =
      typeof raw === "number" && Number.isFinite(raw) && raw >= 0 ? raw : undefined;
    return { kind: "freshness_hold", target, newCount };
  }
  if (event.detail_kind === "message_sent") return undefined;
  if (event.activity_kind === "custom" && event.detail_kind === "agent_status_changed") {
    return undefined;
  }

  switch (event.activity_kind) {
    case "thinking":
    case "text":
    case "error":
    case "blocked":
    case "custom": {
      const content = event.text?.trim();
      return content ? { kind: "markdown", content } : undefined;
    }
    default:
      return undefined;
  }
}

// DOM-size safety bound for the inline command string. The VISUAL truncation is
// CSS `line-clamp-2` (raft-parity two-line preview + trailing ellipsis, #404);
// this cap just keeps a pathologically long command out of the DOM text node.
// The full redacted command always stays reachable via click-to-expand + copy
// (`subtextFull` from `entries[].command`).
const COMMAND_INLINE_CAP = 500;

function toolPresentation(event: ActivityEvent): ActivityPresentation {
  // #601 security gate (Parker): the mapped/semantic check MUST run before
  // anything else, including the command branch below. `fullCommand()` reads
  // `entries[].command` regardless of whether `tool` is recognized — if an
  // unmapped/unknown tool happened to carry that field (a BE version drift,
  // a partially-scrubbed row, anything), checking command first would still
  // surface it as "Running command · <raw command>". The FE contract must
  // not rely on the BE having already scrubbed unknown rows; it must refuse
  // to render ANY detail for a tool it doesn't recognize, unconditionally.
  const semantic = TOOL_SEMANTIC[normalizedTool(event)];
  if (!semantic) {
    // A tool the FE doesn't recognize (BE didn't canonicalize it, or a parse
    // artifact leaked a non-tool string into `tool`). Show the generic, safe
    // row — never the raw slug, never a fabricated subtext or command detail,
    // regardless of what other fields the event happens to carry (#601,
    // Parker: "unknown 不静默丢" + "unknown 先直接返回 performing_action +
    // 无 subtext/expand"). Gated on `semantic`, NOT `labelKey` — `send_message`
    // is a recognized semantic with no static label (it always renders via
    // the command branch below), and must not be misclassified as unknown.
    return { labelKey: "performing_action", tone: statusTone(event) };
  }

  // Frank's rule (#v0 「不发明新东西」): anything run as a CLI command — bash, and
  // any multica subcommand the daemon canonicalized to a semantic tool
  // (`send_message`, …) — is shown FAITHFULLY as "Running command · <command>",
  // never a product-invented label ("Sending message"). The redacted CLI lives in
  // `entries[].command`; its presence is the signal that this row is a command.
  // Main row = the command wrapped to two lines (CSS line-clamp); click expands
  // to pin the full redacted command + Copy. The dot is raft's amber `running`
  // tone — type-based, so a settled command still reads as a command, never a
  // grey idle row (#404). Native structured tools (read_file/glob/grep) carry
  // no command and keep their real label + real object below.
  const command = fullCommand(event);
  if (command) {
    return {
      labelKey: "running_command",
      subtext: command.length > COMMAND_INLINE_CAP ? command.slice(0, COMMAND_INLINE_CAP) : command,
      subtextKind: "command",
      subtextFull: command,
      tone: "running",
    };
  }

  // A mapped semantic with no static label and no command (e.g. a future
  // command-family alias added to TOOL_SEMANTIC without a matching
  // TOOL_ACTION_KEY entry) still falls back to the generic safe row, never
  // an invented label.
  const labelKey = TOOL_ACTION_KEY[semantic] ?? "performing_action";
  if (labelKey === "performing_action") {
    return { labelKey, tone: statusTone(event) };
  }

  const subtext = toolTarget(event);
  // Classify the subtext so the row renders it correctly (#v0 照实显示). A file
  // tool's target is a PATH (basename-preserving middle-ellipsis). A command
  // tool whose clip arrived without an attached full command still renders as a
  // plain command clip — never the path treatment, which middle-ellipsises on
  // the last `/` and mangles a command that merely contains a slash (the #v0
  // "命令看不全/云里雾里" bug). Everything else is plain text.
  const isPathTool = semantic === "read" || semantic === "write" || semantic === "edit";
  const isCommand = semantic === "command";
  const subtextKind: ActivityPresentation["subtextKind"] = isPathTool
    ? "path"
    : isCommand
      ? "command"
      : "text";
  // A command row (even one whose full command didn't arrive) is amber `running`
  // like every command (#404); other tools keep the status-driven tone.
  return { labelKey, subtext, subtextKind, tone: isCommand ? "running" : statusTone(event) };
}

export function activityPresentation(event: ActivityEvent): ActivityPresentation {
  switch (event.activity_kind) {
    case "thinking":
      return { labelKey: "thinking", subtext: narrativeText(event.text), tone: "neutral" };
    case "text":
      // #441 held-freshness detail row: same label as the status row, plus the
      // structured English detail composed from `details` (waiting tone — Iris:
      // 暖色, not red/blue). Falls through to plain Output for any other text.
      if (event.detail_kind === "send_freshness_hold_detail") {
        // Collapsed row shows only the label (Iris #765: `{time} ● Send held by
        // freshness check`); the target / new-messages / decision facts live in
        // the expanded structured block (see activityExpansionContent).
        return { labelKey: "send_held_by_freshness", tone: "waiting" };
      }
      return { labelKey: "output", subtext: narrativeText(event.text), tone: "neutral" };
    case "tool_call":
      return toolPresentation(event);
    case "turn_end":
    case "session_init":
      return { labelKey: "working", tone: "active" };
    case "compaction_started":
      return { labelKey: "working", subtextKey: "compacting_context", tone: "active" };
    case "compaction_finished":
      return { labelKey: "working", subtextKey: "compaction_finished", tone: "active" };
    case "wake_attempt":
      return { labelKey: "working", subtextKey: "message_received", tone: "active" };
    case "error":
      return {
        labelKey: "failed",
        subtext: narrativeText(event.text) ?? reasonText(event),
        tone: "failure",
      };
    case "blocked":
      // #441 held-freshness status row: canonical label, no subtext (the paired
      // detail `text` row carries the specifics). Any other block → generic wait.
      if (event.detail_kind === "send_freshness_hold") {
        return { labelKey: "send_held_by_freshness", tone: "waiting" };
      }
      return { labelKey: "waiting", subtext: reasonText(event) || narrativeText(event.text), tone: "waiting" };
    case "custom":
      if (event.detail_kind === "agent_status_changed") {
        // Agent presence transition (#411/#525). Raft's status palette: green =
        // idle, yellow = working. We keep it type-based and static (Frank: no
        // pulsing) — idle is a settled neutral row, working an active one. The
        // label IS the state (no subtext). This row also feeds the header/hover
        // latest-state via `projectLatestActivity`, so Idle correctly shows Idle.
        return event.status === "idle"
          ? { labelKey: "idle", tone: "neutral" }
          : { labelKey: "working", tone: "active" };
      }
      if (event.detail_kind === "agent_lifecycle_succeeded") {
        // The old resident runtime has been invalidated, but the replacement is
        // created lazily on the next dispatch. Do not claim it is already online.
        return { labelKey: "restart_prepared", tone: "neutral" };
      }
      if (event.detail_kind.includes("subagent")) {
        // Prefer the daemon's own subagent detail text; fall back to a fixed label.
        return event.text
          ? { labelKey: "working", subtext: narrativeText(event.text), tone: "active" }
          : { labelKey: "working", subtextKey: "subagent_activity", tone: "active" };
      }
      return { labelKey: "working", subtext: narrativeText(event.text), tone: "active" };
    default:
      // Unmapped narrative kind — a neutral working row, never the raw kind string.
      return { labelKey: "working", subtext: narrativeText(event.text), tone: "neutral" };
  }
}

// Building an Intl formatter is slow, so cache one per timezone rather than
// rebuilding on every row render.
const timeFormatters = new Map<string, Intl.DateTimeFormat>();

function timeFormatter(tz: string): Intl.DateTimeFormat {
  let formatter = timeFormatters.get(tz);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat("en-GB", {
      timeZone: tz,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    });
    timeFormatters.set(tz, formatter);
  }
  return formatter;
}

// HH:MM:SS in the viewing timezone (24-hour) — the tabular timestamp Iris's spec
// calls for; matches raft's stream. Pure so it's testable.
export function formatActivityTime(iso: string, tz: string): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return "";
  return timeFormatter(tz).format(ms);
}

// A failure row stays full-strength red until either it's old enough that
// it's clearly history, or something newer has already happened (the agent
// moved on). Below this age it's still "current" even if superseded.
const FAILURE_DECAY_AGE_MS = 15 * 60_000;

/**
 * task #13 (Frank, 2026-08-01): a failure row with no time context and no
 * fading, sitting at the newest end of the timeline hours after it resolved,
 * reads as "broken right now" — Frank hit exactly this. Decay the row's
 * visual weight (same treatment as MergedIdleRow) once it's old or has been
 * superseded by a later event; the label stays "Failed", only the emphasis
 * drops. Pure so it's testable without a clock mock at the call site.
 */
export function isDecayedFailure(
  occurredAt: string,
  hasNewerEvent: boolean,
  nowMs = Date.now(),
): boolean {
  if (hasNewerEvent) return true;
  const ms = Date.parse(occurredAt);
  if (Number.isNaN(ms)) return false;
  return nowMs - ms > FAILURE_DECAY_AGE_MS;
}

/**
 * Compact English relative time for the Activity page header status line
 * (LRM-563). Activity chrome is English-only — do not route through i18n.
 */
export function formatActivityRelativeTime(iso: string, nowMs = Date.now()): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return "";
  const diff = Math.max(0, nowMs - ms);
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
