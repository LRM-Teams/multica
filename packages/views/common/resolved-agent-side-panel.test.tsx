// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@multica/core/api";
import type { Agent } from "@multica/core/types";
import { ResolvedAgentSidePanel } from "./resolved-agent-side-panel";

const toastError = vi.hoisted(() => vi.fn());
const detailState = vi.hoisted(() => ({
  data: undefined as Agent | undefined,
  isPending: false,
  isError: false,
  error: null as unknown,
  isFetched: true,
  enabled: true as boolean | undefined,
}));
const identityState = vi.hoisted(() => ({
  data: undefined as
    | { display_name: string; name: string; profile_access: string }
    | undefined,
  isPending: false,
  isError: false,
  isFetched: true,
}));

vi.mock("sonner", () => ({
  toast: { error: toastError },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/agents", () => ({
  agentDetailOptions: (wsId: string, agentId: string) => ({
    queryKey: ["workspaces", wsId, "agent", agentId],
  }),
  agentPresenceOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "agent-presence"],
  }),
  memberProfileOptions: (wsId: string, memberType: string, memberId: string) => ({
    queryKey: ["workspaces", wsId, "member-profile", memberType, memberId],
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: readonly unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) {
      return {
        data: undefined,
        isPending: false,
        isError: false,
        error: null,
        isFetched: false,
      };
    }
    const key = opts.queryKey;
    if (key[2] === "agent") {
      detailState.enabled = opts.enabled;
      return detailState;
    }
    if (key[2] === "member-profile") return identityState;
    return {
      data: undefined,
      isPending: false,
      isError: false,
      error: null,
      isFetched: true,
    };
  },
}));

vi.mock("../channels/components/agent-side-panel", () => ({
  AgentSidePanel: ({ agent }: { agent: { id: string; name: string; display_name?: string } }) => (
    <div data-testid="agent-side-panel" data-agent-id={agent.id}>
      {agent.display_name || agent.name}
    </div>
  ),
}));

vi.mock("./actor-profile-popover", () => ({
  ActorProfileContent: ({ memberId }: { memberId: string }) => (
    <div data-testid="identity-only-profile">{memberId}</div>
  ),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: {
      profile_popover: {
        close_aria: string;
        no_permission_toast: string;
        agent_unavailable: string;
      };
    }) => string) =>
      selector({
        profile_popover: {
          close_aria: "Close profile",
          no_permission_toast: "You don't have permission to view this member's profile",
          agent_unavailable: "Agent unavailable",
        },
      }),
  }),
}));

function makeAgent(id = "agent-1"): Agent {
  return {
    id,
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "rt-1",
    name: "beckham",
    display_name: "贝克汉姆",
    description: "Group manager",
    instructions: "",
    avatar_url: null,
    runtime_mode: "cloud",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    model: "",
    thinking_level: "",
    owner_id: "owner-1",
    skills: [],
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  } as Agent;
}

describe("ResolvedAgentSidePanel (LRM-292)", () => {
  const onClose = vi.fn();

  beforeEach(() => {
    onClose.mockClear();
    toastError.mockClear();
    detailState.data = undefined;
    detailState.isPending = false;
    detailState.isError = false;
    detailState.error = null;
    detailState.isFetched = true;
    detailState.enabled = true;
    identityState.data = undefined;
    identityState.isPending = false;
    identityState.isError = false;
    identityState.isFetched = true;
  });

  it("always fetches GetAgent (does not gate on ListAgents.find)", () => {
    detailState.data = makeAgent();

    render(
      <ResolvedAgentSidePanel
        agentId="agent-1"
        currentUserId="user-1"
        members={[]}
        onClose={onClose}
      />,
    );

    expect(detailState.enabled).not.toBe(false);
    expect(screen.getByTestId("agent-side-panel")).toHaveAttribute("data-agent-id", "agent-1");
    expect(toastError).not.toHaveBeenCalled();
  });

  it("opens channel-only / group-manager agents via GetAgent 200", () => {
    detailState.data = makeAgent("gm-1");

    render(
      <ResolvedAgentSidePanel
        agentId="gm-1"
        currentUserId="user-1"
        members={[]}
        onClose={onClose}
      />,
    );

    expect(screen.getByTestId("agent-side-panel")).toHaveAttribute("data-agent-id", "gm-1");
    expect(screen.getByTestId("agent-side-panel")).toHaveTextContent("贝克汉姆");
    expect(toastError).not.toHaveBeenCalled();
  });

  it("shows optimistic snapshot name while GetAgent is pending", () => {
    detailState.isPending = true;

    render(
      <ResolvedAgentSidePanel
        agentId="gm-1"
        identitySnapshot={{ display_name: "贝克汉姆" }}
        currentUserId="user-1"
        members={[]}
        onClose={onClose}
      />,
    );

    expect(screen.getByText("贝克汉姆")).toBeInTheDocument();
    expect(screen.queryByTestId("agent-side-panel")).toBeNull();
  });

  it("opens identity-only profile on GetAgent 403 instead of silent no-op", () => {
    detailState.isError = true;
    detailState.error = new ApiError("forbidden", 403, "Forbidden");
    identityState.data = {
      display_name: "Private Agent",
      name: "private-agent",
      profile_access: "identity_only",
    };

    render(
      <ResolvedAgentSidePanel
        agentId="private-1"
        currentUserId="user-1"
        members={[]}
        onClose={onClose}
      />,
    );

    expect(screen.getByTestId("identity-only-profile")).toHaveTextContent("private-1");
    expect(screen.getByText("Private Agent")).toBeInTheDocument();
    expect(toastError).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

});
