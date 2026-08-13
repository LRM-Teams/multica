"use client";

import { useMemo } from "react";
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
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Badge } from "@multica/ui/components/ui/badge";
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
        className="flex max-h-[min(820px,90vh)] max-w-[min(1040px,94vw)] flex-col overflow-hidden p-0"
      >
        <DialogHeader className="border-b px-5 py-4 text-left">
          <DialogTitle>{node?.title || t(($) => $.d5.report.title)}</DialogTitle>
          <div className="mt-2 flex flex-wrap gap-2">
            {typedNode?.level ? (
              <Badge variant="outline" className="text-[10px] uppercase">
                {typedNode.level}
              </Badge>
            ) : null}
            {confidence != null ? (
              <Badge variant="secondary" className="text-[10px]">
                {t(($) => $.node.confidence)}{" "}
                {(confidence <= 1 ? confidence * 100 : confidence).toFixed(0)}%
              </Badge>
            ) : null}
            {typedNode?.goal_version_id ? (
              <Badge variant="outline" className="text-[10px]">
                {t(($) => $.d5.report.goal_version, {
                  version: typedNode.goal_version_id,
                })}
              </Badge>
            ) : null}
          </div>
        </DialogHeader>

        {lineage.length > 0 ? (
          <section className="border-b px-5 py-3">
            <h3 className="mb-2 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.d5.report.lineage_title)}
            </h3>
            <div className="grid gap-2 sm:grid-cols-2">
              {lineage.map((group) => (
                <section key={group.relation} data-lineage-relation={group.relation}>
                  <h4 className="text-[10px] font-semibold uppercase text-muted-foreground">
                    {t(($) => $.d5.report.lineage[group.relation])}
                  </h4>
                  <ul className="mt-1 space-y-1 text-[12px] text-muted-foreground">
                    {group.nodeIds.map((id) => {
                      const title = lineageTitles.get(id)?.trim() || id;
                      return (
                        <li key={id} data-testid={`research-node-report-lineage-${id}`}>
                          {onSelectLineageNode ? (
                            <button
                              type="button"
                              className="max-w-full truncate text-left text-primary underline-offset-2 hover:underline"
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
          <div className="min-h-0 flex-1 overflow-y-auto">
            <ResearchNodeDetailBody
              node={node}
              sources={sources}
              run={run}
              members={members ?? []}
              onClose={onClose}
              showClose
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
