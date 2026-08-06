// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDeleteAgent } from "./confirm-delete-agent";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string, vars?: Record<string, unknown>) => {
      const template = selector(RESOURCES);
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(vars?.[key] ?? `{{${key}}}`),
      );
    },
  }),
}));

const RESOURCES = {
  delete_confirm: {
    title: "Delete agent?",
    description: "About to delete \"{{name}}\". Restorable.",
    cancel: "Cancel",
    confirm: "Delete",
    confirming: "Deleting…",
  },
};

describe("ConfirmDeleteAgent (LRM-865)", () => {
  it("does not call onConfirm until Delete is clicked", () => {
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    render(
      <ConfirmDeleteAgent
        open
        displayName="UI Designer"
        onConfirm={onConfirm}
        onOpenChange={onOpenChange}
      />,
    );
    expect(screen.getByTestId("confirm-delete-agent")).toBeInTheDocument();
    expect(screen.getByText('About to delete "UI Designer". Restorable.')).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("shows Deleting… and disables actions while pending", () => {
    render(
      <ConfirmDeleteAgent
        open
        displayName="Atlas"
        pending
        onConfirm={vi.fn()}
        onOpenChange={vi.fn()}
      />,
    );
    const confirm = screen.getByTestId("confirm-delete-agent-confirm");
    expect(confirm).toHaveTextContent("Deleting…");
    expect(confirm).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  });

  it("fires onConfirm when Delete is clicked", () => {
    const onConfirm = vi.fn();
    render(
      <ConfirmDeleteAgent
        open
        displayName="Atlas"
        onConfirm={onConfirm}
        onOpenChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId("confirm-delete-agent-confirm"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
