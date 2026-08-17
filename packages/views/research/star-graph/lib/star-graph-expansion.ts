/**
 * Presentation-only expansion contract for the D5 star graph.
 *
 * The caller owns every set. In V6 they must come from the server Projection
 * Snapshot/Slice (`expandable`) plus client request state. The renderer never
 * derives absorption, tier, Frontier membership, or child identities.
 */
export interface StarGraphExpansionControl {
  expandableNodeIds: ReadonlySet<string>;
  expandedNodeIds: ReadonlySet<string>;
  loadingNodeIds?: ReadonlySet<string>;
  /** Latest server-backed disclosure result, used only for spatial motion. */
  transition?: StarGraphExpansionTransition | null;
  /** Removes blur/glow while retaining position and opacity semantics. */
  lowPerformance?: boolean;
  onToggleNode: (nodeId: string) => void;
}

export interface StarGraphExpansionTransition {
  sequence: string | number;
  kind: "expand" | "collapse";
  rootNodeId: string;
  /** Exact node ids returned/removed by the one-layer Projection Slice. */
  revealedNodeIds: readonly string[];
}
