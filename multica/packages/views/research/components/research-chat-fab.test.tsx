import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchChatFab } from "./research-chat-fab";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        panel: {
          chat: "Fleet chat",
          chat_mode: {
            empty: "Idle",
            loading: "Loading",
            running: "In progress",
            error: "Error",
          },
        },
      };
      return picker(keys as never);
    },
  }),
}));

describe("ResearchChatFab", () => {
  it("exposes data-chat-mode for each four-state mode", () => {
    const { rerender } = render(
      <ResearchChatFab mode="empty" onOpen={() => {}} />,
    );
    expect(screen.getByTestId("research-canvas-chat-fab")).toHaveAttribute(
      "data-chat-mode",
      "empty",
    );

    for (const mode of ["loading", "running", "error"] as const) {
      rerender(<ResearchChatFab mode={mode} onOpen={() => {}} />);
      expect(screen.getByTestId("research-canvas-chat-fab")).toHaveAttribute(
        "data-chat-mode",
        mode,
      );
    }
  });
});
