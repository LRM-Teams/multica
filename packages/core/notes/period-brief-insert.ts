import type { MessagePart, NotePeriodBriefInsertMode } from "../types";

const PERIOD_BRIEF_INSERT_PROPOSE_FENCE_RE =
  /<period_brief_insert_propose\b([^>]*)\/?>\s*(?:<\/period_brief_insert_propose>)?/gi;

export const PERIOD_BRIEF_INSERT_PROPOSE_FENCE_STRIP_RE =
  /<period_brief_insert_propose\b[^>]*\/?>\s*(?:<\/period_brief_insert_propose>)?/gi;

export type PeriodBriefInsertPropose = {
  runId: string;
  targetPageId: string;
  mode: NotePeriodBriefInsertMode;
  label?: string;
};

function fenceAttr(attrs: string, name: string): string {
  const matched = attrs.match(new RegExp(`(?:^|\\s)${name}\\s*=\\s*["']([^"']+)["']`, "i"));
  return matched?.[1]?.trim() ?? "";
}

function proposeFromPart(
  part: Extract<MessagePart, { type: "period_brief_insert_propose" }> & {
    selected_option_id?: string;
  },
): PeriodBriefInsertPropose | null {
  const runId = part.ref_id.trim();
  const targetPageId = part.target_page_id.trim();
  if (!runId || !targetPageId) return null;
  const modeRaw = (part.mode || part.selected_option_id || "").trim();
  if (modeRaw !== "append" && modeRaw !== "child") return null;
  const label = part.label?.trim();
  return { runId, targetPageId, mode: modeRaw, ...(label ? { label } : {}) };
}

export function parsePeriodBriefInsertPropose(input: {
  content?: string;
  parts?: ReadonlyArray<
    Pick<MessagePart, "type"> & Partial<MessagePart> & { selected_option_id?: string }
  > | null;
}): PeriodBriefInsertPropose | null {
  for (const part of input.parts ?? []) {
    if (part.type !== "period_brief_insert_propose") continue;
    const parsed = proposeFromPart(part as Extract<MessagePart, { type: "period_brief_insert_propose" }>);
    if (parsed) return parsed;
  }
  PERIOD_BRIEF_INSERT_PROPOSE_FENCE_RE.lastIndex = 0;
  const match = PERIOD_BRIEF_INSERT_PROPOSE_FENCE_RE.exec(input.content ?? "");
  if (!match) return null;
  const attrs = match[1] ?? "";
  const runId = fenceAttr(attrs, "run");
  const targetPageId = fenceAttr(attrs, "page") || fenceAttr(attrs, "target");
  const modeRaw = fenceAttr(attrs, "mode").toLowerCase();
  const mode: NotePeriodBriefInsertMode | "" =
    modeRaw === "append" || modeRaw === "child" ? modeRaw : "";
  if (!runId || !targetPageId || !mode) return null;
  const label = fenceAttr(attrs, "label");
  return { runId, targetPageId, mode, ...(label ? { label } : {}) };
}

export function stripPeriodBriefInsertProposeMarkers(text: string): string {
  return text.replace(PERIOD_BRIEF_INSERT_PROPOSE_FENCE_STRIP_RE, "");
}
