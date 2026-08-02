import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchClarificationQuestion } from "@multica/core/types";
import { ResearchClarificationCard } from "./research-clarification-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, string>,
    ) => {
      const dict = {
        clarification: {
          submit: "Submit answer",
          submitting: "Submitting…",
          skip: "Skip",
          required_fields: "Fill at least one field before submitting",
          answered: "Answer sent — session continues",
          answered_option: `Selected: ${vars?.label ?? ""}`,
          skipped: "Skipped — session continues",
        },
      };
      return fn(dict);
    },
  }),
}));

const listQuestion: ResearchClarificationQuestion = {
  question_id: "q1",
  prompt: "Which dimension first?",
  layout: "list",
  options: [
    { id: "cost", label: "Cost", description: "Economics" },
    { id: "recall", label: "Recall" },
  ],
  fields: [],
  allow_skip: true,
  message_id: "m1",
  created_at: "2026-08-02T10:00:00Z",
};

const formQuestion: ResearchClarificationQuestion = {
  question_id: "q2",
  prompt: "Hard constraints?",
  layout: "form",
  options: [],
  fields: [
    { id: "budget", label: "Budget", type: "text", required: true },
    { id: "notes", label: "Notes", type: "textarea" },
  ],
  allow_skip: true,
  message_id: "m2",
  created_at: "2026-08-02T10:00:00Z",
};

describe("ResearchClarificationCard", () => {
  it("renders list options full-width and fires select / skip", () => {
    const onSelect = vi.fn();
    const onSkip = vi.fn();
    const { container } = render(
      <ResearchClarificationCard
        question={listQuestion}
        resolution={{ status: "pending" }}
        onSelectOption={onSelect}
        onSkip={onSkip}
      />,
    );
    const card = container.querySelector('[data-testid="research-clarification-card"]');
    expect(card?.className).toContain("w-full");
    expect(screen.getByText("Which dimension first?")).toBeTruthy();
    fireEvent.click(screen.getByText("Cost"));
    expect(onSelect).toHaveBeenCalledWith("cost");
    fireEvent.click(screen.getByTestId("research-clarification-skip"));
    expect(onSkip).toHaveBeenCalled();
  });

  it("renders form fields and submits values", () => {
    const onSubmit = vi.fn();
    render(
      <ResearchClarificationCard
        question={formQuestion}
        resolution={{ status: "pending" }}
        onSubmitForm={onSubmit}
      />,
    );
    fireEvent.change(screen.getByLabelText(/Budget/), { target: { value: "2000" } });
    fireEvent.click(screen.getByTestId("research-clarification-submit"));
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ budget: "2000" }),
    );
  });

  it("locks controls after skip and shows status", () => {
    render(
      <ResearchClarificationCard
        question={listQuestion}
        resolution={{ status: "skipped", replyMessageId: "u1" }}
      />,
    );
    expect(screen.getByTestId("research-clarification-skipped")).toBeTruthy();
    expect(screen.queryByTestId("research-clarification-skip")).toBeNull();
    const options = screen.getAllByTestId("research-clarification-option");
    for (const opt of options) {
      expect((opt as HTMLButtonElement).disabled).toBe(true);
    }
  });
});
