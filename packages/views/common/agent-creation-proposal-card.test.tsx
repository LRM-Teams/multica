// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentCreationProposalCard } from "./agent-creation-proposal-card";

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: string[] }) =>
    options.queryKey?.includes("members")
      ? { data: [{ user_id: "owner-1", role: "owner" }] }
      : { data: [], isLoading: false },
  useQueryClient: () => ({ setQueryData: vi.fn(), invalidateQueries: vi.fn() }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (state: { user: { id: string } }) => unknown) =>
    select({ user: { id: "owner-1" } }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
  workspaceKeys: { agents: (id: string) => ["agents", id] },
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
}));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ actorProfile: (_type: string, id: string) => `/acme/profile/agent/${id}` }),
}));
vi.mock("../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock("../agents/components/create-agent-dialog", () => ({ CreateAgentDialog: () => null }));
vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (catalog: { windy: Record<string, unknown> }) => unknown) => {
      const selected = selector({
        windy: {
          hiring_card_badge: "Proposal",
          card_created: ({ name }: { name: string }) => `Created: ${name}`,
          view_created_agent: "View created Agent",
        },
      });
      return typeof selected === "function" ? selected({ name: "Proposal Agent" }) : selected;
    },
  }),
}));

describe("AgentCreationProposalCard", () => {
  it("links a completed proposal to the created Agent and exposes no Create button", () => {
    render(
      <AgentCreationProposalCard
        proposal={{
          message_id: "message-1",
          name: "Proposal Agent",
          description: "",
          status: "executed",
          result_agent_id: "agent-42",
        }}
      />,
    );

    expect(screen.queryByRole("button", { name: /create agent/i })).toBeNull();
    expect(screen.getByRole("link", { name: "View created Agent" })).toHaveAttribute(
      "href",
      "/acme/profile/agent/agent-42",
    );
  });
});
