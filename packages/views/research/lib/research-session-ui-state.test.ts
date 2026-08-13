import { describe, expect, it } from "vitest";
import {
  INITIAL_RESEARCH_SESSION_UI_STATE,
  researchSessionUiReducer,
} from "./research-session-ui-state";

describe("researchSessionUiReducer", () => {
  it("clears every session-scoped draft and overlay on session change", () => {
    const dirty = {
      body: "send this only to the old session",
      createProject: false,
      createChannel: false,
      deliveryOpen: true,
      selectedFamily: "evidence",
    };

    expect(researchSessionUiReducer(dirty, { type: "resetSession" })).toEqual(
      INITIAL_RESEARCH_SESSION_UI_STATE,
    );
  });
});
