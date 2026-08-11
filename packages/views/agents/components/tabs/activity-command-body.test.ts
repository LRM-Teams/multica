// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  ACTIVITY_COMMAND_FOLD_PREVIEW_LINES,
  ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD,
  ACTIVITY_COMMAND_LONG_LINE_THRESHOLD,
  foldActivityCommandPreview,
  isLongActivityCommand,
} from "./activity-command-body";

describe("isLongActivityCommand / foldActivityCommandPreview", () => {
  it("treats short multi-line bodies as not long", () => {
    expect(isLongActivityCommand("multica message send \\\n  --channel x")).toBe(false);
  });

  it("flags bodies at the line threshold", () => {
    const lines = Array.from(
      { length: ACTIVITY_COMMAND_LONG_LINE_THRESHOLD },
      (_, i) => `line ${i}`,
    ).join("\n");
    expect(isLongActivityCommand(lines)).toBe(true);
  });

  it("flags bodies at the char threshold", () => {
    expect(isLongActivityCommand("x".repeat(ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD))).toBe(true);
  });

  it("folds to the first N whole lines", () => {
    const lines = Array.from({ length: 20 }, (_, i) => `echo ${i}`);
    const preview = foldActivityCommandPreview(lines.join("\n"));
    expect(preview.split("\n")).toHaveLength(ACTIVITY_COMMAND_FOLD_PREVIEW_LINES);
    expect(preview).toBe(lines.slice(0, ACTIVITY_COMMAND_FOLD_PREVIEW_LINES).join("\n"));
  });

  it("folds a single huge line by char cap", () => {
    const huge = "y".repeat(ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD + 50);
    expect(foldActivityCommandPreview(huge)).toHaveLength(ACTIVITY_COMMAND_LONG_CHAR_THRESHOLD);
  });
});
