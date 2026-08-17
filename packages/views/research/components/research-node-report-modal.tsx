"use client";

import { useMemo, type ReactNode } from "react";
import type {
  ResearchFleetMember,
  ResearchGraphNode,
  ResearchRunSnapshot,
  ResearchSource,
} from "@multica/core/types";
import type { TypedGraphNode } from "@multica/core/research";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Badge } from "@multica/ui/components/ui/badge";
import { FileText, GitMerge, ListTree } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { buildNodeReportLineage } from "../lib/research-node-report-lineage";
import { ResearchNodeDetailBody } from "./research-node-detail";

const EMPTY_TYPED_NODES: readonly TypedGraphNode[] = [];

export function ResearchNodeReportModal({
  open,
  node,
  typedNode,
  sources,
  run,
  members,
  typedNodes = EMPTY_TYPED_NODES,
  onClose,
  onSelectLineageNode,
}: {
  open: boolean;
  node: ResearchGraphNode | null;
  typedNode?: TypedGraphNode | null;
  sources: ResearchSource[];
  run?: ResearchRunSnapshot;
  members?: ResearchFleetMember[];
  typedNodes?: readonly TypedGraphNode[];
  onClose: () => void;
  onSelectLineageNode?: (nodeId: string) => void;
}) {
  const { t } = useT("research");
  const lineage = buildNodeReportLineage(typedNode, node);
  const lineageTitles = useMemo(
    () => new Map(typedNodes.map((item) => [item.id, item.title])),
    [typedNodes],
  );
  const confidence =
    typedNode?.confidence ??
    (typeof (node?.payload as { confidence?: number } | null)?.confidence === "number"
      ? (node?.payload as { confidence: number }).confidence
      : null);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent
        data-testid="research-node-report-modal"
        className="flex max-h-[min(860px,92vh)] max-w-[min(1120px,95vw)] flex-col overflow-hidden border-emerald-900/60 bg-[#07111b] p-0 text-slate-100"
      >
        <DialogHeader className="border-b border-emerald-950 bg-[radial-gradient(circle_at_18%_0%,rgba(34,197,94,0.16),transparent_52%)] px-6 py-5 text-left">
          <div className="flex flex-wrap items-start justify-between gap-4 pr-7">
            <div className="min-w-0 max-w-3xl">
              <DialogTitle className="text-balance text-xl leading-tight text-slate-50">
                {node?.title || t(($) => $.d5.report.title)}
              </DialogTitle>
              <DialogDescription className="mt-2 line-clamp-3 max-w-[72ch] text-sm leading-6 text-slate-400">
                {node?.summary || t(($) => $.d5.report.empty_summary)}
              </DialogDescription>
            </div>
            {confidence != null ? (
              <div className="shrink-0 text-right" data-testid="research-node-report-confidence">
                <div className="text-[10px] tracking-[0.12em] text-emerald-300/70 uppercase">
                  {t(($) => $.node.confidence)}
                </div>
                <div className="mt-0.5 text-2xl font-semibold tabular-nums text-emerald-200">
                  {(confidence <= 1 ? confidence * 100 : confidence).toFixed(0)}%
                </div>
              </div>
            ) : null}
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            {typedNode?.level ? (
              <Badge variant="outline" className="border-emerald-800/80 bg-emerald-950/60 text-[10px] text-emerald-200 uppercase">
                {typedNode.level}
              </Badge>
            ) : null}
            {typedNode?.goal_version_id ? (
              <Badge variant="outline" className="border-slate-700 bg-slate-900/60 text-[10px] text-slate-300">
                {t(($) => $.d5.report.goal_version, {
                  version: typedNode.goal_version_id,
                })}
              </Badge>
            ) : null}
            {typedNode?.document_count != null ? (
              <Badge variant="outline" className="border-slate-700 bg-slate-900/60 text-[10px] text-slate-300">
                {t(($) => $.d5.report.document_count, {
                  count: typedNode.document_count,
                })}
              </Badge>
            ) : null}
          </div>
        </DialogHeader>

        {node ? (
          <nav
            aria-label={t(($) => $.d5.report.section_navigation)}
            className="flex gap-1 overflow-x-auto border-b border-slate-800 bg-slate-950/45 px-5 py-2"
          >
            <ReportNavLink href="#node-report-overview" icon={<FileText />}>
              {t(($) => $.d5.report.overview_section)}
            </ReportNavLink>
            {lineage.length > 0 ? (
              <ReportNavLink href="#node-report-lineage" icon={<GitMerge />}>
                {t(($) => $.d5.report.lineage_title)}
              </ReportNavLink>
            ) : null}
            <ReportNavLink href="#node-report-detail" icon={<ListTree />}>
              {t(($) => $.d5.report.detail_section)}
            </ReportNavLink>
          </nav>
        ) : null}

        {node ? (
          <section id="node-report-overview" className="scroll-mt-3 border-b border-slate-800 px-5 py-4">
            <h2 className="text-sm font-semibold text-slate-100">
              {t(($) => $.d5.report.overview_section)}
            </h2>
            <p className="mt-2 max-w-[72ch] text-sm leading-6 text-slate-300">
              {node.summary || t(($) => $.d5.report.empty_summary)}
            </p>
          </section>
        ) : null}

        {lineage.length > 0 ? (
          <section id="node-report-lineage" className="scroll-mt-3 border-b border-slate-800 px-5 py-4">
            <h2 className="mb-3 text-sm font-semibold text-slate-100">
              {t(($) => $.d5.report.lineage_title)}
            </h2>
            <div className="grid gap-3 sm:grid-cols-2">
              {lineage.map((group) => (
                <section key={group.relation} data-lineage-relation={group.relation} className="rounded-xl border border-slate-800 bg-slate-900/50 p-3">
                  <h3 className="text-[10px] font-semibold tracking-wide text-slate-400 uppercase">
                    {t(($) => $.d5.report.lineage[group.relation])}
                  </h3>
                  <ul className="mt-2 space-y-1.5 text-xs text-slate-300">
                    {group.nodeIds.map((id) => {
                      const title = lineageTitles.get(id)?.trim() || id;
                      return (
                        <li key={id} data-testid={`research-node-report-lineage-${id}`}>
                          {onSelectLineageNode ? (
                            <button
                              type="button"
                              className="max-w-full truncate text-left text-emerald-300 underline-offset-2 hover:underline focus-visible:underline"
                              title={title !== id ? id : undefined}
                              onClick={() => onSelectLineageNode(id)}
                            >
                              {title}
                            </button>
                          ) : (
                            title
                          )}
                        </li>
                      );
                    })}
                  </ul>
                </section>
              ))}
            </div>
          </section>
        ) : null}

        {node ? (
          <div id="node-report-detail" className="min-h-0 flex-1 scroll-mt-3 overflow-y-auto">
            <ResearchNodeDetailBody
              node={node}
              sources={sources}
              run={run}
              members={members ?? []}
              onClose={onClose}
              showClose={false}
            />
          </div>
        ) : (
          <p className="px-5 py-4 text-sm text-muted-foreground">
            {t(($) => $.d5.report.empty_summary)}
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ReportNavLink({
  href,
  icon,
  children,
}: {
  href: string;
  icon: ReactNode;
  children: ReactNode;
}) {
  return (
    <a
      href={href}
      className="flex min-h-8 shrink-0 items-center gap-2 rounded-lg px-2.5 text-xs text-slate-400 transition-colors hover:bg-emerald-950/55 hover:text-emerald-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-400"
    >
      <span className="[&>svg]:size-3.5" aria-hidden>
        {icon}
      </span>
      <span>{children}</span>
    </a>
  );
}
