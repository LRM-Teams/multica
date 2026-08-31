import type { TypedGraphResponse } from "@multica/core/research";
import type { StarGraphLayoutResult } from "@multica/core/research";
import {
  buildStarCanvasViewModel,
  extractLayoutResultFromViewModel,
  type StarCanvasViewModel,
} from "../star-graph";

export interface D5SessionCanvasBuild {
  model: StarCanvasViewModel;
  /** Pre-viewport-rebase layout — must feed the next incremental layout pass. */
  layoutForNext: StarGraphLayoutResult;
}

export function buildD5SessionCanvasModel(
  typed: TypedGraphResponse | undefined,
  viewport: { width: number; height: number },
  options: {
    rightPanelWidth: number;
    previousLayout?: StarGraphLayoutResult;
  },
): D5SessionCanvasBuild | null {
  if (!typed || typed.nodes.length === 0) return null;

  const base = buildStarCanvasViewModel({
    nodes: typed.nodes,
    edges: typed.edges,
    seed: typed.graph_version,
    // Stable version so graph_version bumps reuse incremental layout positions.
    version: "d5-star-v3",
    previous: options.previousLayout,
  });

  const layoutForNext = extractLayoutResultFromViewModel(base);
  // The interactive camera owns viewport fitting. Pre-scaling node positions
  // here and fitting them again in StarGraphCanvas compressed every relation
  // into the same short radius and destroyed the constellation's depth.
  void viewport;
  void options.rightPanelWidth;
  return { model: base, layoutForNext };
}
