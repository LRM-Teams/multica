// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AgentProfileMessageButton } from "./agent-profile-message-button";

const openDMMocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => openDMMocks,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

const RESOURCES = {
  side_panel: {
    message_button: "Message",
    message_opening: "Opening…",
  },
};

describe("AgentProfileMessageButton (LRM-283 / LRM-351)", () => {
  beforeEach(() => {
    openDMMocks.openDM.mockReset();
    openDMMocks.isPending = false;
  });

  it("renders full-width primary message button with send icon", () => {
    render(<AgentProfileMessageButton agentId="agent-1" />);
    const btn = screen.getByTestId("agent-profile-message-button");
    expect(btn).toHaveTextContent("Message");
    expect(btn).not.toBeDisabled();
    // LRM-351: theme primary tokens — not Slack light-only hex fills.
    expect(btn.className).toMatch(/bg-primary/);
    expect(btn.className).toMatch(/text-primary-foreground/);
    expect(btn.className).not.toMatch(/#f4f4f4/);
    expect(btn.className).not.toMatch(/rgba\(29,\s*28,\s*29/);
  });

  it("opens or creates DM for the agent on click", () => {
    render(<AgentProfileMessageButton agentId="agent-42" />);
    fireEvent.click(screen.getByTestId("agent-profile-message-button"));
    expect(openDMMocks.openDM).toHaveBeenCalledWith({
      peer_type: "agent",
      peer_id: "agent-42",
    });
  });

  it("shows loading copy and blocks re-click while pending", () => {
    openDMMocks.isPending = true;
    render(<AgentProfileMessageButton agentId="agent-1" />);
    const btn = screen.getByTestId("agent-profile-message-button");
    expect(btn).toHaveTextContent("Opening…");
    expect(btn).toBeDisabled();
  });
});
