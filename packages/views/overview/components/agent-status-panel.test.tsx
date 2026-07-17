// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AgentTaskFeedItem } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

// A feed row whose author-sourced instruction carries mention markdown — the
// exact shape that leaked the raw `mention://` URI into the Overview "我的 Agent
// 工作状态" feed. `issue_id: ""` keeps the row on the plain (non-link) branch so
// the test needs no navigation/workspace-route context.
const FEED_ITEM: AgentTaskFeedItem = {
  id: "task-1",
  agent_id: "agent-1",
  issue_id: "",
  status: "completed",
  completed_at: null,
  trigger_summary:
    "Reviewed [@Nash](mention://agent/6ef7ba41-c493-1111-2222-333344445555)'s PR",
};

// Controlled query sources so the panel renders one deterministic row with no
// network layer (mirrors channel-tasks-board.test.tsx).
vi.mock("@multica/core/agents/queries", () => ({
  agentTaskFeedOptions: () => ({
    queryKey: ["agent-task-feed"],
    queryFn: () => ({ tasks: [FEED_ITEM] }),
    initialPageParam: null,
    getNextPageParam: () => undefined,
  }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({
    queryKey: ["agent-list"],
    queryFn: () => [{ id: "agent-1", name: "Wren" }],
  }),
}));
// useWorkspacePaths throws outside a workspace route; stub the one method the
// row uses (never reached here since the row has no issue_id).
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/issues/${id}` }),
}));
// jsdom has no layout, so real Virtuoso computes a 0-height viewport and renders
// nothing; render every item inline instead.
vi.mock("react-virtuoso", () => ({
  Virtuoso: ({
    data,
    itemContent,
  }: {
    data: AgentTaskFeedItem[];
    itemContent: (i: number, item: AgentTaskFeedItem) => React.ReactNode;
  }) => (
    <div>
      {data.map((item, i) => (
        <div key={item.id}>{itemContent(i, item)}</div>
      ))}
    </div>
  ),
}));

import { AgentStatusPanel } from "./agent-status-panel";

describe("AgentStatusPanel", () => {
  it("normalizes mention markdown to a plain @name and never leaks the raw mention:// URI", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={client}>
        <AgentStatusPanel wsId="ws-1" />
      </QueryClientProvider>,
    );

    const description = await screen.findByText(/Reviewed/);
    expect(description.textContent).toContain("@Nash");
    expect(description.textContent).not.toContain("mention://");
  });
});
