import { projectRunnerActivitySummary } from "../../agents/use-agent-live-status";
import { runnerActivityVisuals } from "../../agents/runner-activity-visuals";
import { isCompactActivityLabel } from "./is-compact-activity-label";

type SummaryItem = {
  agent_id: string;
  summary: { label: string; activityKind: string; detailKind: string };
};

export type ComposerActivityAgent = {
  agentId: string;
  name: string;
};

export type ComposerActivityRow = {
  agentId: string;
  name: string;
  label: string;
  dotClass: string;
  rank: number;
};

const PRESENCE_ONLY_LABELS = new Set(["Online", "Offline", "Idle", "Working"]);

function labelBase(label: string): string {
  return label.replace(/[.…]+$/u, "").trim();
}

/** Presence words belong on the avatar, not the composer strip. */
export function isComposerPresenceOnlyLabel(label: string): boolean {
  return PRESENCE_ONLY_LABELS.has(labelBase(label));
}

/**
 * Compact composer Activity rows for the given conversation agents.
 * Visible compact verbs only; active Activity facts sort first.
 *
 * A single-agent conversation carries no names: the one peer is unambiguous,
 * so its verb reads as "Thinking..." rather than "Peer Thinking...".
 */
export function selectComposerAgentActivityRows(
  agents: readonly ComposerActivityAgent[],
  items: readonly SummaryItem[] | undefined,
): ComposerActivityRow[] {
  if (agents.length === 0 || !items || items.length === 0) return [];

  const byId = new Map(items.map((item) => [item.agent_id, item.summary]));
  const named = agents.length > 1;
  const rows: ComposerActivityRow[] = [];
  for (const agent of agents) {
    const summary = byId.get(agent.agentId);
    const projection = projectRunnerActivitySummary(summary);
    if (!projection || !isCompactActivityLabel(projection.label)) continue;
    rows.push({
      agentId: agent.agentId,
      name: named ? agent.name.trim() : "",
      label: projection.label,
      dotClass: projection.dotClass,
      rank: runnerActivityVisuals({ activity_kind: summary!.activityKind, detail_kind: summary!.detailKind }).rank,
    });
  }

  // Presence words belong on the avatar, not the composer strip — including
  // 1:1 DMs. Showing Online while a collector is mid-task reads as idle.
  const visible = rows.filter((row) => !isComposerPresenceOnlyLabel(row.label));

  visible.sort((a, b) => {
    const rank = a.rank - b.rank;
    if (rank !== 0) return rank;
    return a.name.localeCompare(b.name) || a.agentId.localeCompare(b.agentId);
  });
  return visible;
}

/** One rendered line of the strip: the agents currently on the same verb. */
export type ComposerActivityLine = {
  key: string;
  label: string;
  dotClass: string;
  /** Empty in a single-agent conversation, where a name adds nothing. */
  names: string[];
  hiddenNameCount: number;
};

/** Lines the strip renders before collapsing the rest into a "+N" tail. */
export const COMPOSER_ACTIVITY_MAX_LINES = 2;
/** Names shown inside one line before collapsing to "+N". */
export const COMPOSER_ACTIVITY_MAX_NAMES = 2;

type LineDraft = Omit<ComposerActivityLine, "hiddenNameCount"> & {
  rank: number;
  agentCount: number;
};

/**
 * Collapse per-agent rows into one line per verb, then cap the lines.
 *
 * A busy channel used to stack one row per agent (8 agents = 8 lines pushing
 * the conversation up). Same-verb agents share a line, and everything past the
 * cap becomes a single "+N more agents" tail, so the strip never grows past
 * COMPOSER_ACTIVITY_MAX_LINES + 1 lines.
 *
 * Expects fact-priority-sorted rows (selectComposerAgentActivityRows output) — a line
 * takes its dot from the first member it sees.
 */
export function groupComposerAgentActivityRows(
  rows: readonly ComposerActivityRow[],
): { lines: ComposerActivityLine[]; hiddenAgentCount: number } {
  const byVerb = new Map<string, LineDraft>();
  for (const row of rows) {
    const key = labelBase(row.label);
    const draft = byVerb.get(key);
    if (draft) {
      draft.agentCount += 1;
      if (row.name) draft.names.push(row.name);
      continue;
    }
    byVerb.set(key, {
      key,
      label: row.label,
      dotClass: row.dotClass,
      names: row.name ? [row.name] : [],
      rank: row.rank,
      agentCount: 1,
    });
  }

  // Most active fact first, then the verb the most agents share — line order must
  // not hinge on which member happens to sort first alphabetically.
  const drafts = [...byVerb.values()].sort((a, b) => {
    const rank = a.rank - b.rank;
    if (rank !== 0) return rank;
    return b.agentCount - a.agentCount || a.key.localeCompare(b.key);
  });

  const lines = drafts
    .slice(0, COMPOSER_ACTIVITY_MAX_LINES)
    .map(({ key, label, dotClass, names }) => ({
      key,
      label,
      dotClass,
      names: names.slice(0, COMPOSER_ACTIVITY_MAX_NAMES),
      hiddenNameCount: Math.max(0, names.length - COMPOSER_ACTIVITY_MAX_NAMES),
    }));
  const hiddenAgentCount = drafts
    .slice(COMPOSER_ACTIVITY_MAX_LINES)
    .reduce((count, draft) => count + draft.agentCount, 0);

  return { lines, hiddenAgentCount };
}
