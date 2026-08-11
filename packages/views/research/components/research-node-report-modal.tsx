"use client";

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
import { ResearchNodeDetailBody } from "./research-node-detail";

function lineageInputs(
  node: TypedGraphNode | undefined,
  snapshotNode: ResearchGraphNode | null,
): string[] {
  const merged = node?.merged_from?.length
    ? node.merged_from
    : Array.isArray((snapshotNode?.payload as { merged_from?: string[] } | null)?.merged_from)
      ? ((snapshotNode?.payload as { merged_from?: string[] }).merged_from ?? [])
      : [];
  return merged.filter(Boolean);
}

export function ResearchNodeReportModal({
  open,
  node,
  typedNode,
  sources,
  run,
  members,
  onClose,
}: {
  open: boolean;
  node: ResearchGraphNode | null;
  typedNode?: TypedGraphNode | null;
  sources: ResearchSource[];
  run?: ResearchRunSnapshot;
  members?: ResearchFleetMember[];
  onClose: () => void;
}) {
  const { t } = useT("research");
  const mergedFrom = lineageInputs(typedNode ?? undefined, node);
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

        {mergedFrom.length > 0 ? (
          <section className="border-b px-5 py-3">
            <h3 className="mb-2 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.d5.report.lineage_title)}
            </h3>
            <ul className="space-y-1 text-[12px] text-muted-foreground">
              {mergedFrom.map((id) => (
                <li key={id} data-testid={`research-node-report-lineage-${id}`}>
                  {id}
                </li>
              ))}
            </ul>
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
