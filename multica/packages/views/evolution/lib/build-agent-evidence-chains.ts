import type { Agent, EvolutionReviewSubmission, EvolutionUnitMetric } from "@multica/core/types";

export type EvidenceNodeKind = "write" | "promote" | "used" | "pending_use";

export type EvidenceNode = {
  id: string;
  kind: EvidenceNodeKind;
  title: string;
  summary: string;
  meta: string;
  at: string | null;
  eventType?: string;
  experienceKind: string;
  scope?: string;
  confidence?: string | number | null;
  decision?: string;
  sourceRef?: string;
  taskRef?: string;
  submissionId?: string;
  unitId?: string | null;
  pending?: boolean;
};

export type EvidenceChain = {
  experienceId: string;
  title: string;
  kind: string;
  nodes: EvidenceNode[];
  complete: boolean;
  latestAt: number;
};

export type AgentEvidenceSummary = {
  agent: Agent;
  writes: number;
  promoted: number;
  used: number;
  pendingPromote: number;
  chainComplete: boolean;
  chains: EvidenceChain[];
  latestActivityAt: number;
  subtitleKey:
    | "evidenceRecentUsed"
    | "evidencePendingPromote"
    | "evidenceNoUseYet"
    | "evidenceNoWrites";
  subtitleAt: string | null;
  subtitleKind: string | null;
};

function normalizeUnitType(value: string | null | undefined): string {
  const lower = (value ?? "").toLowerCase();
  if (lower.includes("skill")) return "skill";
  if (lower.includes("memory")) return "memory";
  if (lower.includes("preference")) return "preference";
  if (lower.includes("workflow") || lower.includes("tool")) return "workflow";
  return value || "memory";
}

function timestamp(value: string | null | undefined): number {
  if (!value) return 0;
  const t = Date.parse(value);
  return Number.isFinite(t) ? t : 0;
}

function experienceKey(submission: EvolutionReviewSubmission): string {
  return (
    submission.promoted_unit_id ||
    submission.local_unit_id ||
    submission.id
  );
}

function findUnitMetric(
  metrics: EvolutionUnitMetric[],
  submission: EvolutionReviewSubmission,
): EvolutionUnitMetric | undefined {
  const keys = [submission.promoted_unit_id, submission.local_unit_id, submission.id].filter(
    Boolean,
  ) as string[];
  return metrics.find(
    (m) =>
      keys.includes(m.unit_id ?? "") ||
      keys.includes(m.local_unit_id) ||
      (m.title &&
        submission.title &&
        m.title.trim().toLowerCase() === submission.title.trim().toLowerCase()),
  );
}

function buildChain(
  experienceId: string,
  submissions: EvolutionReviewSubmission[],
  metrics: EvolutionUnitMetric[],
): EvidenceChain {
  const primary =
    submissions.find((s) => s.status === "promoted") ??
    submissions.find((s) => s.status === "needs_review") ??
    submissions[0]!;
  const kind = normalizeUnitType(primary.unit_type);
  const title = primary.title || primary.summary || experienceId.slice(0, 8);
  const writeAt = primary.source_created_at ?? primary.created_at ?? null;
  const sourceRef =
    primary.evidence?.source ||
    primary.evidence?.evidence_refs?.[0] ||
    primary.bundle_ref ||
    undefined;

  const nodes: EvidenceNode[] = [
    {
      id: `${experienceId}:write`,
      kind: "write",
      title: "Local proposal written",
      summary: primary.summary || primary.content || title,
      meta: [primary.evidence?.source_date, sourceRef].filter(Boolean).join(" · "),
      at: writeAt,
      experienceKind: kind,
      sourceRef,
      submissionId: primary.id,
      unitId: primary.local_unit_id,
      eventType: `${kind}.written`,
    },
  ];

  const promoted = submissions.find((s) => s.status === "promoted");
  const rejected = submissions.find((s) => s.status === "rejected");
  if (promoted) {
    nodes.push({
      id: `${experienceId}:promote`,
      kind: "promote",
      title: "Curation promote / enable",
      summary: `Promoted as ${kind === "skill" ? "Skill" : "Memory"} · scope: ${promoted.suggested_scope || "agent-private"}${
        promoted.review_confidence != null ? ` · confidence ${promoted.review_confidence}` : ""
      }`,
      meta: ["curator", promoted.review_decision || "approve", promoted.reviewed_at ?? ""]
        .filter(Boolean)
        .join(" · "),
      at: promoted.reviewed_at ?? promoted.updated_at ?? null,
      experienceKind: kind,
      scope: promoted.suggested_scope,
      confidence: promoted.review_confidence ?? promoted.confidence,
      decision: "approved",
      submissionId: promoted.id,
      unitId: promoted.promoted_unit_id ?? promoted.local_unit_id,
      eventType: `${kind}.promoted`,
    });
  } else if (rejected) {
    nodes.push({
      id: `${experienceId}:promote`,
      kind: "promote",
      title: "Not promoted",
      summary: rejected.review_reason || rejected.reject_reason || "Rejected by curation",
      meta: ["curator", "rejected", rejected.reviewed_at ?? ""].filter(Boolean).join(" · "),
      at: rejected.reviewed_at ?? rejected.updated_at ?? null,
      experienceKind: kind,
      decision: "rejected",
      submissionId: rejected.id,
      unitId: rejected.local_unit_id,
      eventType: `${kind}.rejected`,
    });
  }

  const metric = findUnitMetric(metrics, primary);
  const usedCount = metric?.used_count ?? 0;
  if (usedCount > 0) {
    nodes.push({
      id: `${experienceId}:used`,
      kind: "used",
      title: "Used in later tasks",
      summary: `Cited in later tasks (not merely on disk) · used×${usedCount}`,
      meta: ["task run", "cite event", metric?.last_used_at ?? ""].filter(Boolean).join(" · "),
      at: metric?.last_used_at ?? null,
      experienceKind: kind,
      scope: primary.suggested_scope,
      taskRef: metric?.unit_id ?? metric?.local_unit_id,
      unitId: metric?.unit_id ?? metric?.local_unit_id,
      eventType: kind === "skill" ? "skill.invoked" : "memory.cited_in_task",
      submissionId: primary.id,
    });
  } else if (promoted) {
    nodes.push({
      id: `${experienceId}:pending_use`,
      kind: "pending_use",
      title: "Awaiting use",
      summary: "Promoted but not yet cited in a later task",
      meta: "pending",
      at: null,
      experienceKind: kind,
      pending: true,
      submissionId: promoted.id,
      unitId: promoted.promoted_unit_id ?? promoted.local_unit_id,
    });
  }

  const complete = Boolean(
    nodes.some((n) => n.kind === "write") &&
      nodes.some((n) => n.kind === "promote" && n.decision === "approved") &&
      nodes.some((n) => n.kind === "used"),
  );
  const latestAt = Math.max(0, ...nodes.map((n) => timestamp(n.at)));

  return { experienceId, title, kind, nodes, complete, latestAt };
}

