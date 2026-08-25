/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteHighlightsCompose } from "./note-highlights-compose";

const DEFAULT_PROMPT =
  "请整理本笔记以及它的子笔记的重点。先用 notes 工具读取当前页及其子树，再按层级列出每页的核心结论、待办和未决问题。不要复述全文，写成可读提纲。";

describe("NoteHighlightsCompose", () => {
  it("shows the default prompt in a card, not a dialog", () => {
    renderWithI18n(
      <NoteHighlightsCompose
        initialText={DEFAULT_PROMPT}
        onSend={vi.fn()}
        onCancel={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("highlights-compose")).toBeTruthy();
    expect(screen.getByRole("textbox")).toHaveValue(DEFAULT_PROMPT);
    expect(screen.getByText("先改范围或侧重点，再发送")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("sends the edited prompt from the card", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    renderWithI18n(
      <NoteHighlightsCompose
        initialText={DEFAULT_PROMPT}
        onSend={onSend}
        onCancel={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    const editor = screen.getByRole("textbox");
    await user.clear(editor);
    await user.type(editor, "只要待办，不要全文");
    await user.click(screen.getByTestId("highlights-send"));

    expect(onSend).toHaveBeenCalledWith("只要待办，不要全文");
  });

  it("disables send when the prompt is empty", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    renderWithI18n(
      <NoteHighlightsCompose
        initialText={DEFAULT_PROMPT}
        onSend={onSend}
        onCancel={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    await user.clear(screen.getByRole("textbox"));
    expect(screen.getByTestId("highlights-send")).toBeDisabled();
    await user.click(screen.getByTestId("highlights-send"));
    expect(onSend).not.toHaveBeenCalled();
  });

  it("cancels without sending", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn();
    const onCancel = vi.fn();
    renderWithI18n(
      <NoteHighlightsCompose
        initialText={DEFAULT_PROMPT}
        onSend={onSend}
        onCancel={onCancel}
      />,
      { locale: "zh-Hans" },
    );

    await user.click(screen.getByTestId("highlights-cancel"));
    expect(onCancel).toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
  });
});
