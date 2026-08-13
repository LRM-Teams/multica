/// LRM-1472 / UI-04 — dispute subgraph component module export surface.
export {
  buildDisputeModel,
  buildDisputeModelForNode,
  disputeSubgraphForNode,
  findDisputeRoot,
  type DisputeSubgraphModel,
  type EvidenceView,
  type PositionView,
  type TurnView,
  type EscalationView,
  type DecisionView,
} from "./model";
export type { FocusNodeHandler } from "./parts";
export { PositionFan, EvidenceRelation } from "./parts";
export { stanceGlyph, stanceTone, stanceLabel, type DisputeStanceTone } from "./stance";
export {
  DeliberationTimeline,
  EscalationBanner,
  DecisionHistory,
  DisputeCard,
  Section,
  StatusBadge,
} from "./panels";
export { disputeStatusLabel, type DisputeStatusKey } from "./status-label";
export {
  DisputeDetailSection,
  PositionDetailSection,
  DeliberationDetailSection,
  TurnDetailSection,
  DecisionDetailSection,
} from "./detail-sections";
export {
  moveDisputeNavIndex,
  rovingTabNext,
  disputeNavFromKey,
  type DisputeNavDirection,
} from "./keyboard-nav";
export {
  disputeNodeGlyph,
  decisionIsSuperseded,
  isDisputeDomainNodeType,
  stanceFromPayload,
  turnMarkerFromPayload,
  verdictFromPayload,
  type DecisionVerdict,
  type DeliberationTurnMarker,
  type DisputeStance,
} from "./encode";
export {
  DISPUTE_EDGES,
  DISPUTE_FIXTURE,
  DISPUTE_NODES,
  DISPUTE_SESSION_ID,
} from "./__fixtures__/dispute-contract.fixture";
