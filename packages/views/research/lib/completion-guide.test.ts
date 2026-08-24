import { describe, expect, it } from "vitest";
import { resolveCompletionGuideKind } from "./completion-guide";

describe("completion-guide (LRM-832)", () => {
  it("maps terminal statuses to done / failed / null", () => {
    expect(resolveCompletionGuideKind("completed")).toBe("done");
    expect(resolveCompletionGuideKind("archived")).toBe("done");
    expect(resolveCompletionGuideKind("failed")).toBe("failed");
    expect(resolveCompletionGuideKind("error")).toBe("failed");
    expect(resolveCompletionGuideKind("cancelled")).toBe("failed");
    expect(resolveCompletionGuideKind("running")).toBeNull();
    expect(resolveCompletionGuideKind(null)).toBeNull();
  });
});
