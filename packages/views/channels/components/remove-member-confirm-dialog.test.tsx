// @vitest-environment jsdom

import { type ComponentProps } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { RemoveMemberConfirmDialog } from "./remove-member-confirm-dialog";

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
  members: {
    remove_confirm_title: 'Remove "{{name}}"?',
    remove_confirm_description: "They lose channel access immediately.",
    remove_confirm: "Confirm remove",
    remove_confirming: "Removing…",
    remove_cancel: "Cancel",
  },
};

function renderDialog(
  overrides: Partial<ComponentProps<typeof RemoveMemberConfirmDialog>> = {},
) {
  const onConfirm = vi.fn();
  const onOpenChange = vi.fn();
  const view = render(
    <RemoveMemberConfirmDialog
      open
      displayName="Beckham"
      onConfirm={onConfirm}
      onOpenChange={onOpenChange}
      {...overrides}
    />,
  );
  return { onConfirm, onOpenChange, ...view };
}

describe("RemoveMemberConfirmDialog (LRM-1327)", () => {
  it("renders as alertdialog with Cancel autofocus (not destructive)", () => {
    renderDialog();
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByTestId("group-member-remove-confirm")).toBeInTheDocument();
    const cancel = screen.getByTestId("group-member-remove-cancel");
    expect(cancel).toHaveFocus();
    expect(document.activeElement).not.toBe(
      screen.getByTestId("group-member-remove-confirm-action"),
    );
  });

  it("shows spinner copy and disables both actions while pending", () => {
    renderDialog({ pending: true });
    const confirm = screen.getByTestId("group-member-remove-confirm-action");
    expect(confirm).toHaveTextContent("Removing…");
    expect(confirm).toBeDisabled();
    expect(confirm.querySelector("svg")).not.toBeNull();
    expect(screen.getByTestId("group-member-remove-cancel")).toBeDisabled();
  });

  it("keeps Cancel disabled so dismiss cannot fire while pending", () => {
    const { onOpenChange } = renderDialog({ pending: true });
    fireEvent.click(screen.getByTestId("group-member-remove-cancel"));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("fires onConfirm once and locks double-click", () => {
    const { onConfirm } = renderDialog();
    const action = screen.getByTestId("group-member-remove-confirm-action");
    fireEvent.click(action);
    fireEvent.click(action);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("footer uses column-reverse on narrow / row+end on sm classes", () => {
    renderDialog();
    const footer = screen.getByTestId("group-member-remove-confirm").querySelector(
      "[data-slot='alert-dialog-footer']",
    );
    expect(footer?.className).toContain("flex-col-reverse");
    expect(footer?.className).toContain("sm:flex-row");
    expect(footer?.className).toContain("sm:justify-end");
  });

  it("content max-width classes match 320/384 lock", () => {
    renderDialog();
    const content = screen.getByTestId("group-member-remove-confirm");
    expect(content.className).toMatch(/max-w-xs/);
    expect(content.className).toMatch(/sm:max-w-sm/);
  });
});
