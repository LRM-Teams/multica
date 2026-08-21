/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import {
  CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS,
  ChatMessageHoverShell,
} from "./chat-message-hover-actions";

const copyTextMock = vi.fn();

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: (text: string) => copyTextMock(text),
}));

describe("ChatMessageHoverShell", () => {
  beforeEach(() => {
    copyTextMock.mockReset();
    copyTextMock.mockResolvedValue(true);
  });

  it("keeps the Messages hover-overlay classes and hides the bar at rest", () => {
    expect(CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS).toContain("group-hover:opacity-100");
    expect(CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS).toContain("opacity-0");
    expect(CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS).not.toContain("static");
  });

  it("does not render a fixed copy control when disabled", () => {
    renderWithI18n(
      <ChatMessageHoverShell enabled={false} copyTextValue="hello">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    expect(screen.queryByTestId("chat-message-action-bar")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
  });

  it("copies from the hover bar, not a footer slot", async () => {
    const user = userEvent.setup();
    renderWithI18n(
      <ChatMessageHoverShell enabled copyTextValue="brief body">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    expect(screen.getByTestId("chat-message-action-bar")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy" }));
    expect(copyTextMock).toHaveBeenCalledWith("brief body");
  });
});
