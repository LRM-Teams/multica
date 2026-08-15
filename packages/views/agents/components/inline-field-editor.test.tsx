// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InlineFieldEditor } from "./inline-field-editor";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

vi.mock("./char-counter", () => ({
  CharCounter: ({ length, max }: { length: number; max: number }) => (
    <div data-testid="char-counter">
      {length}/{max}
    </div>
  ),
}));

const RESOURCES = {
  inspector: {
    save: "Save",
    cancel: "Cancel",
    save_failed: "Failed to save",
  },
};

describe("InlineFieldEditor (LRM-471)", () => {
  it("keeps a decorated display while preserving in-place editing", () => {
    render(
      <InlineFieldEditor
        value="Atlas"
        kind="input"
        label="Display name"
        onSave={vi.fn(async () => {})}
        displayContent={<span data-testid="decorated-name">Decorated Atlas</span>}
        testId="name"
      />,
    );

    const trigger = screen.getByTestId("name-trigger");
    expect(screen.getByTestId("decorated-name")).toHaveTextContent("Decorated Atlas");
    expect(trigger).toHaveClass("w-fit", "max-w-full");
    expect(trigger.querySelector("svg")).toHaveClass("lucide-pencil");
    fireEvent.click(trigger);
    expect(screen.getByLabelText("Display name")).toHaveValue("Atlas");
  });




  it("blocks empty display name via validate", async () => {
    const onSave = vi.fn(async () => {});
    render(
      <InlineFieldEditor
        value="Atlas"
        kind="input"
        label="Display name"
        validate={(v) => (v.trim() ? null : "required")}
        onSave={onSave}
        testId="name"
      />,
    );
    fireEvent.click(screen.getByTestId("name-trigger"));
    fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "   " } });
    fireEvent.click(screen.getByTestId("name-save"));
    expect(await screen.findByTestId("name-error")).toHaveTextContent("required");
    expect(onSave).not.toHaveBeenCalled();
  });
});
