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
});
