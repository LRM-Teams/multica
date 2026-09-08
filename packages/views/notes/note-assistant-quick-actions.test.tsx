/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteAssistantQuickActions } from "./note-assistant-quick-actions";

describe("NoteAssistantQuickActions", () => {
  it("runs period brief and highlights actions", async () => {
    const user = userEvent.setup();
    const onAction = vi.fn();
    renderWithI18n(
      <NoteAssistantQuickActions layout="centered" onAction={onAction} />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("note-assistant-quick-actions")).toHaveAttribute(
      "data-layout",
      "centered",
    );
    await user.click(screen.getByTestId("note-assistant-action-period-brief"));
    expect(onAction).toHaveBeenCalledWith("period_brief");
    onAction.mockClear();
    await user.click(screen.getByTestId("note-assistant-action-highlights"));
    expect(onAction).toHaveBeenCalledWith("highlights");
  });
});
