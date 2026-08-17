import type { ResearchSelectedReference } from "@multica/core/types";

export type ResearchSessionUiState = {
  body: string;
  createProject: boolean;
  createChannel: boolean;
  deliveryOpen: boolean;
  selectedFamily: string | null;
  selectedResearchRefs: ResearchSelectedReference[];
};

export type ResearchSessionUiAction =
  | { type: "setBody"; body: string }
  | { type: "setCreateProject"; value: boolean }
  | { type: "setCreateChannel"; value: boolean }
  | { type: "setDeliveryOpen"; value: boolean }
  | { type: "setFamily"; family: string | null }
  | { type: "attachResearchRef"; reference: ResearchSelectedReference }
  | { type: "removeResearchRef"; stableId: string }
  | { type: "clearBody" };

export const INITIAL_RESEARCH_SESSION_UI_STATE: ResearchSessionUiState = {
  body: "",
  createProject: true,
  createChannel: true,
  deliveryOpen: false,
  selectedFamily: null,
  selectedResearchRefs: [],
};

export function researchSessionUiReducer(
  state: ResearchSessionUiState,
  action: ResearchSessionUiAction,
): ResearchSessionUiState {
  switch (action.type) {
    case "setBody":
      return { ...state, body: action.body };
    case "setCreateProject":
      return { ...state, createProject: action.value };
    case "setCreateChannel":
      return { ...state, createChannel: action.value };
    case "setDeliveryOpen":
      return { ...state, deliveryOpen: action.value };
    case "setFamily":
      return { ...state, selectedFamily: action.family };
    case "attachResearchRef":
      return {
        ...state,
        selectedResearchRefs: [
          ...state.selectedResearchRefs.filter(
            (reference) => reference.stable_id !== action.reference.stable_id,
          ),
          action.reference,
        ].slice(-256),
      };
    case "removeResearchRef":
      return {
        ...state,
        selectedResearchRefs: state.selectedResearchRefs.filter(
          (reference) => reference.stable_id !== action.stableId,
        ),
      };
    case "clearBody":
      return { ...state, body: "", selectedResearchRefs: [] };
    default:
      return state;
  }
}
