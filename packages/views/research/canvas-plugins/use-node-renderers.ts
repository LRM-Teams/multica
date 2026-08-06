"use client";

import { useEffect, useState } from "react";
import type { NodeTypes as ReactFlowNodeTypes } from "@xyflow/react";
import { useResearchCanvasPluginSlots } from "./registry";
import type {
  ResearchCanvasNodeRendererPayload,
  ResearchCanvasPluginRegistration,
} from "./types";

/**
 * FE-10 — resolve the `nodeRenderer` plugin slot into a ReactFlow NodeTypes
 * map, layered over the canvas's base renderers.
 *
 * The nodeRenderer slot is lazy: its chunk is only requested once this hook
 * runs (i.e. when the canvas mounts with a registered nodeRenderer plugin).
 * While the chunk loads or if it fails, the resolver returns the base
 * renderers unchanged — a broken nodeRenderer module can never blank the
 * canvas (AC #1) and never author canonical state (AC #3).
 */

export function useResearchCanvasNodeRenderers(
  baseNodeTypes: ReactFlowNodeTypes,
): { nodeTypes: ReactFlowNodeTypes; loaded: boolean } {
  const slots = useResearchCanvasPluginSlots();
  const [resolved, setResolved] = useState<ReactFlowNodeTypes | null>(null);

  const registration = slots?.api
    .forSlot("nodeRenderer")
    .find((reg): reg is ResearchCanvasPluginRegistration<"nodeRenderer"> =>
      reg.slot === "nodeRenderer",
    );

  useEffect(() => {
    if (!registration) {
      setResolved(null);
      return;
    }
    let cancelled = false;
    setResolved(null);
    registration
      .load()
      .then((m) => {
        if (cancelled) return;
        const contributor = m.default as ResearchCanvasNodeRendererPayload;
        const contributed = contributor(baseNodeTypes);
        // Merge over base — plugin addition/override only.
        setResolved({ ...baseNodeTypes, ...contributed } as ReactFlowNodeTypes);
      })
      .catch(() => {
        // A failed nodeRenderer module degrades to the base renderers.
        if (!cancelled) setResolved(null);
      });
    return () => {
      cancelled = true;
    };
    // baseNodeTypes is a stable module-level constant in the canvas.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [registration]);

  if (!registration || resolved === null) {
    return { nodeTypes: baseNodeTypes, loaded: false };
  }
  return { nodeTypes: resolved, loaded: true };
}
