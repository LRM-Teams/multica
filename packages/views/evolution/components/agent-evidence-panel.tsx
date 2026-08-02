"use client";

import { useMemo, useState } from "react";
import { Link2, ChevronLeft } from "lucide-react";
import type { Agent, EvolutionReviewSubmission, EvolutionUnitMetric } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { AppLink } from "../../navigation/app-link";
import {
  buildAgentEvidenceSummaries,
  filterChains,
  type AgentEvidenceSummary,
  type EvidenceFilter,
  type EvidenceNode,
} from "../lib/build-agent-evidence-chains";
import { useEvolutionCopy } from "./evolution-center-page";

type NarrowStep = "list" | "agent" | "node";

function relativeShort(iso: string | null, copy: ReturnType<typeof useEvolutionCopy>): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const delta = Date.now() - t;
  if (delta < 60_000) return copy("evidenceJustNow");
  if (delta < 3_600_000) return `${Math.max(1, Math.round(delta / 60_000))}m`;
  if (delta < 86_400_000) return `${Math.max(1, Math.round(delta / 3_600_000))}h`;
  return `${Math.max(1, Math.round(delta / 86_400_000))}d`;
}

function nodeTone(kind: EvidenceNode["kind"]): string {
  if (kind === "write") return "bg-sky-500";
  if (kind === "promote") return "bg-emerald-500";
  if (kind === "used") return "bg-violet-500";
  return "bg-muted-foreground/40 border border-dashed border-muted-foreground/50";
}

function nodeLabel(kind: EvidenceNode["kind"], copy: ReturnType<typeof useEvolutionCopy>): string {
  if (kind === "write") return copy("evidenceNodeWrite");
  if (kind === "promote") return copy("evidenceNodePromote");
  if (kind === "used") return copy("evidenceNodeUsed");
  return copy("evidenceNodePendingUse");
}

function subtitleFor(
  summary: AgentEvidenceSummary,
  copy: ReturnType<typeof useEvolutionCopy>,
): string {
  if (summary.subtitleKey === "evidenceRecentUsed") {
    const when = relativeShort(summary.subtitleAt, copy);
    const kind =
      summary.subtitleKind === "skill"
        ? copy("skill")
        : summary.subtitleKind === "memory"
          ? copy("memory")
          : summary.subtitleKind || "";
    return [copy("evidenceRecentUsed"), when, kind].filter(Boolean).join(" · ");
  }
  if (summary.subtitleKey === "evidencePendingPromote") {
    return copy("evidencePendingPromote").replace(
      "{count}",
      String(summary.pendingPromote || 1),
    );
  }
  if (summary.subtitleKey === "evidenceNoUseYet") return copy("evidenceNoUseYet");
  return copy("evidenceNoWrites");
}

