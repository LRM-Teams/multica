/// Reference-only export surface for the dispute subgraph module.
/// Production components import from `./encode` and the contract fixture is
/// dev/test-only (never wired into a production path).
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
