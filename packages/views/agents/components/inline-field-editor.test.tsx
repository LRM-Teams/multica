// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

    expect(screen.getByTestId("decorated-name")).toHaveTextContent("Decorated Atlas");
    fireEvent.click(screen.getByTestId("name-trigger"));
    expect(screen.getByLabelText("Display name")).toHaveValue("Atlas");
  });

  it("turns the field into an in-place input — no dialog/popover", async () => {
    const onSave = vi.fn(async () => {});
    render(
      <InlineFieldEditor
        value="Atlas"
        kind="input"
        label="Display name"
        onSave={onSave}
        testId="name"
      />,
    );

    fireEvent.click(screen.getByTestId("name-trigger"));
    expect(screen.getByTestId("name")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(document.querySelector("[data-slot=popover-content]")).toBeNull();

    const input = screen.getByLabelText("Display name");
    fireEvent.change(input, { target: { value: "Nova" } });
    fireEvent.click(screen.getByTestId("name-save"));

    await waitFor(() => expect(onSave).toHaveBeenCalledWith("Nova"));
  });

  it("saves input on Enter and cancels on Escape", async () => {
    const onSave = vi.fn(async () => {});
    render(
      <InlineFieldEditor
        value="Atlas"
        kind="input"
        label="Display name"
        onSave={onSave}
        testId="name"
      />,
    );
    fireEvent.click(screen.getByTestId("name-trigger"));
    const input = screen.getByLabelText("Display name");
    fireEvent.change(input, { target: { value: "Nova" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("Nova"));

    fireEvent.click(screen.getByTestId("name-trigger"));
    fireEvent.keyDown(screen.getByLabelText("Display name"), { key: "Escape" });
    expect(screen.queryByTestId("name")).not.toBeInTheDocument();
    expect(screen.getByTestId("name-trigger")).toBeInTheDocument();
  });

  it("saves textarea with Ctrl+Enter and surfaces save errors inline", async () => {
    const onSave = vi.fn(async () => {
      throw new Error("boom");
    });
    render(
      <InlineFieldEditor
        value="old"
        kind="textarea"
        label="Description"
        onSave={onSave}
        testId="desc"
      />,
    );
    fireEvent.click(screen.getByTestId("desc-trigger"));
    const area = screen.getByLabelText("Description");
    fireEvent.change(area, { target: { value: "new desc" } });
    fireEvent.keyDown(area, { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(screen.getByTestId("desc-error")).toHaveTextContent("boom"));
    expect(screen.getByTestId("desc")).toBeInTheDocument();
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
