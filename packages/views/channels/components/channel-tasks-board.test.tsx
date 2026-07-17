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
// Lightweight stand-ins for the shared presentational pieces (#3b): the real
// editable `BoardColumn` pulls in dnd-kit / view-store / modals, so we mock the
// module. `BoardStatusHeading` still uses the real `useT` so the localized
// status header ("In Progress" / "Todo") is exercised end-to-end, and
// `BoardColumnShell` renders heading + body like the real shell.
vi.mock("../../issues/components/board-column", async () => {
  const { useT } = await import("../../i18n");
  return {
    BOARD_COL_WIDTH: 300,
    BoardColumnShell: ({
      heading,
      actions,
      children,
      widthClassName,
    }: {
      heading: React.ReactNode;
      actions?: React.ReactNode;
      children: React.ReactNode;
      widthClassName?: string;
    }) => (
      // Faithful to the real shell, which applies `widthClassName` to its root
      // div — so the board's responsive width class contract stays observable.
      <div data-testid="board-column-shell" className={widthClassName}>
        {heading}
        {actions}
        {children}
      </div>
    ),
    BoardStatusHeading: ({ status, count }: { status: string; count: number }) => {
      const { t } = useT("issues");
      return (
        <div>
          <span>{t(($) => ($.status as Record<string, string>)[status] ?? status)}</span>
          <span>{count}</span>
        </div>
      );
    },
  };
});
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
// The mobile layout is a JS-breakpoint branch (selector + single column) vs the
// desktop horizontal columns, so the board-local `useIsNarrow` (≤768px, #685
// closure) is the switch. jsdom has no real viewport/matchMedia, so drive it
// directly. Default false = desktop; true = the ≤768 segmented layout.
let mockIsNarrow = false;
vi.mock("../hooks/use-is-narrow", () => ({
  useIsNarrow: () => mockIsNarrow,
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
    mockIsNarrow = false;
    // jsdom doesn't implement scrollIntoView; the board calls it on the active
    // pill's ref. Stub it so the pill-into-view effect can run + be asserted.
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("desktop (>768px): renders every status column side by side", async () => {
    listSourceIssues.mockResolvedValue({
      issues: [
        makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" }),
        makeIssue({ id: "issue-2", title: "Add dark mode", status: "todo" }),
      ],
      total: 2,
    });
    renderBoard();

    // Both columns' cards are visible at once — no selector, no single-column gate.
    expect(await screen.findByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.getByText("Add dark mode")).toBeInTheDocument();
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByText("Todo")).toBeInTheDocument();
    // No mobile segmented control on desktop.
    expect(screen.queryByRole("button", { name: /Todo/ })).not.toBeInTheDocument();
  });

  it("narrow (≤768px): a pill per status (empty ones too); selecting an empty status shows its empty state; switching pills swaps the visible column", async () => {
    mockIsNarrow = true;
    listSourceIssues.mockResolvedValue({
      issues: [
        makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" }),
        makeIssue({ id: "issue-2", title: "Add dark mode", status: "todo" }),
      ],
      total: 2,
    });
    renderBoard();

    // A pill for EVERY status the desktop board shows (Iris ruling) — including
    // the empty ones — in BOARD_STATUSES order, each with its count.
    const todoPill = await screen.findByRole("button", { name: /Todo/ });
    const inProgressPill = screen.getByRole("button", { name: /In Progress/ });
    const backlogPill = screen.getByRole("button", { name: /Backlog/ });
    for (const label of ["Backlog", "Todo", "In Progress", "In Review", "Done", "Blocked"]) {
      expect(screen.getByRole("button", { name: new RegExp(label) })).toBeInTheDocument();
    }
    // The empty statuses still get a pill, showing a `0` count.
    expect(backlogPill).toHaveTextContent("0");

    // The default selection is the first status that HAS issues in BOARD_STATUSES
    // order (todo before in_progress), so only its card is visible.
    expect(screen.getByText("Add dark mode")).toBeInTheDocument();
    expect(screen.queryByText("Fix the login bug")).not.toBeInTheDocument();
    expect(todoPill).toHaveAttribute("aria-pressed", "true");

    // Switching the selector swaps the visible column: the old status's card
    // disappears and the newly-selected status's card appears.
    await userEvent.click(inProgressPill);
    expect(await screen.findByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.queryByText("Add dark mode")).not.toBeInTheDocument();
    expect(inProgressPill).toHaveAttribute("aria-pressed", "true");

    // Selecting an EMPTY status renders its empty state (no cards) below.
    await userEvent.click(backlogPill);
    expect(await screen.findByText("No tasks")).toBeInTheDocument();
    expect(screen.queryByText("Fix the login bug")).not.toBeInTheDocument();
    expect(screen.queryByText("Add dark mode")).not.toBeInTheDocument();
    expect(backlogPill).toHaveAttribute("aria-pressed", "true");
  });

  it("narrow: scrolls the default-selected pill into view within the pill row (#685 closure nit)", async () => {
    mockIsNarrow = true;
    listSourceIssues.mockResolvedValue({
      // Default selection is the first NON-empty status (in_progress here), which
      // can sit off-screen to the right in the horizontally-scrolled pill row.
      issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
      total: 1,
    });
    renderBoard();

    const activePill = await screen.findByRole("button", { name: /In Progress/ });
    expect(activePill).toHaveAttribute("aria-pressed", "true");
    // The active pill (only it) is brought into view, centered in its scroller —
    // not the page (block: "nearest").
    expect(activePill.scrollIntoView).toHaveBeenCalledWith({ inline: "center", block: "nearest" });
  });

  it("column width uses the min-[769px]: breakpoint that agrees with useIsNarrow — never md: (≥768 would snap to 300px at 768 while JS says narrow)", async () => {
    // The width class is static (the responsive prefix does the switching, not
    // JS), so its contract is identical narrow or wide. Assert it directly: base
    // full-width + the desktop 300px gated at >768, and crucially NO `md:` — a
    // `md:`/≥768 reintroduction would disagree with `useIsNarrow` (≤768) at 768.
    mockIsNarrow = true;
    listSourceIssues.mockResolvedValue({
      issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
      total: 1,
    });
    renderBoard();

    await screen.findByText("Fix the login bug");
    const shell = screen.getByTestId("board-column-shell");
    expect(shell).toHaveClass("w-full");
    expect(shell).toHaveClass("min-[769px]:w-[300px]");
    expect(shell.className).not.toMatch(/(?:^|\s|:)md:/);
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
