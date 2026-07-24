// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Issue, IssueStatus } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ChannelTasksBoard } from "./channel-tasks-board";

// Mock the paginated options source (#562/#684). The board runs a real
// useInfiniteQuery against a real QueryClient, paging by offset; this controlled
// queryFn returns one page per offset so the status grouping + load-more are the
// whole contract. `getNextPageParam` mirrors the real one (offset until total).
const listSourceIssues = vi.fn();
// The channel/group's bound project (#576 follow-up) — "" (or unresolved) means
// unbound. Individual tests override via `mockProjectId`.
let mockProjectId = "";
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
  channelProjectOptions: (_wsId: string, channelId: string) => ({
    queryKey: ["channel-project", channelId],
    queryFn: () => Promise.resolve(mockProjectId),
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// The "whole project" scope (#576 follow-up). `listProjectIssues` is the
// controlled queryFn standing in for the real bucketed `fetchFirstPages` —
// resolving to a flat Issue[] mirrors `myIssueListOptions`'s own
// `select: flattenIssueBuckets`. `mockLoadMoreByStatus` stands in for
// `useLoadMoreByStatus`'s per-column pagination.
const listProjectIssues = vi.fn();
const mockLoadMoreByStatus = vi.fn();
vi.mock("@multica/core/issues/queries", () => ({
  myIssueListOptions: (_wsId: string, scope: string, filter: Record<string, unknown>) => ({
    queryKey: ["my-issues", scope, filter],
    queryFn: () => listProjectIssues(scope, filter),
  }),
}));
vi.mock("@multica/core/issues/mutations", () => ({
  useLoadMoreByStatus: (status: IssueStatus, myIssues?: { scope: string; filter: Record<string, unknown> }) =>
    mockLoadMoreByStatus(status, myIssues),
}));

vi.mock("../../projects/components/project-chip", () => ({
  ProjectChip: ({ projectId }: { projectId: string }) => <span>Project {projectId}</span>,
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
  AppLink: ({ href, children, className }: { href: string; children: React.ReactNode; className?: string }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }),
}));
vi.mock("../../issues/components/priority-icon", () => ({
  PriorityIcon: () => <span data-testid="priority-icon" />,
}));
vi.mock("../../issues/components/status-heading", () => ({
  StatusHeading: ({ status, count }: { status: string; count: number }) => (
    <div>
      <span>List-{status}</span>
      <span>{count}</span>
    </div>
  ),
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));
vi.mock("../../labels/label-chip", () => ({
  LabelChip: ({ label }: { label: { name: string } }) => <span>{label.name}</span>,
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
    mockProjectId = "";
    // Sane default so every column's unconditional `useLoadMoreByStatus` call
    // (group scope passes `projectLoadMore: undefined`, but the hook itself is
    // still called every render) has something to destructure. Tests that care
    // about project-scope per-column pagination override this explicitly.
    mockLoadMoreByStatus.mockReturnValue({ loadMore: vi.fn(), hasMore: false, isLoading: false, total: 0 });
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
    expect(await screen.findByText("No issues")).toBeInTheDocument();
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

  it("shows the empty state when the channel has no source issues", async () => {
    listSourceIssues.mockResolvedValue({ issues: [], total: 0 });
    renderBoard();

    expect(await screen.findByText("No issues in this scope")).toBeInTheDocument();
    expect(screen.getByText(/Issues created from messages in this group/)).toBeInTheDocument();
  });

  it("toolbar always shows List/Board segment; default is Board", async () => {
    listSourceIssues.mockResolvedValue({
      issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
      total: 1,
    });
    renderBoard();

    expect(await screen.findByText("Fix the login bug")).toBeInTheDocument();
    const listBtn = screen.getByRole("button", { name: /^List$/ });
    const boardBtn = screen.getByRole("button", { name: /^Board$/ });
    expect(boardBtn).toHaveAttribute("aria-pressed", "true");
    expect(listBtn).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText("In Progress")).toBeInTheDocument();
  });

  it("switching to List shows compact rows for the same loaded issues (status groups + links); Board stays available", async () => {
    listSourceIssues.mockResolvedValue({
      issues: [
        makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress", identifier: "LRM-1" }),
        makeIssue({ id: "issue-2", title: "Add dark mode", status: "todo", identifier: "LRM-2" }),
      ],
      total: 2,
    });
    renderBoard();

    await screen.findByText("Fix the login bug");
    await userEvent.click(screen.getByRole("button", { name: /^List$/ }));

    expect(screen.getByRole("button", { name: /^List$/ })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: /^Board$/ })).toHaveAttribute("aria-pressed", "false");
    // List rows: both issues visible (no mobile single-column gate), with identifiers.
    expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.getByText("Add dark mode")).toBeInTheDocument();
    expect(screen.getByText("LRM-1")).toBeInTheDocument();
    expect(screen.getByText("LRM-2")).toBeInTheDocument();
    expect(screen.getByText("List-in_progress")).toBeInTheDocument();
    expect(screen.getByText("List-todo")).toBeInTheDocument();
    // Board column shells gone; list rows still link out.
    expect(screen.queryByTestId("board-column-shell")).not.toBeInTheDocument();
    expect(screen.getByText("Fix the login bug").closest("a")).toHaveAttribute("href", "/acme/issues/issue-1");

    // Switch back — board columns return, same data.
    await userEvent.click(screen.getByRole("button", { name: /^Board$/ }));
    expect(await screen.findAllByTestId("board-column-shell")).not.toHaveLength(0);
    expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.getByText("Add dark mode")).toBeInTheDocument();
  });

  it("List view on narrow: no status pill selector — single full-width list", async () => {
    mockIsNarrow = true;
    listSourceIssues.mockResolvedValue({
      issues: [
        makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" }),
        makeIssue({ id: "issue-2", title: "Add dark mode", status: "todo" }),
      ],
      total: 2,
    });
    renderBoard();

    await userEvent.click(await screen.findByRole("button", { name: /^List$/ }));
    expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.getByText("Add dark mode")).toBeInTheDocument();
    // Board mobile pills must not appear in List.
    expect(screen.queryByRole("button", { name: /Todo/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /In Progress/ })).not.toBeInTheDocument();
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

  // #576 follow-up — the scope toggle ("This group" vs "Whole project").
  describe("scope toggle", () => {
    it("no project bound: renders exactly as before — no toggle, no chip", async () => {
      mockProjectId = "";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
        total: 1,
      });
      renderBoard();

      expect(await screen.findByText("Fix the login bug")).toBeInTheDocument();
      expect(screen.queryByText(/This group/)).not.toBeInTheDocument();
      expect(screen.queryByText(/Whole project/)).not.toBeInTheDocument();
    });

    it("project bound: shows the toggle, defaulting to \"This group\" (unchanged channel-scoped issues)", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
        total: 1,
      });
      listProjectIssues.mockResolvedValue([]);
      renderBoard();

      const groupPill = await screen.findByRole("button", { name: "This group" });
      const projectPill = screen.getByRole("button", { name: /Whole project/ });
      expect(groupPill).toHaveAttribute("aria-pressed", "true");
      expect(projectPill).toHaveAttribute("aria-pressed", "false");
      // The project's name rides along as supplementary label text on the
      // "Whole project" option, but isn't itself a separate switching control.
      expect(projectPill).toHaveTextContent("Project proj-1");
      // Default scope shows the channel-scoped (group) issues, unchanged.
      expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    });

    it("keyboard: arrow keys move focus between items via the real ToggleGroup roving-tabindex, and re-activating the already-pressed item never drops to 0 active scopes", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
        total: 1,
      });
      listProjectIssues.mockResolvedValue([]);
      const user = userEvent.setup();
      renderBoard();

      const groupPill = await screen.findByRole("button", { name: "This group" });
      const projectPill = screen.getByRole("button", { name: /Whole project/ });

      // Focus the currently-active item and activate it again (re-click via
      // keyboard) — Base UI's single-select ToggleGroup would emit an EMPTY
      // array here; the guard in `TasksScopeToggle` must keep it pinned to
      // "group" rather than ending up with neither item pressed.
      groupPill.focus();
      expect(groupPill).toHaveFocus();
      await user.keyboard("[Enter]");
      expect(groupPill).toHaveAttribute("aria-pressed", "true");
      expect(projectPill).toHaveAttribute("aria-pressed", "false");
      // Never both unpressed.
      expect(
        groupPill.getAttribute("aria-pressed") === "true" || projectPill.getAttribute("aria-pressed") === "true",
      ).toBe(true);

      // Arrow-right moves FOCUS to the next item via the group's real
      // roving-tabindex composite navigation (not a manual tab-index hack).
      await user.keyboard("[ArrowRight]");
      expect(projectPill).toHaveFocus();

      // Activating the now-focused item switches the pressed scope — exactly
      // one item pressed at a time, never zero.
      await user.keyboard("[Enter]");
      expect(projectPill).toHaveAttribute("aria-pressed", "true");
      expect(groupPill).toHaveAttribute("aria-pressed", "false");
      expect(await screen.findByText("Project proj-1")).toBeInTheDocument();

      // Arrow-left moves focus back.
      await user.keyboard("[ArrowLeft]");
      expect(groupPill).toHaveFocus();
    });

    it("switching to \"Whole project\": swaps to the project-scoped query — list, empty state and count all switch together", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Group-only task", status: "todo" })],
        total: 1,
      });
      listProjectIssues.mockResolvedValue([
        makeIssue({ id: "issue-2", title: "Project-wide task A", status: "todo" }),
        makeIssue({ id: "issue-3", title: "Project-wide task B", status: "in_progress" }),
      ]);
      renderBoard();

      expect(await screen.findByText("Group-only task")).toBeInTheDocument();
      expect(screen.queryByText("Project-wide task A")).not.toBeInTheDocument();

      const projectPill = screen.getByRole("button", { name: /Whole project/ });
      await userEvent.click(projectPill);

      // The group-scoped card disappears; both project-wide cards appear —
      // never a mix of the two scopes in one render.
      expect(await screen.findByText("Project-wide task A")).toBeInTheDocument();
      expect(screen.getByText("Project-wide task B")).toBeInTheDocument();
      expect(screen.queryByText("Group-only task")).not.toBeInTheDocument();
      expect(projectPill).toHaveAttribute("aria-pressed", "true");

      // The status columns reflect the project-scoped set's own counts, not
      // the group-scoped set's.
      expect(screen.getByText("Todo")).toBeInTheDocument();
      expect(screen.getByText("In Progress")).toBeInTheDocument();
    });

    it("\"Whole project\" empty: shows the project-scoped empty state, not the channel one", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Group-only task", status: "todo" })],
        total: 1,
      });
      listProjectIssues.mockResolvedValue([]);
      renderBoard();

      const projectPill = await screen.findByRole("button", { name: /Whole project/ });
      await userEvent.click(projectPill);

      expect(await screen.findByText("No issues in this project")).toBeInTheDocument();
      expect(screen.queryByText("No issues in this scope")).not.toBeInTheDocument();
      expect(screen.queryByText("Group-only task")).not.toBeInTheDocument();
    });

    it("no permission to view the project's issues: the \"Whole project\" option disables itself with a visible reason, not a silent 403", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Fix the login bug", status: "in_progress" })],
        total: 1,
      });
      // The project-scoped query fails (e.g. a 403) — this must surface BEFORE
      // any click, disabling the toggle rather than letting the user tap
      // through into the error.
      listProjectIssues.mockRejectedValue(new Error("forbidden"));
      renderBoard();

      const projectPill = await screen.findByRole("button", { name: /Whole project/ });
      await vi.waitFor(() => expect(projectPill).toBeDisabled());
      expect(screen.getByText("Can't load this project's issues right now")).toBeInTheDocument();
      // Group scope is unaffected — still the active, working view.
      expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    });

    it("switching channels resets the scope back to \"This group\" — a per-channel view, not a sticky global mode", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({
        issues: [makeIssue({ id: "issue-1", title: "Group-only task", status: "todo" })],
        total: 1,
      });
      listProjectIssues.mockResolvedValue([
        makeIssue({ id: "issue-2", title: "Project-wide task", status: "todo" }),
      ]);
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const { rerender } = renderWithI18n(
        <QueryClientProvider client={queryClient}>
          <ChannelTasksBoard channelId="chan-1" />
        </QueryClientProvider>,
      );

      const projectPill = await screen.findByRole("button", { name: /Whole project/ });
      await userEvent.click(projectPill);
      expect(await screen.findByText("Project-wide task")).toBeInTheDocument();

      rerender(
        <QueryClientProvider client={queryClient}>
          <ChannelTasksBoard channelId="chan-2" />
        </QueryClientProvider>,
      );

      // Back on "This group" for the new channel, not stuck on "Whole project".
      const groupPillAfterSwitch = await screen.findByRole("button", { name: "This group" });
      expect(groupPillAfterSwitch).toHaveAttribute("aria-pressed", "true");
      expect(await screen.findByText("Group-only task")).toBeInTheDocument();
    });

    it("archived channel or member-only access: the toggle is driven only by `channelId` — no admin/archived flag gates read visibility", async () => {
      // The component takes no archived/admin prop; the read-only toggle must
      // work purely off the channel id + its resolved project binding, exactly
      // like `ChannelProjectSettingsPanel`'s own read (vs. write) split — only
      // the WRITE picker there is admin-gated, never the read.
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({ issues: [], total: 0 });
      listProjectIssues.mockResolvedValue([
        makeIssue({ id: "issue-2", title: "Project-wide task", status: "todo" }),
      ]);
      renderBoard();

      const projectPill = await screen.findByRole("button", { name: /Whole project/ });
      expect(projectPill).not.toBeDisabled();
      await userEvent.click(projectPill);
      expect(await screen.findByText("Project-wide task")).toBeInTheDocument();
    });

    it("\"Whole project\" scope: a status column with more pages than loaded shows its own per-column Load more (group scope's flat bar does not apply here)", async () => {
      mockProjectId = "proj-1";
      listSourceIssues.mockResolvedValue({ issues: [], total: 0 });
      listProjectIssues.mockResolvedValue([
        makeIssue({ id: "issue-2", title: "Project-wide task", status: "todo" }),
      ]);
      // Simulate the "todo" column having more on the server than loaded.
      const loadMore = vi.fn();
      mockLoadMoreByStatus.mockImplementation((status: IssueStatus) =>
        status === "todo"
          ? { loadMore, hasMore: true, isLoading: false, total: 5 }
          : { loadMore: vi.fn(), hasMore: false, isLoading: false, total: 0 },
      );
      renderBoard();

      const projectPill = await screen.findByRole("button", { name: /Whole project/ });
      await userEvent.click(projectPill);
      await screen.findByText("Project-wide task");

      // "4 remaining" — 5 total minus the 1 already loaded for "todo".
      const columnLoadMore = screen.getByRole("button", { name: /Load more/ });
      expect(columnLoadMore).toHaveTextContent("4");
      await userEvent.click(columnLoadMore);
      expect(loadMore).toHaveBeenCalledTimes(1);
    });
  });
});
