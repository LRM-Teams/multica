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
      />,
    );

    const diff = screen.getByTestId("note-ai-diff-preview");
    expect(within(diff).getByText("Current")).toBeInTheDocument();
    expect(within(diff).getByText("AI proposal")).toBeInTheDocument();
    expect(within(diff).getByText("old line")).toBeInTheDocument();
    expect(within(diff).getByText("new line")).toBeInTheDocument();
    expect(within(diff).getByText("shared")).toBeInTheDocument();
  });
});
