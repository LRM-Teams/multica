import type { TypedGraphResponse } from "@multica/core/research";
import type { StarGraphLayoutResult } from "@multica/core/research";
import {
  buildStarCanvasViewModel,
  rebaseStarCanvasIntoViewModel,
  type StarCanvasViewModel,
} from "../star-graph";

export function buildD5SessionCanvasModel(
  typed: TypedGraphResponse | undefined,
  viewport: { width: number; height: number },
  options: {
    rightPanelWidth: number;
    previousLayout?: StarGraphLayoutResult;
  },
): StarCanvasViewModel | null {
  if (!typed || typed.nodes.length === 0) return null;

  const base = buildStarCanvasViewModel({
    nodes: typed.nodes,
    edges: typed.edges,
    seed: typed.graph_version,
    version: `typed-${typed.graph_version}`,
    previous: options.previousLayout,
  });

  if (viewport.width <= 0 || viewport.height <= 0) return base;

  return rebaseStarCanvasIntoViewModel(base, viewport, {
    rightPanelWidth: options.rightPanelWidth,
    padding: 32,
  });
}