export function AgentEvidencePanel({
  agents,
  submissions,
  unitMetrics,
  loading,
  onOpenLearning,
}: {
  agents: Agent[];
  submissions: EvolutionReviewSubmission[];
  unitMetrics: EvolutionUnitMetric[];
  loading: boolean;
  onOpenLearning: () => void;
}) {
  const copy = useEvolutionCopy();
  const paths = useWorkspacePaths();
  const summaries = useMemo(
    () => buildAgentEvidenceSummaries(agents, submissions, unitMetrics),
    [agents, submissions, unitMetrics],
  );
  // User picks only — fall back to first agent / default node while rendering
  // (no prop→state sync effects; react-doctor no-derived-state / no-effect-chain).
  const [agentIdOverride, setAgentIdOverride] = useState<string | null>(null);
  const [filter, setFilter] = useState<EvidenceFilter>("all");
  const [nodeIdOverride, setNodeIdOverride] = useState<string | null>(null);
  const [narrowStep, setNarrowStep] = useState<NarrowStep>("list");

  const selectedAgentId =
    agentIdOverride && summaries.some((s) => s.agent.id === agentIdOverride)
      ? agentIdOverride
      : (summaries[0]?.agent.id ?? null);
  const selected = summaries.find((s) => s.agent.id === selectedAgentId) ?? null;
  const visibleChains = useMemo(
    () => (selected ? filterChains(selected.chains, filter) : []),
    [filter, selected],
  );
  const defaultNodeId = useMemo(() => {
    const firstUsed = visibleChains
      .flatMap((c) => c.nodes)
      .find((n) => n.kind === "used" && !n.pending);
    return firstUsed?.id ?? visibleChains[0]?.nodes.at(-1)?.id ?? null;
  }, [visibleChains]);
  const selectedNodeId =
    nodeIdOverride &&
    visibleChains.some((c) => c.nodes.some((n) => n.id === nodeIdOverride))
      ? nodeIdOverride
      : defaultNodeId;

  const selectedNode = visibleChains
    .flatMap((c) => c.nodes.map((n) => ({ chain: c, node: n })))
    .find((x) => x.node.id === selectedNodeId);

  const selectAgent = (id: string) => {
    setAgentIdOverride(id);
    setFilter("all");
    setNodeIdOverride(null);
  };
  const selectFilter = (next: EvidenceFilter) => {
    setFilter(next);
    setNodeIdOverride(null);
  };

  if (loading) {
    return (
      <div className="grid gap-3 lg:grid-cols-[240px_minmax(0,1fr)_280px]" data-testid="evidence-loading">
        <Card className="h-64 animate-pulse bg-muted/40" />
        <Card className="h-64 animate-pulse bg-muted/40" />
        <Card className="hidden h-64 animate-pulse bg-muted/40 lg:block" />
      </div>
    );
  }

  if (agents.length === 0) {
    return (
      <Card>
        <CardContent className="py-10 text-center text-sm text-muted-foreground">
          {copy("noAgents")}
        </CardContent>
      </Card>
    );
  }

  return (
    <>
      {/* Desktop / tablet: list + chain + detail */}
      <div
        className="hidden gap-3 md:grid md:grid-cols-[220px_minmax(0,1fr)] xl:grid-cols-[240px_minmax(0,1fr)_300px]"
        data-testid="agent-evidence-desktop"
      >
        <AgentListColumn
          summaries={summaries}
          selectedAgentId={selectedAgentId}
          onSelect={selectAgent}
        />
        <AgentChainColumn
          selected={selected}
          filter={filter}
          onFilter={selectFilter}
          chains={visibleChains}
          selectedNodeId={selectedNodeId}
          onSelectNode={setNodeIdOverride}
          onOpenLearning={onOpenLearning}
        />
        <div className="hidden xl:block">
          <NodeDetailCard
            selected={selected}
            pair={selectedNode}
            agentPath={selected ? paths.agentDetail(selected.agent.id) : undefined}
          />
        </div>
      </div>

      {/* Narrow: progressive */}
      <div className="md:hidden" data-testid="agent-evidence-narrow">
        {narrowStep === "list" && (
          <AgentListColumn
            summaries={summaries}
            selectedAgentId={selectedAgentId}
            onSelect={(id) => {
              selectAgent(id);
              setNarrowStep("agent");
            }}
          />
        )}
        {narrowStep === "agent" && selected && (
          <div className="space-y-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="gap-1 px-0"
              onClick={() => setNarrowStep("list")}
            >
              <ChevronLeft className="size-4" />
              {selected.agent.display_name || selected.agent.name}
            </Button>
            <AgentChainColumn
              selected={selected}
              filter={filter}
              onFilter={selectFilter}
              chains={visibleChains}
              selectedNodeId={selectedNodeId}
              onSelectNode={(id) => {
                setNodeIdOverride(id);
                setNarrowStep("node");
              }}
              onOpenLearning={onOpenLearning}
            />
          </div>
        )}
        {narrowStep === "node" && selected && (
          <div className="space-y-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="gap-1 px-0"
              onClick={() => setNarrowStep("agent")}
            >
              <ChevronLeft className="size-4" />
              {copy("evidenceNodeDetail")}
            </Button>
            <NodeDetailCard
              selected={selected}
              pair={selectedNode}
              agentPath={paths.agentDetail(selected.agent.id)}
            />
          </div>
        )}
      </div>
    </>
  );
}

