import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchD5Rail } from "./research-d5-rail";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        d5: {
          rail: {
            chat_tab: "Fleet chat",
            detail_tab: "Node detail",
            agent_tab: "Agent settings",
            hide: "Hide context rail",
          },
        },
      }),
  }),
}));

describe("ResearchD5Rail accessibility", () => {
  it("exposes tabs and only one visible tabpanel", () => {
    const onModeChange = vi.fn();
    const { rerender } = render(
      <ResearchD5Rail
        id="context"
        mode="chat"
        onModeChange={onModeChange}
        chatPanel={<button>Send</button>}
        detailPanel={<button>Inspect</button>}
        agentPanel={<button>Configure</button>}
        agentAvailable
      />,
    );

    const chatTab = screen.getByRole("tab", { name: "Fleet chat" });
    const detailTab = screen.getByRole("tab", { name: "Node detail" });
    const agentTab = screen.getByRole("tab", { name: "Agent settings" });
    expect(chatTab).toHaveAttribute("aria-selected", "true");
    expect(detailTab).toHaveAttribute("aria-selected", "false");
    expect(agentTab).toHaveAttribute("aria-selected", "false");
    expect(chatTab).toHaveAttribute("tabindex", "0");
    expect(detailTab).toHaveAttribute("tabindex", "-1");
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Send");
    expect(screen.queryByRole("button", { name: "Inspect" })).toBeNull();

    fireEvent.click(detailTab);
    expect(onModeChange).toHaveBeenCalledWith("detail");
    rerender(
      <ResearchD5Rail
        id="context"
        mode="detail"
        onModeChange={onModeChange}
        chatPanel={<button>Send</button>}
        detailPanel={<button>Inspect</button>}
        agentPanel={<button>Configure</button>}
        agentAvailable
      />,
    );
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Inspect");
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();

    fireEvent.click(agentTab);
    expect(onModeChange).toHaveBeenCalledWith("agent");
  });

  it("moves tab focus and selection with the arrow-key pattern", () => {
    const onModeChange = vi.fn();
    render(
      <ResearchD5Rail
        id="context"
        mode="chat"
        onModeChange={onModeChange}
        chatPanel={null}
        detailPanel={null}
      />,
    );
    const chatTab = screen.getByRole("tab", { name: "Fleet chat" });
    const detailTab = screen.getByRole("tab", { name: "Node detail" });
    chatTab.focus();
    fireEvent.keyDown(chatTab, { key: "ArrowRight" });
    expect(onModeChange).toHaveBeenCalledWith("detail");
    expect(document.activeElement).toBe(detailTab);

    fireEvent.keyDown(detailTab, { key: "Home" });
    expect(onModeChange).toHaveBeenLastCalledWith("chat");
    expect(document.activeElement).toBe(chatTab);
  });

  it("disables Agent settings when the selected node has no Agent", () => {
    render(
      <ResearchD5Rail
        id="context"
        mode="detail"
        onModeChange={() => {}}
        chatPanel={null}
        detailPanel={null}
      />,
    );

    expect(
      screen.getByRole("tab", { name: "Agent settings" }),
    ).toBeDisabled();
  });

  it("passes hidden and inert state to the rail ownership surface", () => {
    render(
      <ResearchD5Rail
        id="context"
        mode="chat"
        onModeChange={() => {}}
        chatPanel={<button>Send</button>}
        detailPanel={null}
        aria-hidden
        inert
      />,
    );
    const rail = screen.getByTestId("research-d5-rail");
    expect(rail).toHaveAttribute("aria-hidden", "true");
    expect(rail).toHaveAttribute("inert");
  });
});
