import { describe, expect, it } from "vitest";
import {
  INITIAL_RESEARCH_SESSION_UI_STATE,
  researchSessionUiReducer,
} from "./research-session-ui-state";

const reference = {
  stable_id: "insight:00000000-0000-4000-8000-000000000123",
  kind: "insight",
  entity_id: "00000000-0000-4000-8000-000000000123",
  revision: 2,
  content_hash: `sha256:${"a".repeat(64)}`,
  display_summary: "Synthesis",
};

describe("researchSessionUiReducer selected references", () => {
  it("deduplicates immutable identities and clears them after send", () => {
    const attached = researchSessionUiReducer(
      INITIAL_RESEARCH_SESSION_UI_STATE,
      { type: "attachResearchRef", reference },
    );
    const repeated = researchSessionUiReducer(attached, {
      type: "attachResearchRef",
      reference: { ...reference, display_summary: "Latest label" },
    });

    expect(repeated.selectedResearchRefs).toEqual([
      { ...reference, display_summary: "Latest label" },
    ]);
    expect(
      researchSessionUiReducer(repeated, { type: "clearBody" })
        .selectedResearchRefs,
    ).toEqual([]);
  });
});
