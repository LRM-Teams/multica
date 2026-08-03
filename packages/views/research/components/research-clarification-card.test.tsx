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

  it("announces a missing required field and describes the invalid input", () => {
    render(
      <ResearchClarificationCard
        question={formQuestion}
        resolution={{ status: "pending" }}
      />,
    );

    fireEvent.click(screen.getByTestId("research-clarification-submit"));

    const error = screen.getByTestId("research-clarification-error");
    const budget = screen.getByLabelText(/Budget/);
    expect(error).toHaveAttribute("role", "alert");
    expect(budget).toHaveAttribute("aria-required", "true");
    expect(budget).toHaveAttribute("aria-invalid", "true");
    expect(budget).toHaveAttribute("aria-describedby", error.id);
    expect(budget).toHaveFocus();
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

  // LRM-1169 — an answered option must stay readable by keyboard / screen reader.
  it("keeps the answered option focusable and announces it as selected", () => {
    const onSelect = vi.fn();
    render(
      <ResearchClarificationCard
        question={listQuestion}
        resolution={{
          status: "answered",
          optionId: "cost",
          optionLabel: "Cost",
          replyMessageId: "u1",
        }}
        onSelectOption={onSelect}
      />,
    );

    const answered = screen
      .getAllByTestId("research-clarification-option")
      .find((el) => el.getAttribute("data-option-id") === "cost") as HTMLButtonElement;
    const notPicked = screen
      .getAllByTestId("research-clarification-option")
      .find((el) => el.getAttribute("data-option-id") === "recall") as HTMLButtonElement;

    // The picked option is not removed from the tab order.
    expect(answered.hasAttribute("disabled")).toBe(false);
    expect(answered.disabled).toBe(false);
    expect(answered.getAttribute("aria-disabled")).toBe("true");
    expect(answered.getAttribute("aria-pressed")).toBe("true");
    expect(answered.getAttribute("aria-label")).toBe("Cost");
    answered.focus();
    expect(document.activeElement).toBe(answered);

    // Options the user did not pick still leave the tab order.
    expect(notPicked.disabled).toBe(true);
    expect(notPicked.getAttribute("aria-pressed")).toBe("false");

    // Re-activating the answered option must not resubmit.
    fireEvent.click(answered);
    fireEvent.keyDown(answered, { key: "Enter" });
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("does not resubmit while a pending answer is in flight", () => {
    const onSelect = vi.fn();
    render(
      <ResearchClarificationCard
        question={listQuestion}
        resolution={{ status: "pending" }}
        pending
        onSelectOption={onSelect}
      />,
    );
    const options = screen.getAllByTestId("research-clarification-option");
    for (const opt of options) {
      expect((opt as HTMLButtonElement).disabled).toBe(true);
      expect(opt.getAttribute("aria-disabled")).toBe("true");
      fireEvent.click(opt);
    }
    expect(onSelect).not.toHaveBeenCalled();
  });

  // LRM-1170 — one dim level per state, and skip settles the same way in both layouts.
  it("applies exactly one opacity utility per option state", () => {
    const opacities = (el: Element) =>
      el.className.split(/\s+/).filter((c) => c.startsWith("opacity-"));

    const interactive = render(
      <ResearchClarificationCard question={listQuestion} resolution={{ status: "pending" }} />,
    );
    for (const opt of screen.getAllByTestId("research-clarification-option")) {
      expect(opacities(opt)).toEqual([]);
    }
    interactive.unmount();

    const inFlight = render(
      <ResearchClarificationCard
        question={listQuestion}
        resolution={{ status: "pending" }}
        pending
      />,
    );
    for (const opt of screen.getAllByTestId("research-clarification-option")) {
      expect(opacities(opt)).toEqual(["opacity-60"]);
    }
    inFlight.unmount();

    render(
      <ResearchClarificationCard
        question={listQuestion}
        resolution={{
          status: "answered",
          optionId: "cost",
          optionLabel: "Cost",
          replyMessageId: "u1",
        }}
      />,
    );
    for (const opt of screen.getAllByTestId("research-clarification-option")) {
      const expected =
        opt.getAttribute("data-option-id") === "cost" ? [] : ["opacity-50"];
      expect(opacities(opt)).toEqual(expected);
    }
  });

  it("removes skip once settled in both option and form layouts", () => {
    for (const question of [listQuestion, formQuestion]) {
      const pendingRender = render(
        <ResearchClarificationCard question={question} resolution={{ status: "pending" }} />,
      );
      expect(screen.getByTestId("research-clarification-skip")).toBeTruthy();
      pendingRender.unmount();

      const answeredRender = render(
        <ResearchClarificationCard
          question={question}
          resolution={{ status: "answered", replyMessageId: "u1" }}
        />,
      );
      expect(screen.queryByTestId("research-clarification-skip")).toBeNull();
      answeredRender.unmount();

      const skippedRender = render(
        <ResearchClarificationCard
          question={question}
          resolution={{ status: "skipped", replyMessageId: "u1" }}
        />,
      );
      expect(screen.queryByTestId("research-clarification-skip")).toBeNull();
      skippedRender.unmount();
    }
  });
});
