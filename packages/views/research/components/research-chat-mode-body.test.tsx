import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchChatModeBody, ResearchChatModeChip } from "./research-chat-mode-body";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        panel: {
          chat_mode: {
            empty: "Idle",
            loading: "Loading",
            running: "In progress",
            error: "Error",
          },
        },
        chat: {
          empty_title: "No dialogue yet",
          empty_body: "Empty body",
          loading_body: "Connecting…",
          error_title: "Could not send",
          error_body: "Error body",
        },
        session_page: { retry: "Retry" },
      };
      return picker(keys as never);
    },
  }),
}));

describe("ResearchChatModeBody / Chip", () => {
  it("renders designed empty / loading / error bodies", () => {
    const { rerender } = render(<ResearchChatModeBody mode="empty" />);
    expect(screen.getByTestId("research-chat-empty")).toBeInTheDocument();
    expect(screen.getByText("No dialogue yet")).toBeInTheDocument();

    rerender(<ResearchChatModeBody mode="loading" />);
    expect(screen.getByTestId("research-chat-loading")).toBeInTheDocument();

    rerender(<ResearchChatModeBody mode="error" onRetry={() => {}} />);
    expect(screen.getByTestId("research-chat-error")).toBeInTheDocument();
    expect(screen.getByText("Retry")).toBeInTheDocument();
  });

  it("chip exposes data-chat-mode", () => {
    render(<ResearchChatModeChip mode="running" />);
    expect(screen.getByTestId("research-chat-mode")).toHaveAttribute(
      "data-chat-mode",
      "running",
    );
  });

  // The chat feed flips loading -> running inside one mount, so the announcement
  // must live on a node that survives every mode change (same fix shape as the
  // M2 aux cards and the canvas live region). A live region that only exists on
  // the loading branch is unmounted exactly when the content becomes ready.
  it("keeps one persistent live region across every mode flip", () => {
    const { rerender } = render(<ResearchChatModeChip mode="loading" />);
    const live = screen.getByTestId("research-chat-mode-live");
    // Native <output> carries the status role (react-doctor prefer-tag-over-role).
    expect(live.tagName).toBe("OUTPUT");
    expect(live).toHaveAttribute("aria-live", "polite");
    expect(live).toHaveAttribute("aria-busy", "true");
    expect(live).toHaveTextContent("Loading");

    rerender(<ResearchChatModeChip mode="running" />);
    // Same DOM node — a remount would reset the live region and swallow the
    // ready announcement.
    expect(screen.getByTestId("research-chat-mode-live")).toBe(live);
    expect(live).toHaveAttribute("aria-busy", "false");
    expect(live).toHaveTextContent("In progress");

    rerender(<ResearchChatModeChip mode="error" />);
    expect(screen.getByTestId("research-chat-mode-live")).toBe(live);
    expect(live).toHaveTextContent("Error");
  });

  it("does not duplicate the mode announcement on the loading body", () => {
    render(<ResearchChatModeBody mode="loading" />);
    const loading = screen.getByTestId("research-chat-loading");
    // aria-busy stays (it describes the region), but the announcement is owned
    // by the persistent chip region — two live regions would double-speak.
    expect(loading).toHaveAttribute("aria-busy");
    expect(loading).not.toHaveAttribute("aria-live");
  });
});