function AgentListColumn({
  summaries,
  selectedAgentId,
  onSelect,
}: {
  summaries: AgentEvidenceSummary[];
  selectedAgentId: string | null;
  onSelect: (id: string) => void;
}) {
  const copy = useEvolutionCopy();
  return (
    <Card className="bg-background/85 backdrop-blur" data-testid="evidence-agent-list">
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">{copy("evidenceWorkspaceAgents")}</CardTitle>
        <p className="text-xs text-muted-foreground">{copy("evidenceSortHint")}</p>
      </CardHeader>
      <CardContent className="space-y-1.5">
        {summaries.map((summary) => {
          const active = summary.agent.id === selectedAgentId;
          return (
            <button
              key={summary.agent.id}
              type="button"
              onClick={() => onSelect(summary.agent.id)}
              className={cn(
                "flex w-full items-start gap-2.5 rounded-xl border px-2.5 py-2 text-left transition-colors",
                active
                  ? "border-brand/40 bg-brand/10"
                  : "border-transparent hover:bg-muted/50",
              )}
              data-testid={`evidence-agent-${summary.agent.id}`}
            >
              <ActorAvatar
                actorType="agent"
                actorId={summary.agent.id}
                size={32}
                showStatusDot
              />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="truncate text-sm font-medium">
                    {summary.agent.display_name || summary.agent.name}
                  </span>
                  {summary.chainComplete && (
                    <Badge variant="secondary" className="text-[10px]">
                      {copy("evidenceChainComplete")}
                    </Badge>
                  )}
                </div>
                <div className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">
                  {subtitleFor(summary, copy)}
                </div>
              </div>
            </button>
          );
        })}
      </CardContent>
    </Card>
  );
}

