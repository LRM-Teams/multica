import type { TypedGraphNode } from "@multica/core/research";
import type { ResearchMessage } from "@multica/core/types";

export type GoalVersionEntry = {
  version: number;
  goal: string;
  reason: string | null;
  createdAt: string | null;
  isCurrent: boolean;
};

function metaRecord(meta: unknown): Record<string, unknown> | null {
  if (!meta || typeof meta !== "object") return null;
  return meta as Record<string, unknown>;
}

function metaString(meta: unknown, key: string): string | null {
  const value = metaRecord(meta)?.[key];
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function metaNumber(meta: unknown, key: string): number | null {
  const value = metaRecord(meta)?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

export function countNodesByGoalVersionId(
  nodes: readonly TypedGraphNode[],
): Map<string, number> {
  const counts = new Map<string, number>();
  for (const node of nodes) {
    const key = node.goal_version_id?.trim();
    if (!key) continue;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return counts;
}

/**
 * Build read-only goal version history from process messages and the current run
 * contract. Never fabricates versions — only surfaces server-emitted facts.
 */
export function buildGoalVersionHistory(input: {
  currentGoal: string;
  currentVersion: number | null | undefined;
  messages: readonly ResearchMessage[];
}): GoalVersionEntry[] {
  const currentVersion =
    typeof input.currentVersion === "number" && Number.isFinite(input.currentVersion)
      ? input.currentVersion
      : null;

  const byVersion = new Map<number, GoalVersionEntry>();

  for (const message of input.messages) {
    if (message.card_kind !== "process") continue;
    const op = metaString(message.meta, "op");
    if (op !== "goal_steered") continue;
    const version = metaNumber(message.meta, "goal_version");
    const goal = metaString(message.meta, "goal") || message.body.trim();
    if (version == null || !goal) continue;
    byVersion.set(version, {
      version,
      goal,
      reason: metaString(message.meta, "reason"),
      createdAt: message.created_at || null,
      isCurrent: false,
    });
  }

  if (currentVersion != null) {
    const existing = byVersion.get(currentVersion);
    byVersion.set(currentVersion, {
      version: currentVersion,
      goal: input.currentGoal.trim() || existing?.goal || "",
      reason: existing?.reason ?? null,
      createdAt: existing?.createdAt ?? null,
      isCurrent: true,
    });
  } else if (input.currentGoal.trim()) {
    byVersion.set(0, {
      version: 0,
      goal: input.currentGoal.trim(),
      reason: null,
      createdAt: null,
      isCurrent: true,
    });
  }

  return [...byVersion.values()].sort((a, b) => b.version - a.version);
}
