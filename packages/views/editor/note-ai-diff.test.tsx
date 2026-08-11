/**
 * @vitest-environment happy-dom
 */
import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { NoteAIDiffPreview } from "./note-ai-diff";
import { buildNoteAILineDiff } from "./note-ai-diff-utils";

describe("NoteAIDiffPreview", () => {
  it("builds a line diff with removed and added lines", () => {
    expect(buildNoteAILineDiff("alpha\nbeta\ngamma", "alpha\nbetter\ngamma\ndelta")).toEqual([
      { kind: "same", text: "alpha", oldLine: 1, newLine: 1 },
      { kind: "remove", text: "beta", oldLine: 2, newLine: null },
      { kind: "add", text: "better", oldLine: null, newLine: 2 },
      { kind: "same", text: "gamma", oldLine: 3, newLine: 3 },
      { kind: "add", text: "delta", oldLine: null, newLine: 4 },
    ]);
  });

  it("renders a unified diff preview", () => {
    render(
      <NoteAIDiffPreview
        before={"old line\nshared"}
        after={"new line\nshared"}
        beforeLabel="Current"
        afterLabel="AI proposal"
        emptyLabel="No line changes."
        omittedLabel="Some diff lines are hidden."
      />,
    );

    const diff = screen.getByTestId("note-ai-diff-preview");
    expect(within(diff).getByText("Current")).toBeInTheDocument();
    expect(within(diff).getByText("AI proposal")).toBeInTheDocument();
    expect(within(diff).getByText("old line")).toBeInTheDocument();
    expect(within(diff).getByText("new line")).toBeInTheDocument();
    expect(within(diff).getByText("shared")).toBeInTheDocument();
  });

  it("collapses unchanged context in rendered previews", () => {
    render(
      <NoteAIDiffPreview
        before={"same 1\nsame 2\nsame 3\nsame 4\nsame 5\nsame 6\nold\nsame 7\nsame 8\nsame 9\nsame 10\nsame 11"}
        after={"same 1\nsame 2\nsame 3\nsame 4\nsame 5\nsame 6\nnew\nsame 7\nsame 8\nsame 9\nsame 10\nsame 11"}
        beforeLabel="Current"
        afterLabel="AI proposal"
        emptyLabel="No line changes."
        omittedLabel="Some diff lines are hidden."
      />,
    );

    const diff = screen.getByTestId("note-ai-diff-preview");
    expect(within(diff).getByText("old")).toBeInTheDocument();
    expect(within(diff).getByText("new")).toBeInTheDocument();
    expect(within(diff).getAllByText("Some diff lines are hidden.").length).toBeGreaterThan(0);
  });

  it("uses an anchored capped diff for very large replacements", () => {
    const before = Array.from({ length: 60 }, (_, index) => `old ${index + 1}`).join("\n");
    const after = Array.from({ length: 60 }, (_, index) => `new ${index + 1}`).join("\n");
    const lines = buildNoteAILineDiff(before, after, {
      compact: true,
      maxCompareCells: 10,
      maxChangedLinesPerSide: 6,
    });

    expect(lines.some((line) => line.kind === "remove")).toBe(true);
    expect(lines.some((line) => line.kind === "add")).toBe(true);
    expect(lines.some((line) => line.kind === "omitted")).toBe(true);
    expect(lines.length).toBeLessThan(20);
  });
});
