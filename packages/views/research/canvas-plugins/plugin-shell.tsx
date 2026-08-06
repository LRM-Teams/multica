"use client";

import { useMemo, type JSX, type ReactNode } from "react";
import { ResearchCanvasPluginSlot } from "./plugin-host";
import { ResearchCanvasPluginSlots } from "./plugin-slots";
import { useResearchCanvasSlot } from "./use-registered-slot";
import { buildCanvasPluginContext } from "./context";
import type {
  ResearchCanvasNodeContext,
  ResearchCanvasPluginRegistration,
  ResearchCanvasPluginContext,
} from "./types";
import type { ResearchCanvasRenderSlotId } from "./plugin-host";

const RENDER_SLOT_IDS: ResearchCanvasRenderSlotId[] = [
  "insight",
  "dispute",
  "motion",
  "executionOverlay",
  "trajectoryJump",
];

/**
 * FE-10 / LRM-1486 — THE single Canvas plugin shell.
 *
 * `research-canvas.tsx` wires the plugin system in exactly one place
 * (`<ResearchCanvasPluginShell>`), and every downstream plugin slice
 * (Insight / Dispute / Motion / Execution overlay / Trajectory) registers
 * into the typed slots instead of editing the canvas. This keeps the other
 * parallel PRs from争抢 research-canvas.tsx.
 *
 * It:
 *  - provides the plugin-slot registry to feature modules in the tree;
 *  - renders the five render-slot boundaries (Insight, Dispute, Motion,
 *    Execution overlay, Trajectory entry) each in its OWN lazy + error
 *    boundary, so one failing module never blanks the canvas (AC #1);
 *  - builds the display-only `CanvasPluginContext` (AC #3): slots receive a
 *    read-only view snapshot and can never touch the committed Projection.
 *
 * Each render slot renders inside a transparent layer. Absent slots are inert
 * (`AbsentSlotFallback`), loading shows a local skeleton, and errors collapse
 * to a retry chip — so an empty shell is visually identical to today's canvas.
 */
export function ResearchCanvasPluginShell({
  nodes,
  selectedNodeId,
  reducedMotion,
  children,
}: {
  /** Already-derived display node contexts (never canonical nodes). */
  nodes: readonly ResearchCanvasNodeContext[];
  selectedNodeId: string | null;
  reducedMotion: boolean;
  children?: ReactNode;
}): JSX.Element {
  const context: ResearchCanvasPluginContext = useMemo(
    () => buildCanvasPluginContext(nodes, selectedNodeId, reducedMotion),
    [nodes, selectedNodeId, reducedMotion],
  );

  return (
    <ResearchCanvasPluginSlots>
      <div
        data-testid="research-canvas-plugin-shell"
        className="pointer-events-none absolute inset-0 z-10"
        aria-hidden="true"
      >
        {RENDER_SLOT_IDS.map((slot) => (
          <SlotLayer key={slot} slot={slot} context={context} />
        ))}
      </div>
      {children}
    </ResearchCanvasPluginSlots>
  );
}

function SlotLayer({
  slot,
  context,
}: {
  slot: ResearchCanvasRenderSlotId;
  context: ResearchCanvasPluginContext;
}): JSX.Element {
  const registration = useResearchCanvasSlot(slot);
  return (
    <div
      className="absolute inset-0"
      data-slot={slot}
      data-testid={`research-canvas-plugin-slot-${slot}`}
    >
      <ResearchCanvasPluginSlot
        slot={slot}
        registration={registration as ResearchCanvasPluginRegistration<any> | undefined}
        props={{ context }}
      />
    </div>
  );
}
