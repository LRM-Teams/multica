// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InlineFieldEditor } from "./inline-field-editor";

vi.mock("../../i18n", () => ({
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
  },
  detail: {
    update_failed_toast: "Failed to update agent",
  },
};

describe("InlineFieldEditor (LRM-471)", () => {
  it("swaps the trigger for an in-place input — no dialog/popover role", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <InlineFieldEditor
        value="Beckham"
        kind="input"
        ariaLabel="Edit display name"
        onSave={onSave}
      />,
    );

    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));

    expect(screen.queryByRole("dialog")).toBeNull();
    const input = screen.getByLabelText("Edit display name");
    expect(input).toHaveProperty("value", "Beckham");
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
  });

  it("saves display name on Enter and returns to the read view", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <InlineFieldEditor
        value="Old"
        kind="input"
        ariaLabel="Edit display name"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));
    const input = screen.getByLabelText("Edit display name");
    fireEvent.change(input, { target: { value: "New name" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith("New name");
    });
    await waitFor(() => {
      expect(screen.getByTestId("inline-field-editor-trigger")).toBeTruthy();
    });
  });

  it("saves textarea on ⌘/Ctrl+Enter, not plain Enter", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <InlineFieldEditor
        value="desc"
        kind="textarea"
        ariaLabel="Description"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));
    const area = screen.getByLabelText("Description");
    fireEvent.change(area, { target: { value: "updated" } });
    fireEvent.keyDown(area, { key: "Enter" });
    expect(onSave).not.toHaveBeenCalled();

    fireEvent.keyDown(area, { key: "Enter", metaKey: true });
    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith("updated");
    });
  });

  it("cancels on Esc without calling onSave", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <InlineFieldEditor
        value="Beckham"
        kind="input"
        ariaLabel="Edit display name"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));
    const input = screen.getByLabelText("Edit display name");
    fireEvent.change(input, { target: { value: "Nope" } });
    fireEvent.keyDown(input, { key: "Escape" });

    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId("inline-field-editor-trigger").textContent).toContain(
      "Beckham",
    );
  });

  it("surfaces validate errors inline and keeps editing open", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <InlineFieldEditor
        value="Beckham"
        kind="input"
        ariaLabel="Edit display name"
        validate={(v) => (v.trim() ? null : "Display name is required")}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));
    const input = screen.getByLabelText("Edit display name");
    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Display name is required")).toBeTruthy();
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Edit display name")).toBeTruthy();
  });

  it("surfaces rejected saves inline (LRM-238 — not silent)", async () => {
    const onSave = vi.fn().mockRejectedValue(new Error("server said no"));
    render(
      <InlineFieldEditor
        value="Beckham"
        kind="input"
        ariaLabel="Edit display name"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));
    const input = screen.getByLabelText("Edit display name");
    fireEvent.change(input, { target: { value: "New" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("server said no")).toBeTruthy();
    expect(screen.getByLabelText("Edit display name")).toBeTruthy();
  });

  it("shows char counter for description and blocks over-limit save", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <InlineFieldEditor
        value="ok"
        kind="textarea"
        ariaLabel="Description"
        maxLength={5}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("inline-field-editor-trigger"));
    const area = screen.getByLabelText("Description");
    fireEvent.change(area, { target: { value: "too-long" } });
    expect(screen.getByTestId("char-counter").textContent).toBe("8/5");
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    fireEvent.keyDown(area, { key: "Enter", ctrlKey: true });
    expect(onSave).not.toHaveBeenCalled();
  });
});
