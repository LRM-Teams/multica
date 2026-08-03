"use client";

import { memo } from "react";
import type { Node, NodeProps } from "@xyflow/react";
import type { ResearchFlowNodeData } from "../lib/layout-graph";

export type ResearchGitGutterNode = Node<ResearchFlowNodeData, "gitGutter">;

function ResearchGitGutterNodeComponent({ data }: NodeProps<ResearchGitGutterNode>) {
  const width = data.gutterWidth ?? 72;
  const height = data.gutterHeight ?? 400;
  const segments = data.gutterSegments ?? [];

  return (
    <div
      className="pointer-events-none absolute inset-0"
      data-testid="research-git-gutter"
      aria-hidden
    >
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        className="overflow-visible"
      >
        {segments.map((seg) => (
          <path
            key={`lane-${seg.lane}`}
            d={seg.d}
            fill="none"
            stroke={seg.color}
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        ))}
      </svg>
    </div>
  );
}

export const ResearchGitGutterNodeView = memo(ResearchGitGutterNodeComponent);
