export type ResearchSessionUiState = {
  body: string;
  createProject: boolean;
  createChannel: boolean;
  deliveryOpen: boolean;
  selectedFamily: string | null;
};

export type ResearchSessionUiAction =
  | { type: "setBody"; body: string }
  | { type: "setCreateProject"; value: boolean }
  | { type: "setCreateChannel"; value: boolean }
  | { type: "setDeliveryOpen"; value: boolean }
  | { type: "setFamily"; family: string | null }
  | { type: "clearBody" };

export const INITIAL_RESEARCH_SESSION_UI_STATE: ResearchSessionUiState = {
  body: "",
  createProject: true,
  createChannel: true,
  deliveryOpen: false,
  selectedFamily: null,
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
    case "clearBody":
      return { ...state, body: "" };
    default:
      return state;
  }
}
