import { projectRunnerActivitySummary } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "./is-compact-activity-label";

type SummaryItem = {
  agent_id: string;
  summary: { label: string; tone: string; visibility: string };
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
  tone: string;
};

const TONE_RANK: Record<string, number> = {
  active: 0,
  info: 1,
  warning: 2,
  error: 3,
  success: 4,
};

const PRESENCE_ONLY_LABELS = new Set(["Online", "Offline", "Idle", "Working"]);

function toneRank(tone: string): number {
  return TONE_RANK[tone] ?? 5;
}

function labelBase(label: string): string {
  return label.replace(/[.…]+$/u, "").trim();
}

/** Presence words belong on the avatar, not the composer strip. */
export function isComposerPresenceOnlyLabel(label: string): boolean {
  return PRESENCE_ONLY_LABELS.has(labelBase(label));
}

/**
 * Compact composer Activity rows for the given conversation agents.
 * Visible compact verbs only; live tones (active/info) sort first.
 */
export function selectComposerAgentActivityRows(
  agents: readonly ComposerActivityAgent[],
  items: readonly SummaryItem[] | undefined,
): ComposerActivityRow[] {
  if (agents.length === 0 || !items || items.length === 0) return [];

  const byId = new Map(items.map((item) => [item.agent_id, item.summary]));
  const rows: ComposerActivityRow[] = [];
  for (const agent of agents) {
    const summary = byId.get(agent.agentId);
    const projection = projectRunnerActivitySummary(summary);
    if (!projection || !isCompactActivityLabel(projection.label)) continue;
    rows.push({
      agentId: agent.agentId,
      name: agent.name.trim(),
      label: projection.label,
      dotClass: projection.dotClass,
      tone: summary?.tone ?? "",
    });
  }

  // Group rosters: only currently-live verbs. A 1:1 strip keeps the same
  // compact set as before (Online may still render for a single peer).
  const visible =
    agents.length > 1 ? rows.filter((row) => !isComposerPresenceOnlyLabel(row.label)) : rows;

  visible.sort((a, b) => {
    const rank = toneRank(a.tone) - toneRank(b.tone);
    if (rank !== 0) return rank;
    return a.name.localeCompare(b.name) || a.agentId.localeCompare(b.agentId);
  });
  return visible;
}
