import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ActorMentionProfileTrigger } from "./actor-mention-profile-trigger";

const mocks = vi.hoisted(() => ({
  openAgentFromContext: null as null | ((id: string) => void),
  openMemberFromContext: null as null | ((id: string) => void),
  openAgentFromStore: vi.fn(),
  closeAgentPanel: vi.fn(),
  openMemberFromStore: vi.fn(),
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (
    selector: (state: { open: typeof mocks.openAgentFromStore; close: typeof mocks.closeAgentPanel }) => unknown,
  ) => selector({ open: mocks.openAgentFromStore, close: mocks.closeAgentPanel }),
}));

vi.mock("@multica/core/workspace", () => ({
  useMemberPanelStore: (
    selector: (state: { open: typeof mocks.openMemberFromStore }) => unknown,
  ) => selector({ open: mocks.openMemberFromStore }),
}));

vi.mock("./agent-panel-context", () => ({
  useOpenAgentPanel: () => mocks.openAgentFromContext,
}));

vi.mock("./member-panel-context", () => ({
  useOpenMemberPanel: () => mocks.openMemberFromContext,
}));

vi.mock("./actor-profile-popover", () => ({
  ActorProfileTrigger: ({
    memberType,
    memberId,
    onClickCapture,
    children,
  }: {
    memberType: string;
    memberId: string;
    onClickCapture: () => void;
    children: ReactNode;
  }) => (
    <button
      type="button"
      data-member-type={memberType}
      data-member-id={memberId}
      onClick={onClickCapture}
    >
      {children}
    </button>
  ),
}));

describe("ActorMentionProfileTrigger", () => {
  beforeEach(() => {
    mocks.openAgentFromContext = null;
    mocks.openMemberFromContext = null;
    mocks.openAgentFromStore.mockReset();
    mocks.closeAgentPanel.mockReset();
    mocks.openMemberFromStore.mockReset();
  });

  it("prefers the local agent panel provider", async () => {
    const user = userEvent.setup();
    const openFromContext = vi.fn();
    mocks.openAgentFromContext = openFromContext;
    render(
      <ActorMentionProfileTrigger actorType="agent" actorId="agent-1">
        @Agent
      </ActorMentionProfileTrigger>,
    );

    await user.click(screen.getByRole("button", { name: "@Agent" }));
    expect(openFromContext).toHaveBeenCalledWith("agent-1");
    expect(mocks.openAgentFromStore).not.toHaveBeenCalled();
  });

  it("falls back to the global agent panel store", async () => {
    const user = userEvent.setup();
    render(
      <ActorMentionProfileTrigger actorType="agent" actorId="agent-2">
        @Agent
      </ActorMentionProfileTrigger>,
    );

    await user.click(screen.getByRole("button", { name: "@Agent" }));
    expect(mocks.openAgentFromStore).toHaveBeenCalledWith("agent-2");
  });

  it("closes the agent panel before opening a member panel", async () => {
    const user = userEvent.setup();
    const openMemberFromContext = vi.fn();
    mocks.openMemberFromContext = openMemberFromContext;
    render(
      <ActorMentionProfileTrigger actorType="member" actorId="member-1">
        @Member
      </ActorMentionProfileTrigger>,
    );

    await user.click(screen.getByRole("button", { name: "@Member" }));
    expect(mocks.closeAgentPanel).toHaveBeenCalledOnce();
    expect(openMemberFromContext).toHaveBeenCalledWith("member-1");
    expect(mocks.openMemberFromStore).not.toHaveBeenCalled();
  });
});