/**
 * Compose per-agent evidence chains from evolution submissions + unit usage
 * metrics (LRM-986 / 983 冻稿). Soft-empty when an agent has no writes.
 */
export function buildAgentEvidenceSummaries(
  agents: Agent[],
  submissions: EvolutionReviewSubmission[],
  unitMetrics: EvolutionUnitMetric[],
): AgentEvidenceSummary[] {
  const byAgent = new Map<string, EvolutionReviewSubmission[]>();
  for (const submission of submissions) {
    const list = byAgent.get(submission.source_agent_id) ?? [];
    list.push(submission);
    byAgent.set(submission.source_agent_id, list);
  }

  return agents
    .map((agent) => {
      const agentSubs = byAgent.get(agent.id) ?? [];
      const byExperience = new Map<string, EvolutionReviewSubmission[]>();
      for (const submission of agentSubs) {
        const key = experienceKey(submission);
        const list = byExperience.get(key) ?? [];
        list.push(submission);
        byExperience.set(key, list);
      }

      const chains = [...byExperience.entries()]
        .map(([id, group]) => buildChain(id, group, unitMetrics))
        .toSorted((a, b) => b.latestAt - a.latestAt);

      const writes = chains.length;
      const promoted = chains.filter((c) =>
        c.nodes.some((n) => n.kind === "promote" && n.decision === "approved"),
      ).length;
      const used = chains.filter((c) => c.nodes.some((n) => n.kind === "used")).length;
      const pendingPromote = chains.filter(
        (c) =>
          !c.nodes.some((n) => n.kind === "promote") &&
          agentSubs.some((s) => ["candidate", "needs_review"].includes(s.status)),
      ).length;
      const chainComplete = chains.some((c) => c.complete);
      const latestActivityAt = Math.max(0, ...chains.map((c) => c.latestAt));

      let subtitleKey: AgentEvidenceSummary["subtitleKey"] = "evidenceNoWrites";
      let subtitleAt: string | null = null;
      let subtitleKind: string | null = null;
      const latestUsed = chains.find((c) => c.nodes.some((n) => n.kind === "used"));
      if (latestUsed) {
        subtitleKey = "evidenceRecentUsed";
        const usedNode = latestUsed.nodes.find((n) => n.kind === "used");
        subtitleAt = usedNode?.at ?? null;
        subtitleKind = latestUsed.kind;
      } else if (pendingPromote > 0) {
        subtitleKey = "evidencePendingPromote";
      } else if (writes > 0) {
        subtitleKey = "evidenceNoUseYet";
      }

      return {
        agent,
        writes,
        promoted,
        used,
        pendingPromote,
        chainComplete,
        chains,
        latestActivityAt,
        subtitleKey,
        subtitleAt,
        subtitleKind,
      };
    })
    .toSorted(
      (a, b) =>
        b.latestActivityAt - a.latestActivityAt ||
        Number(b.chainComplete) - Number(a.chainComplete) ||
        b.writes - a.writes,
    );
}

export type EvidenceFilter = "all" | "write" | "promote" | "used";

export function filterChains(
  chains: EvidenceChain[],
  filter: EvidenceFilter,
): EvidenceChain[] {
  if (filter === "all") return chains;
  if (filter === "write") return chains;
  if (filter === "promote") {
    return chains.filter((c) =>
      c.nodes.some((n) => n.kind === "promote" && n.decision === "approved"),
    );
  }
  return chains.filter((c) => c.nodes.some((n) => n.kind === "used"));
}
