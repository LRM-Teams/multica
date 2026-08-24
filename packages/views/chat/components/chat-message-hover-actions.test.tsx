/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import {
  CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS,
  ChatMessageHoverShell,
} from "./chat-message-hover-actions";

const copyTextMock = vi.fn();
const getNotePage = vi.fn();
const updateNotePage = vi.fn();
const createNotePage = vi.fn();

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: (text: string) => copyTextMock(text),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getNotePage: (...args: unknown[]) => getNotePage(...args),
    updateNotePage: (...args: unknown[]) => updateNotePage(...args),
    createNotePage: (...args: unknown[]) => createNotePage(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function renderHover(ui: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return renderWithI18n(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("ChatMessageHoverShell", () => {
  beforeEach(() => {
    copyTextMock.mockReset();
    copyTextMock.mockResolvedValue(true);
    getNotePage.mockReset();
    updateNotePage.mockReset();
    createNotePage.mockReset();
    getNotePage.mockResolvedValue({ id: "page-1", content: "Original body" });
    updateNotePage.mockImplementation(async (id: string, body: { content?: string; title?: string }) => ({
      id,
      ...body,
    }));
    createNotePage.mockResolvedValue({ id: "child-1", title: "brief body" });
  });

  it("keeps the Messages hover-overlay classes and hides the bar at rest", () => {
    expect(CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS).toContain("group-hover:opacity-100");
    expect(CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS).toContain("opacity-0");
    expect(CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS).not.toContain("static");
  });

  it("does not render a fixed copy control when disabled", () => {
    renderHover(
      <ChatMessageHoverShell enabled={false} copyTextValue="hello">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    expect(screen.queryByTestId("chat-message-action-bar")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
  });

  it("copies from the hover bar, not a footer slot", async () => {
    const user = userEvent.setup();
    renderHover(
      <ChatMessageHoverShell enabled copyTextValue="brief body">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    expect(screen.getByTestId("chat-message-action-bar")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy" }));
    expect(copyTextMock).toHaveBeenCalledWith("brief body");
  });

  it("uses a compact copy control in the bubble hover bar", () => {
    renderHover(
      <ChatMessageHoverShell enabled copyTextValue="brief body">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    const button = screen.getByRole("button", { name: "Copy" });
    expect(button.className).toMatch(/\bsize-5\b/);
    expect(button.className).not.toMatch(/\bsize-7\b/);
    expect(button.querySelector("svg")?.getAttribute("class") ?? "").toMatch(/\bsize-3\b/);
  });

  it("hides note insert actions when the message is not on a note page", () => {
    renderHover(
      <ChatMessageHoverShell enabled copyTextValue="brief body">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Insert below note" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Insert as child note" })).not.toBeInTheDocument();
  });

  it("places insert-below and insert-child after copy on a note page", () => {
    renderHover(
      <ChatMessageHoverShell enabled copyTextValue="brief body" noteInsertPageId="page-1">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    const buttons = screen.getAllByRole("button");
    expect(buttons.map((button) => button.getAttribute("aria-label"))).toEqual([
      "Copy",
      "Insert below note",
      "Insert as child note",
    ]);
  });

  it("appends the copied text below the current note", async () => {
    const user = userEvent.setup();
    renderHover(
      <ChatMessageHoverShell enabled copyTextValue="brief body" noteInsertPageId="page-1">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    await user.click(screen.getByRole("button", { name: "Insert below note" }));
    await waitFor(() => {
      expect(getNotePage).toHaveBeenCalledWith("page-1");
      expect(updateNotePage).toHaveBeenCalledWith("page-1", {
        content: "Original body\n\n## brief body\n\nbrief body",
      });
    });
  });

  it("creates a child note with the copied text", async () => {
    const user = userEvent.setup();
    renderHover(
      <ChatMessageHoverShell enabled copyTextValue="brief body" noteInsertPageId="page-1">
        <p>body</p>
      </ChatMessageHoverShell>,
    );
    await user.click(screen.getByRole("button", { name: "Insert as child note" }));
    await waitFor(() => {
      expect(createNotePage).toHaveBeenCalledWith({ parent_id: "page-1", title: "brief body" });
      expect(updateNotePage).toHaveBeenCalledWith("child-1", { content: "brief body" });
    });
  });
});
