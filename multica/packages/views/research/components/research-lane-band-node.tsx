"use client";

import { memo } from "react";
import type { Node, NodeProps } from "@xyflow/react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import { LOGIC_LANE_IDS, type LogicLaneId } from "../lib/logic-lanes";

export type ResearchLaneBandNode = Node<ResearchFlowNodeData, "laneBand">;

function ResearchLaneBandNodeComponent({ data }: NodeProps<ResearchLaneBandNode>) {
  const { t } = useT("research");
  const laneId = (data.laneLabelKey ?? data.laneId ?? "orchestrate") as LogicLaneId;
  const index = LOGIC_LANE_IDS.indexOf(laneId);
  const label = t(($) => $.logic.lane[laneId]);

  return (
    <div
      className={cn(
        "pointer-events-none h-full w-full border-b border-dashed border-border/55",
        index % 2 === 0 ? "bg-muted/25" : "bg-transparent",
      )}
      data-testid={`research-lane-band-${laneId}`}
      aria-hidden
    >
      <div className="flex h-full w-[76px] items-center px-2">
        <span className="text-[11px] font-semibold leading-tight text-muted-foreground">
          {label}
        </span>
      </div>
    </div>
  );
}

export const ResearchLaneBandNodeView = memo(ResearchLaneBandNodeComponent);
