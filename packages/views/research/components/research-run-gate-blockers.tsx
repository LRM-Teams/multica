"use client";

import { AlertTriangle, LocateFixed } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { RunV2GateBlocker } from "../lib/run-v2-canvas-view-model";

export function ResearchRunGateBlockers({ blockers, degraded, onLocate, title, degradedTitle, degradedBody }: {
  blockers: readonly RunV2GateBlocker[];
  degraded: boolean;
  onLocate: (nodeId: string) => void;
  title: string;
  degradedTitle: string;
  degradedBody: string;
}) {
  if (!degraded && blockers.length === 0) return null;
  return (
    <aside className="pointer-events-auto absolute left-3 top-14 z-20 w-[min(360px,calc(100%-24px))] rounded-lg border bg-card/95 p-2 shadow-sm backdrop-blur-md" aria-label={degraded ? degradedTitle : title} data-testid="research-run-gates">
      <div className="flex items-center gap-2 px-1 py-1 text-xs font-semibold text-foreground">
        <AlertTriangle className="size-3.5 text-warning" aria-hidden />
        {degraded ? degradedTitle : title}
      </div>
      {degraded ? <p className="px-1 pb-1 text-xs text-muted-foreground">{degradedBody}</p> : null}
      <div className="flex flex-wrap gap-1.5">
        {blockers.map((blocker) => (
          <button key={blocker.id} type="button" disabled={!blocker.targetNodeId} onClick={() => blocker.targetNodeId && onLocate(blocker.targetNodeId)} className={cn("inline-flex min-h-8 items-center gap-1.5 rounded-md border px-2 py-1 text-left text-xs", blocker.targetNodeId ? "border-warning/35 bg-warning/10 text-foreground hover:bg-warning/15 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand" : "cursor-default border-border bg-muted text-muted-foreground")}>
            {blocker.targetNodeId ? <LocateFixed className="size-3" aria-hidden /> : null}
            {blocker.label}
          </button>
        ))}
      </div>
    </aside>
  );
}
