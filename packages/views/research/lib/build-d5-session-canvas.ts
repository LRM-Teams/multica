import type { TypedGraphResponse } from "@multica/core/research";
import type { StarGraphLayoutResult } from "@multica/core/research";
import {
  buildStarCanvasViewModel,
  extractLayoutResultFromViewModel,
  rebaseStarCanvasIntoViewModel,
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
    version: "d5-star-v1",
    previous: options.previousLayout,
  });

  const layoutForNext = extractLayoutResultFromViewModel(base);

  if (viewport.width <= 0 || viewport.height <= 0) {
    return { model: base, layoutForNext };
  }

  return {
    model: rebaseStarCanvasIntoViewModel(base, viewport, {
      rightPanelWidth: options.rightPanelWidth,
      padding: 32,
    }),
    layoutForNext,
  };
}
