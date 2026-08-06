/**
 * Research V6 — node renderers public API (UI-01 / LRM-1475).
 *
 * Re-exports the registry, family visuals, state matrix, shell, generic card
 * and the top-level renderer so sibling modules and demos import from one
 * surface. Counterpart of the canonical `@multica/core/research-v6/registry`.
 */
export {
  NODE_KIND_FAMILIES,
  NODE_KIND_FAMILY_LABELS,
  NODE_KIND_TO_FAMILY,
  familyForNodeKind,
  classifyNodeFamily,
  KNOWN_NODE_KINDS,
} from "./node-kind-registry";
export type { NodeKindFamily, NodeKindFamilySurface } from "./node-kind-registry";
export { familyVisualFor, ALL_FAMILY_VISUALS } from "./node-family-visuals";
export type { NodeFamilyVisual } from "./node-family-visuals";
export {
  NODE_CARD_STATES,
  resolveCardState,
  stateVisualFor,
} from "./node-state-matrix";
export type { NodeCardState, NodeStateVisual } from "./node-state-matrix";
export { NodeCardShell } from "./node-card-shell";
export type { NodeCardShellProps, NodeCardZoom } from "./node-card-shell";
export { GenericNodeCard } from "./generic-node-card";
export type { GenericNodeCardProps } from "./generic-node-card";
export { nodeCardFacts } from "./node-detail-fields";
export type { NodeCardFacts } from "./node-detail-fields";
export { importanceToStars } from "./node-importance";
export { NodeRenderer, V6NodeCard } from "./node-renderer";
export type { NodeRendererProps } from "./node-renderer";
