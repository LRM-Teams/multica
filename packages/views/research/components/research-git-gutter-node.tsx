"use client";

import { memo, useEffect, useRef } from "react";
import type { Node, NodeProps } from "@xyflow/react";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import { gutterGrowthTargetStyle } from "../lib/canvas-reorg-motion";

export type ResearchGitGutterNode = Node<ResearchFlowNodeData, "gitGutter">;

function ResearchGitGutterNodeComponent({ data }: NodeProps<ResearchGitGutterNode>) {
  const width = data.gutterWidth ?? 72;
  const height = data.gutterHeight ?? 400;
  const segments = data.gutterSegments ?? [];
  const pathRefs = useRef<Map<number, SVGPathElement> | null>(null);
  if (pathRefs.current === null) {
    pathRefs.current = new Map();
  }

  // Reorg is broadcast by the canvas root as `data-reorg`, a DOM-only signal.
  // The dash growth is applied imperatively from the observer so no React state
  // mirrors the attribute (mirroring it would force an extra stale render).
  useEffect(() => {
    const paths = pathRefs.current;
    if (!paths) return;
    const first = paths.values().next().value;
    if (!first) return;
    const root = first.closest("[data-reorg]");
    if (!root) return;

    let raf = 0;

    const settle = () => {
      paths.forEach((path) => {
        path.style.cssText = "";
      });
    };

    /** Read every path length first, then write one cssText per path (single reflow each). */
    const measure = () => {
      const measured: { path: SVGPathElement; lane: number; length: number }[] = [];
      paths.forEach((path, lane) => {
        const length = path.getTotalLength();
        if (length > 0) measured.push({ path, lane, length });
      });
      return measured;
    };

    const grow = () => {
      // First frame: hide the full stroke.
      for (const { path, length } of measure()) {
        path.style.cssText = `stroke-dasharray:${length}px;stroke-dashoffset:${length}px;transition:none`;
      }
      // Next frame: animate to a fully drawn stroke.
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        for (const { path, lane, length } of measure()) {
          const target = gutterGrowthTargetStyle(length, lane);
          path.style.cssText = `stroke-dasharray:${length}px;stroke-dashoffset:${target.strokeDashoffset}px;transition:${target.transition}`;
        }
      });
    };

    const isRunning = () => root.getAttribute("data-reorg") === "running";
    let running = isRunning();
    if (running) grow();

    const observer = new MutationObserver(() => {
      const next = isRunning();
      if (next === running) return;
      running = next;
      if (next) grow();
      else settle();
    });
    observer.observe(root, { attributes: true, attributeFilter: ["data-reorg"] });
    return () => {
      observer.disconnect();
      cancelAnimationFrame(raf);
    };
  }, [segments.length]);

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
        {segments.map((seg, index) => (
          <path
            key={`lane-${seg.lane}`}
            ref={(el) => {
              if (el) pathRefs.current?.set(index, el);
              else pathRefs.current?.delete(index);
            }}
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
