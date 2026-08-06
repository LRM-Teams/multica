/** Display-only geometry for the unified canvas render layer. */
export interface Point {
  x: number;
  y: number;
}

/** A rendered node: canonical projection data + display geometry. */
export interface RenderNode {
  id: string;
  kind: string;
  title: string;
  status: string;
  importance: number;
  freshness: number;
  position: Point;
}

/** A rendered edge: canonical relation + endpoints resolved to view ids. */
export interface RenderEdge {
  id: string;
  from: string;
  to: string;
  relation: string;
}
