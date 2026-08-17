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
  onToggleNode: (nodeId: string) => void;
}