function AgentChainColumn({
  selected,
  filter,
  onFilter,
  chains,
  selectedNodeId,
  onSelectNode,
  onOpenLearning,
}: {
  selected: AgentEvidenceSummary | null;
  filter: EvidenceFilter;
  onFilter: (f: EvidenceFilter) => void;
  chains: ReturnType<typeof filterChains>;
  selectedNodeId: string | null;
  onSelectNode: (id: string) => void;
  onOpenLearning: () => void;
}) {
  const copy = useEvolutionCopy();
  if (!selected) return null;

  const empty = selected.writes === 0;

  return (
    <Card className="bg-background/85 backdrop-blur" data-testid="evidence-chain-column">
      <CardHeader className="space-y-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="text-base">
              {selected.agent.display_name || selected.agent.name}
            </CardTitle>
            <Badge variant="outline">{copy("evidenceIndividualBadge")}</Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">{copy("evidenceIndividualHint")}</p>
        </div>
        {!empty && (
          <div className="grid grid-cols-3 gap-2">
            {(
              [
                ["write", selected.writes, copy("evidenceStatWrite"), copy("evidenceStatWriteHint")],
                [
                  "promote",
                  selected.promoted,
                  copy("evidenceStatPromoted"),
                  copy("evidenceStatPromotedHint"),
                ],
                ["used", selected.used, copy("evidenceStatUsed"), copy("evidenceStatUsedHint")],
              ] as const
            ).map(([key, value, label, hint]) => (
              <button
                key={key}
                type="button"
                onClick={() => onFilter(key)}
                className={cn(
                  "rounded-2xl border px-3 py-2.5 text-left transition-colors",
                  filter === key
                    ? "border-brand/40 bg-brand/10"
                    : "border-border/60 bg-muted/20 hover:bg-muted/40",
                )}
                data-testid={`evidence-stat-${key}`}
              >
                <div className="text-[11px] text-muted-foreground">{label}</div>
                <div className="mt-0.5 text-2xl font-semibold tabular-nums">{value}</div>
                <div className="mt-0.5 text-[10px] leading-snug text-muted-foreground">{hint}</div>
              </button>
            ))}
          </div>
        )}
      </CardHeader>
      <CardContent>
        {empty ? (
          <div
            className="flex flex-col items-center rounded-2xl border border-dashed bg-muted/20 px-6 py-10 text-center"
            data-testid="evidence-empty"
          >
            <div className="mb-3 flex size-12 items-center justify-center rounded-full bg-muted">
              <Link2 className="size-5 text-muted-foreground" />
            </div>
            <div className="text-sm font-medium">{copy("evidenceEmptyTitle")}</div>
            <p className="mt-2 max-w-sm text-sm text-muted-foreground">
              {copy("evidenceEmptyBody")}
            </p>
            <Button type="button" variant="secondary" className="mt-4" onClick={onOpenLearning}>
              {copy("evidenceEmptyCta")}
            </Button>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="flex items-center justify-between gap-2">
              <div className="text-sm font-medium">{copy("evidenceChainTitle")}</div>
              <div className="text-[11px] text-muted-foreground">
                {chains.length} · {filter === "all" ? copy("all") : nodeLabel(filter === "promote" ? "promote" : filter === "used" ? "used" : "write", copy)}
              </div>
            </div>
            {chains.length === 0 ? (
              <p className="text-sm text-muted-foreground">{copy("evidenceFilterEmpty")}</p>
            ) : (
              chains.map((chain) => (
                <div key={chain.experienceId} className="space-y-2">
                  <div className="text-xs font-medium text-muted-foreground">{chain.title}</div>
                  <ol className="relative space-y-3 border-s border-border/70 ps-4">
                    {chain.nodes.map((node) => {
                      const active = node.id === selectedNodeId;
                      return (
                        <li key={node.id} className="relative">
                          <span
                            className={cn(
                              "absolute -start-[1.3rem] top-2 size-2.5 rounded-full",
                              nodeTone(node.kind),
                              node.pending && "bg-transparent",
                            )}
                          />
                          <button
                            type="button"
                            onClick={() => onSelectNode(node.id)}
                            className={cn(
                              "w-full rounded-xl border px-3 py-2.5 text-left transition-colors",
                              active
                                ? "border-brand/50 bg-brand/10"
                                : "border-border/50 bg-card/60 hover:border-brand/30",
                              node.pending && "opacity-70",
                            )}
                            data-testid={`evidence-node-${node.kind}`}
                          >
                            <div className="flex flex-wrap items-center gap-1.5">
                              <Badge variant="outline" className="text-[10px]">
                                {nodeLabel(node.kind, copy)}
                              </Badge>
                              {node.decision === "rejected" && (
                                <Badge variant="destructive" className="text-[10px]">
                                  {copy("rejected")}
                                </Badge>
                              )}
                            </div>
                            <div className="mt-1 text-sm font-medium leading-snug">{node.title}</div>
                            <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">
                              {node.summary}
                            </p>
                            {node.meta ? (
                              <div className="mt-1 text-[11px] text-muted-foreground">{node.meta}</div>
                            ) : null}
                          </button>
                        </li>
                      );
                    })}
                  </ol>
                </div>
              ))
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function NodeDetailCard({
  selected,
  pair,
  agentPath,
}: {
  selected: AgentEvidenceSummary | null;
  pair: { chain: { title: string; kind: string }; node: EvidenceNode } | undefined;
  agentPath?: string;
}) {
  const copy = useEvolutionCopy();
  if (!selected || !pair) {
    return (
      <Card className="bg-background/85 backdrop-blur">
        <CardContent className="py-10 text-center text-sm text-muted-foreground">
          {copy("evidenceSelectNode")}
        </CardContent>
      </Card>
    );
  }
  const { node, chain } = pair;
  const rows: Array<[string, string]> = [
    [copy("evidenceFieldEvent"), node.eventType || "—"],
    [copy("evidenceFieldTitle"), chain.title],
    [copy("evidenceFieldKind"), chain.kind],
    [copy("evidenceFieldSource"), node.sourceRef || "—"],
    [copy("evidenceFieldDecision"), node.decision || "—"],
    [copy("evidenceFieldTask"), node.taskRef || "—"],
    [
      copy("evidenceFieldTime"),
      node.at ? new Date(node.at).toLocaleString() : "—",
    ],
  ];

  return (
    <Card className="bg-background/85 backdrop-blur" data-testid="evidence-node-detail">
      <CardHeader>
        <CardTitle className="text-sm">
          {copy("evidenceNodeDetail")} · {nodeLabel(node.kind, copy)}
        </CardTitle>
        <p className="text-xs text-muted-foreground">{node.summary}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <dl className="space-y-2 text-sm">
          {rows.map(([k, v]) => (
            <div key={k} className="grid grid-cols-[7rem_minmax(0,1fr)] gap-2">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="min-w-0 break-words font-medium">{v}</dd>
            </div>
          ))}
        </dl>
        <div className="space-y-2">
          {agentPath ? (
            <AppLink
              href={agentPath}
              className={buttonVariants({
                variant: "secondary",
                className: "h-auto w-full justify-start px-3 py-2 whitespace-normal",
              })}
            >
              <span className="flex flex-col items-start gap-0.5 text-left">
                <span className="text-sm font-medium">{copy("evidenceOpenAgent")}</span>
                <span className="text-[11px] font-normal text-muted-foreground">
                  {copy("evidenceOpenAgentHint")}
                </span>
              </span>
            </AppLink>
          ) : null}
          {node.submissionId ? (
            <div className="rounded-xl border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
              {copy("evidenceSubmissionRef")}: {node.submissionId.slice(0, 8)}
            </div>
          ) : null}
        </div>
      </CardContent>
    </Card>
  );
}
