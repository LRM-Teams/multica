import { describe, expect, it } from "vitest";
import type { TaskMessagePayload } from "@multica/core/types";
import {
  coalesceStreamText,
  isResearchSessionStoppable,
  researchWakeChatTitle,
} from "./research-stream";

function msg(
  partial: Partial<TaskMessagePayload> & Pick<TaskMessagePayload, "seq" | "type">,
): TaskMessagePayload {
  return {
    task_id: "t1",
    issue_id: "",
    ...partial,
  };
}

describe("research-stream helpers", () => {
  it("builds the wake chat title", () => {
    expect(researchWakeChatTitle("abc")).toBe("research:abc");
  });

  it("marks drafting/running/awaiting as stoppable", () => {
    expect(isResearchSessionStoppable("running")).toBe(true);
    expect(isResearchSessionStoppable("paused")).toBe(false);
    expect(isResearchSessionStoppable("completed")).toBe(false);
  });

  it("coalesces text fragments and skips diagnostic/thinking", () => {
    const text = coalesceStreamText([
      msg({ seq: 1, type: "text", content: "Hello " }),
      msg({ seq: 2, type: "thinking", content: "hmm" }),
      msg({ seq: 3, type: "text", content: "world", visibility: "diagnostic_only" }),
      msg({ seq: 4, type: "text", content: "fleet." }),
    ]);
    expect(text).toBe("Hello fleet.");
  });
});
