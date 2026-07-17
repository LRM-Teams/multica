// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Issue } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ChannelTasksBoard } from "./channel-tasks-board";

// Mock the paginated options source (#562/#684). The board runs a real
// useInfiniteQuery against a real QueryClient, paging by offset; this controlled
// queryFn returns one page per offset so the status grouping + load-more are the
// whole contract. `getNextPageParam` mirrors the real one (offset until total).
const listSourceIssues = vi.fn();
vi.mock("@multica/core/channels", () => ({
  channelIssuesInfiniteOptions: (channelId: string) => ({
    queryKey: ["channel-issues", channelId, "infinite"],
    queryFn: ({ pageParam }: { pageParam: number }) => listSourceIssues(channelId, pageParam),
    initialPageParam: 0,
    getNextPageParam: (
      _last: { issues: Issue[]; total: number },
      allPages: Array<{ issues: Issue[]; total: number }>,
    ) => {
      const loaded = allPages.reduce((sum, page) => sum + page.issues.length, 0);
      const total = allPages[0]?.total ?? 0;
      return loaded < total ? loaded : undefined;
    },
  }),
}));

// The board wraps its cards in an isolated view store + the REAL board card.
// Stub those (store machinery + card render) so this unit stays about the
// board's own contract: status-column grouping + load-more pagination.
vi.mock("@multica/core/issues/stores/view-store", () => ({
  createIssueViewStore: () => ({}),
}));
vi.mock("@multica/core/issues/stores/view-store-context", () => ({
  ViewStoreProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("../../issues/components/board-card", () => ({
  BoardCardContent: ({ issue }: { issue: Issue }) => <span>{issue.title}</span>,
}));
vi.mock("../../issues/components/board-column", () => ({ BOARD_COL_WIDTH: 300 }));
vi.mock("../../issues/components/board-status-dot", () => ({
  BOARD_STATUS_DOT: new Proxy({}, { get: () => "" }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }),
}));

function makeIssue(over: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    workspace_id: "ws-1",
    number: 1,
    identifier: "LRM-1",
    title: "Fix the login bug",
    description: null,
    status: "in_progress",
    priority: "high",
    assignee_type: "member",
    assignee_id: "user-1",
    creator_type: "member",
    creator_id: "user-1",
    parent_issue_id: null,
    project_id: null,
    position: 0,
    start_date: null,
    due_date: null,
    metadata: {},
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  } as Issue;
}

function renderBoard() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <ChannelTasksBoard channelId="chan-1" />
    </QueryClientProvider>,
  );
}

describe("ChannelTasksBoard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("groups the source tasks into status columns, each card linking to the task detail", async () => {
    listSourceIssues.mockResolvedValue({
      issues: [
        makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" }),
        makeIssue({ id: "issue-2", title: "Add dark mode", status: "todo" }),
      ],
      total: 2,
    });
    renderBoard();

    const first = await screen.findByText("Fix the login bug");
    expect(first).toBeInTheDocument();
    expect(screen.getByText("Add dark mode")).toBeInTheDocument();
    // Card links out to the task detail (read-only projection, no inline edit).
    expect(first.closest("a")).toHaveAttribute("href", "/acme/issues/issue-1");
    // Board renders the reused localized status column headers (not raw keys).
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByText("Todo")).toBeInTheDocument();
  });

  it("shows the empty state when the channel has no source tasks", async () => {
    listSourceIssues.mockResolvedValue({ issues: [], total: 0 });
    renderBoard();

    expect(await screen.findByText("No tasks from this channel")).toBeInTheDocument();
  });

  it("pages beyond the first 100 without truncating: load-more appends the next offset page", async () => {
    // First page fills to the cap and reports a larger total → load-more shows.
    const firstPage = Array.from({ length: 100 }, (_, i) =>
      makeIssue({ id: `p1-${i}`, title: `First ${i}`, status: "todo" }),
    );
    listSourceIssues.mockImplementation((_channelId: string, offset: number) =>
      offset === 0
        ? Promise.resolve({ issues: firstPage, total: 101 })
        : Promise.resolve({
            issues: [makeIssue({ id: "p2-0", title: "Second page task", status: "done" })],
            total: 101,
          }),
    );
    renderBoard();

    // "Load more (1 remaining)" — never silently caps at 100.
    const loadMore = await screen.findByRole("button", { name: /Load more/ });
    expect(loadMore).toHaveTextContent("1");
    expect(screen.queryByText("Second page task")).not.toBeInTheDocument();

    await userEvent.click(loadMore);

    // Next offset page is appended and grouped into its own status column.
    expect(await screen.findByText("Second page task")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Load more/ })).not.toBeInTheDocument();
  });
});
