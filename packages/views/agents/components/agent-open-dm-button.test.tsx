// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../locales/en/agents.json";
import enCommon from "../../locales/en/common.json";

const openDMMocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => openDMMocks,
}));

import { AgentOpenDmButton } from "./agent-open-dm-button";

function renderButton(variant: "icon" | "labeled" = "icon") {
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { agents: enAgents, common: enCommon } }}
    >
      <AgentOpenDmButton agentId="agent-1" variant={variant} />
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  openDMMocks.openDM.mockReset();
  openDMMocks.isPending = false;
});

describe("AgentOpenDmButton", () => {
  it("opens/creates a DM for the agent on icon click", () => {
    renderButton("icon");

    fireEvent.click(screen.getByTestId("agent-open-dm-button"));

    expect(openDMMocks.openDM).toHaveBeenCalledWith({
      peer_type: "agent",
      peer_id: "agent-1",
    });
  });

  it("shows a labeled Message CTA on the detail variant", () => {
    renderButton("labeled");

    expect(
      screen.getByRole("button", { name: "Send message" }),
    ).toHaveTextContent("Message");
  });

  it("disables while openDM is pending", () => {
    openDMMocks.isPending = true;
    renderButton("icon");

    expect(screen.getByTestId("agent-open-dm-button")).toBeDisabled();
  });
});
