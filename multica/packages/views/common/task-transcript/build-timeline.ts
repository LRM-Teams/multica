import type { TaskMessagePayload } from "@multica/core/types/events";
import { redactSecrets } from "./redact";

/** A unified timeline entry: tool calls, thinking, text, and errors in chronological order. */
export interface TimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
  created_at?: string;
}

function canMergeStreamingText(prev: TimelineItem, next: TimelineItem): boolean {
  return (prev.type === "thinking" || prev.type === "text") && prev.type === next.type;
}

/** Merge adjacent text/thinking fragments that were split only by daemon flush timing. */
export function coalesceTimelineItems(items: TimelineItem[]): TimelineItem[] {
  const sorted = [...items].sort((a, b) => a.seq - b.seq);
  const out: TimelineItem[] = [];

  for (const item of sorted) {
    const prev = out[out.length - 1];
    if (prev && canMergeStreamingText(prev, item)) {
      out[out.length - 1] = {
        ...prev,
        content: `${prev.content ?? ""}${item.content ?? ""}`,
        created_at: item.created_at ?? prev.created_at,
      };
      continue;
    }
    out.push(item);
  }

  return out;
}

export function appendTimelineItem(items: TimelineItem[], item: TimelineItem): TimelineItem[] {
  return coalesceTimelineItems([...items, item]);
}

function redactTimelineItems(items: TimelineItem[]): TimelineItem[] {
  return items.map((item) => ({
    ...item,
    content: item.content ? redactSecrets(item.content) : item.content,
    output: item.output ? redactSecrets(item.output) : item.output,
  }));
}

function inputKeyCount(input?: Record<string, unknown> | null): number {
  return input && typeof input === "object" ? Object.keys(input).length : 0;
}

/**
 * LRM-689: Cursor often emits tool args only on `completed`. Daemon stores
 * those on `tool_result.input`; copy onto the nearest prior empty `tool_use`
 * of the same tool so the bubble stops showing「此步骤未捕获到参数」.
 */
export function backfillToolUseInputFromResults(items: TimelineItem[]): TimelineItem[] {
  if (items.length === 0) return items;
  const out = items.map((item) => ({ ...item }));
  for (let i = 0; i < out.length; i++) {
    const result = out[i];
    if (!result || result.type !== "tool_result" || inputKeyCount(result.input) === 0) continue;
    for (let j = i - 1; j >= 0; j--) {
      const use = out[j];
      if (!use || use.type !== "tool_use") continue;
      if (result.tool && use.tool && result.tool !== use.tool) continue;
      if (inputKeyCount(use.input) > 0) break;
      out[j] = { ...use, input: { ...(result.input as Record<string, unknown>) } };
      break;
    }
  }
  return out;
}

/** Build a chronologically ordered timeline from raw task messages. */
export function buildTimeline(msgs: TaskMessagePayload[]): TimelineItem[] {
  const items: TimelineItem[] = [];
  for (const msg of msgs) {
    // The daemon's empty thinking message is a phase-status wire for the
    // running-status pill. It intentionally has no transcript representation:
    // raw thinking is diagnostic-only, and a status transition is not a step.
    if (msg.type === "thinking" && !(msg.content ?? "").trim()) continue;
    items.push({
      seq: msg.seq,
      type: msg.type,
      tool: msg.tool,
      content: msg.content,
      input: msg.input,
      output: msg.output,
      created_at: msg.created_at,
    });
  }
  return redactTimelineItems(backfillToolUseInputFromResults(coalesceTimelineItems(items)));
}
