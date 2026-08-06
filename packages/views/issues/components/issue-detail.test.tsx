import { forwardRef, useRef, useState, useImperativeHandle } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Issue, TimelineEntry } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

const mockViewport = vi.hoisted(() => ({ isMobile: false }));
const mockNavigation = vi.hoisted(() => ({
  push: vi.fn(),
  pathname: "/issues/issue-1",
  searchParams: new URLSearchParams(),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mockViewport.isMobile,
}));

// useWorkspaceId() derives from useCurrentWorkspace (relative import inside
// @multica/core/hooks.tsx). vi.mock("@multica/core/paths") only intercepts
// the bare-specifier, not the internal relative import. Mock the hooks module
// directly so the bridge hook returns the test UUID.
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// Mock @multica/core/auth
const mockAuthUser = { id: "user-1", email: "test@test.com", name: "Test User" };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: any) => {
      const state = { user: mockAuthUser, isAuthenticated: true };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ user: mockAuthUser, isAuthenticated: true }) },
  ),
  registerAuthStore: vi.fn(),
  createAuthStore: vi.fn(),
}));

// Mock @multica/core/workspace/hooks
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getMemberName: (id: string) => (id === "user-1" ? "Test User" : "Unknown"),
    getAgentName: (id: string) => (id === "agent-1" ? "Claude Agent" : "Unknown Agent"),
    getActorName: (type: string, id: string) => {
      if (type === "member" && id === "user-1") return "Test User";
      if (type === "agent" && id === "agent-1") return "Claude Agent";
      return "Unknown";
    },
    getActorInitials: (type: string) => (type === "member" ? "TU" : "CA"),
    getActorAvatarUrl: () => null,
  }),
}));

// Mock workspace queries
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["workspaces", "ws-1", "members"],
    queryFn: () => Promise.resolve([{ user_id: "user-1", name: "Test User", email: "test@test.com", role: "admin" }]),
  }),
  // LRM-391: ActivityActorName / comment authors may hit member-profiles on
  // directory miss. Always return null (never undefined) for React Query.
  memberProfileOptions: (_wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", "ws-1", "member-profiles", type, id],
    queryFn: () => Promise.resolve(null),
  }),
  agentListOptions: () => ({
    queryKey: ["workspaces", "ws-1", "agents"],
    queryFn: () => Promise.resolve([]),
  }),
  assigneeFrequencyOptions: () => ({
    queryKey: ["workspaces", "ws-1", "assignee-frequency"],
    queryFn: () => Promise.resolve([]),
  }),
  workspaceListOptions: () => ({
    queryKey: ["workspaces"],
    queryFn: () => Promise.resolve([{ id: "ws-1", name: "Test WS", slug: "test" }]),
  }),
}));

// Mock @multica/core/paths — after the URL-driven workspace refactor,
// useCurrentWorkspace / useWorkspacePaths derive from the workspace slug in
// URL Context. Tests don't mount a real route, so we short-circuit to fixtures.
vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useCurrentWorkspace: () => ({ id: "ws-1", name: "Test WS", slug: "test" }),
    useWorkspacePaths: () => actual.paths.workspace("test"),
  };
});

// Mock navigation
vi.mock("../../navigation", () => ({
  AppLink: ({ children, href, ...props }: any) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
  useNavigation: () => ({
    ...mockNavigation,
    getShareableUrl: (p: string) => `https://app.multica.com${p}`,
  }),
  NavigationProvider: ({ children }: { children: React.ReactNode }) => children,
}));

// Mock editor components (Tiptap requires real DOM)
vi.mock("../../editor", () => ({
  useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
  FileDropOverlay: () => null,
  // No-op so comment-card's AttachmentList can render without hitting the
  // real API singleton; tests that care about download wiring should write
  // dedicated specs against `use-download-attachment.test.tsx`.
  useDownloadAttachment: () => vi.fn(),
  // Inert preview hook — comment-card's AttachmentList uses it to gate the
  // Eye button. Dedicated coverage lives in attachment-preview-modal.test.tsx.
  useAttachmentPreview: () => ({
    open: vi.fn(),
    tryOpen: () => false,
    modal: null,
  }),
  isPreviewable: () => false,
  ReadonlyContent: ({ content }: { content: string }) => (
    <div data-testid="readonly-content">{content}</div>
  ),
  ContentEditor: forwardRef(function MockContentEditor(
    { defaultValue, onUpdate, placeholder }: any,
    ref: any,
  ) {
    const valueRef = useRef(defaultValue || "");
    const [value, setValue] = useState(defaultValue || "");
    useImperativeHandle(ref, () => ({
      getMarkdown: () => valueRef.current,
      clearContent: () => { valueRef.current = ""; setValue(""); },
      focus: () => {},
      uploadFile: () => {},
    }));
    return (
      <textarea
        value={value}
        onChange={(e) => {
          valueRef.current = e.target.value;
          setValue(e.target.value);
          onUpdate?.(e.target.value);
        }}
        placeholder={placeholder}
        data-testid="rich-text-editor"
      />
    );
  }),
  TitleEditor: forwardRef(function MockTitleEditor(
    { defaultValue, placeholder, onBlur, onChange }: any,
    ref: any,
  ) {
    const valueRef = useRef(defaultValue || "");
    const [value, setValue] = useState(defaultValue || "");
    useImperativeHandle(ref, () => ({
      getText: () => valueRef.current,
      focus: () => {},
    }));
    return (
      <input
        value={value}
        onChange={(e) => {
          valueRef.current = e.target.value;
          setValue(e.target.value);
          onChange?.(e.target.value);
        }}
        onBlur={() => onBlur?.(valueRef.current)}
        placeholder={placeholder}
        data-testid="title-editor"
      />
    );
  }),
}));

// Mock common components
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorType, actorId }: any) => (
    <span data-testid="actor-avatar">
      {actorType}:{actorId}
    </span>
  ),
}));

vi.mock("../../projects/components/project-picker", () => ({
  ProjectPicker: () => <span data-testid="project-picker">Project</span>,
}));

// Mock api
const mockApiObj = vi.hoisted(() => ({
  getIssue: vi.fn(),
  listTimeline: vi.fn().mockResolvedValue([]),
  listComments: vi.fn().mockResolvedValue([]),
  createComment: vi.fn(),
  updateComment: vi.fn(),
  deleteComment: vi.fn(),
  deleteIssue: vi.fn(),
  updateIssue: vi.fn(),
  listIssueSubscribers: vi.fn().mockResolvedValue([]),
  subscribeToIssue: vi.fn().mockResolvedValue(undefined),
  unsubscribeFromIssue: vi.fn().mockResolvedValue(undefined),
  getActiveTasksForIssue: vi.fn().mockResolvedValue({ tasks: [] }),
  listTasksByIssue: vi.fn().mockResolvedValue([]),
  listTaskMessages: vi.fn().mockResolvedValue([]),
  listChildIssues: vi.fn().mockResolvedValue({ issues: [] }),
  listIssues: vi.fn().mockResolvedValue({ issues: [], total: 0 }),
  uploadFile: vi.fn(),
  listIssueReactions: vi.fn().mockResolvedValue([]),
  addIssueReaction: vi.fn(),
  removeIssueReaction: vi.fn(),
  listAttachments: vi.fn().mockResolvedValue([]),
  addCommentReaction: vi.fn(),
  removeCommentReaction: vi.fn(),
  listMembers: vi.fn().mockResolvedValue([{ user_id: "user-1", name: "Test User", email: "test@test.com", role: "admin" }]),
  listAgents: vi.fn().mockResolvedValue([]),
  getProject: vi.fn(),
  listProjects: vi.fn().mockResolvedValue({ projects: [] }),
}));

vi.mock("@multica/core/api", () => ({
  api: mockApiObj,
  getApi: () => mockApiObj,
  setApiInstance: vi.fn(),
}));

// Mock issue config
vi.mock("@multica/core/issues/config", () => ({
  ALL_STATUSES: ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
  BOARD_STATUSES: ["backlog", "todo", "in_progress", "in_review", "done", "blocked"],
  STATUS_ORDER: ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
  STATUS_CONFIG: {
    backlog: { label: "Backlog", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
    todo: { label: "Todo", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
    in_progress: { label: "In Progress", iconColor: "text-warning", hoverBg: "hover:bg-warning/10" },
    in_review: { label: "In Review", iconColor: "text-success", hoverBg: "hover:bg-success/10" },
    done: { label: "Done", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
    blocked: { label: "Blocked", iconColor: "text-destructive", hoverBg: "hover:bg-destructive/10" },
    cancelled: { label: "Cancelled", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
  },
  PRIORITY_ORDER: ["urgent", "high", "medium", "low", "none"],
  PRIORITY_CONFIG: {
    urgent: { label: "Urgent", bars: 4, color: "text-destructive", badgeBg: "bg-destructive/10", badgeText: "text-destructive" },
    high: { label: "High", bars: 3, color: "text-warning", badgeBg: "bg-warning/10", badgeText: "text-warning" },
    medium: { label: "Medium", bars: 2, color: "text-warning", badgeBg: "bg-warning/10", badgeText: "text-warning" },
    low: { label: "Low", bars: 1, color: "text-muted-foreground", badgeBg: "bg-muted", badgeText: "text-muted-foreground" },
    none: { label: "No priority", bars: 0, color: "text-muted-foreground", badgeBg: "bg-muted", badgeText: "text-muted-foreground" },
  },
}));

// Mock recent issues store
const mockRecordVisit = vi.fn();
vi.mock("@multica/core/issues/stores", () => ({
  useRecentIssuesStore: Object.assign(
    (selector?: any) => {
      const state = { byWorkspace: {}, recordVisit: mockRecordVisit, pruneWorkspaces: vi.fn() };
      return selector ? selector(state) : state;
    },
    {
      getState: () => ({
        byWorkspace: {},
        recordVisit: mockRecordVisit,
        pruneWorkspaces: vi.fn(),
      }),
    },
  ),
  selectRecentIssues: () => () => [],
  useCommentCollapseStore: (selector?: any) => {
    const state = {
      collapsedByIssue: {},
      isCollapsed: () => false,
      toggle: () => {},
    };
    return selector ? selector(state) : state;
  },
  useCommentDraftStore: Object.assign(
    (selector?: any) => {
      const state = {
        drafts: {} as Record<string, { content: string; updatedAt: number }>,
        getDraft: () => undefined,
        setDraft: () => {},
        clearDraft: () => {},
      };
      return selector ? selector(state) : state;
    },
    {
      getState: () => ({
        drafts: {} as Record<string, { content: string; updatedAt: number }>,
        getDraft: () => undefined,
        setDraft: () => {},
        clearDraft: () => {},
      }),
    },
  ),
}));

// Mock react-virtuoso: jsdom has no real layout, so the real Virtuoso would
// compute a 0-height viewport and render nothing. The mock renders every item
// inline so id="comment-..." nodes are always present in the DOM — this
// matches the production cold-path where `initialItemCount` force-mounts
// items[0..targetIdx], giving the deep-link effect a real target node.
//
// scrollIntoViewSpy: the deep-link effect no longer calls native
// scrollIntoView (it drives the timeline container's scrollTop directly to
// avoid scrolling ancestor overflow:hidden boxes — see issue-detail.tsx). We
// keep a no-op stub on the prototype so any stray scrollIntoView call from
// other components doesn't throw; deep-link tests assert the highlight ring
// instead, which is mechanism-independent and observable without layout.
const scrollIntoViewSpy = vi.hoisted(() => vi.fn());

vi.mock("react-virtuoso", () => ({
  Virtuoso: forwardRef(function MockVirtuoso(
    { data, itemContent }: { data: unknown[]; itemContent: (i: number, item: unknown) => unknown },
    ref: any,
  ) {
    useImperativeHandle(ref, () => ({
      // Real Virtuoso ref methods are not exercised by tests in this file
      // since the deep-link cold-path drives the container's scrollTop on the
      // real DOM node, not Virtuoso's imperative API.
      scrollIntoView: vi.fn(),
      scrollToIndex: vi.fn(),
    }));
    return (
      <div data-testid="virtuoso-mock">
        {data.map((item, i) => (
          <div key={i}>{itemContent(i, item) as React.ReactElement}</div>
        ))}
      </div>
    );
  }),
}));

// jsdom's HTMLElement.prototype.scrollIntoView is a no-op stub; replace it
// with a spy so the deep-link effect's call can be observed.
beforeEach(() => {
  scrollIntoViewSpy.mockClear();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    writable: true,
    value: scrollIntoViewSpy,
  });
});

// Mock modals
vi.mock("@multica/core/modals", () => ({
  useModalStore: Object.assign(
    () => ({ open: vi.fn() }),
    { getState: () => ({ open: vi.fn() }) },
  ),
}));

// Mock core/hooks/use-file-upload
vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn().mockResolvedValue("https://example.com/file.png") }),
}));

// Mock realtime
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
  useWSReconnect: vi.fn(),
  useWS: () => ({ subscribe: vi.fn(() => () => {}), onReconnect: vi.fn(() => () => {}) }),
  WSProvider: ({ children }: { children: React.ReactNode }) => children,
  useRealtimeSync: () => {},
}));

// Mock sonner
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

// Mock react-resizable-panels (used by @multica/ui/components/ui/resizable)
vi.mock("react-resizable-panels", () => ({
  Group: ({ children, ...props }: any) => <div data-testid="panel-group" {...props}>{children}</div>,
  Panel: ({ children, ...props }: any) => <div data-testid="panel" {...props}>{children}</div>,
  Separator: ({ children, ...props }: any) => <div data-testid="panel-handle" {...props}>{children}</div>,
  useDefaultLayout: () => ({ defaultLayout: undefined, onLayoutChanged: vi.fn() }),
  usePanelRef: () => ({ current: { isCollapsed: () => false, expand: vi.fn(), collapse: vi.fn() } }),
}));

// ---------------------------------------------------------------------------
// Test data
// ---------------------------------------------------------------------------

const mockIssue: Issue = {
  id: "issue-1",
  workspace_id: "ws-1",
  number: 1,
  identifier: "TES-1",
  title: "Implement authentication",
  description: "Add JWT auth to the backend",
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
  due_date: "2026-06-01T00:00:00Z",
  metadata: {},
  created_at: "2026-01-15T00:00:00Z",
  updated_at: "2026-01-20T00:00:00Z",
};

const mockTimeline: TimelineEntry[] = [
  {
    type: "comment",
    id: "comment-1",
    actor_type: "member",
    actor_id: "user-1",
    content: "Started working on this",
    parent_id: null,
    created_at: "2026-01-16T00:00:00Z",
    updated_at: "2026-01-16T00:00:00Z",
    comment_type: "comment",
  },
  {
    type: "comment",
    id: "comment-2",
    actor_type: "agent",
    actor_id: "agent-1",
    content: "I can help with this",
    parent_id: null,
    created_at: "2026-01-17T00:00:00Z",
    updated_at: "2026-01-17T00:00:00Z",
    comment_type: "comment",
  },
];

// ---------------------------------------------------------------------------
// Import component under test (after mocks)
// ---------------------------------------------------------------------------

import { IssueDetail } from "./issue-detail";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

function renderIssueDetail(issueId = "issue-1") {
  const queryClient = createTestQueryClient();
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <IssueDetail issueId={issueId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// ReadonlyContent bodies that belong to the TIMELINE (message bubbles), not the
// issue description. The description's reading surface (#538) also renders a
// ReadonlyContent, so the message-vs-activity discrimination must exclude it.


// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("IssueDetail (shared)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockViewport.isMobile = false;
    mockNavigation.pathname = "/issues/issue-1";
    mockNavigation.searchParams = new URLSearchParams();
    // Default: issue loads successfully
    mockApiObj.getIssue.mockResolvedValue(mockIssue);
    // /timeline returns the entries flat in chronological order (oldest first).
    mockApiObj.listTimeline.mockResolvedValue(mockTimeline);
    mockApiObj.listIssueReactions.mockResolvedValue([]);
    mockApiObj.listIssueSubscribers.mockResolvedValue([]);
    mockApiObj.listChildIssues.mockResolvedValue({ issues: [] });
    mockApiObj.listIssues.mockResolvedValue({ issues: [], total: 0 });
    mockApiObj.getActiveTasksForIssue.mockResolvedValue({ tasks: [] });
    mockApiObj.listTasksByIssue.mockResolvedValue([]);
    mockApiObj.listMembers.mockResolvedValue([
      { user_id: "user-1", name: "Test User", email: "test@test.com", role: "admin" },
    ]);
    mockApiObj.listAgents.mockResolvedValue([]);
    // Reset project mock — individual tests override per case. Default fixture
    // has project_id: null so getProject is not invoked.
    mockApiObj.getProject.mockReset();
  });

  it("shows loading skeleton while data is loading", () => {
    // Make the API hang to keep loading state
    mockApiObj.getIssue.mockReturnValue(new Promise(() => {}));
    const { container } = renderIssueDetail();

    // Skeleton is aria-hidden (decorative); query DOM slot, not a11y roles.
    expect(container.querySelector('[data-slot="skeleton"]')).toBeTruthy();
  });



  it("renders the issue title leaf as a link to the issue detail page", async () => {
    renderIssueDetail();

    // The breadcrumb leaf is the whole "identifier + title" string wrapped in a
    // single link to the issue's own detail route (used to open the full page
    // from the inline Inbox pane). A bare issue has no ancestor crumbs.
    const leaf = await screen.findByText("TES-1 Implement authentication");
    expect(leaf.closest("a")).toHaveAttribute("href", "/test/issues/issue-1");
  });

  it("keeps the Messages return control in the visible mobile navigation slot", async () => {
    mockViewport.isMobile = true;
    mockNavigation.searchParams = new URLSearchParams({
      returnTo: "/test/channels/channel-1?message=message-1",
    });

    renderIssueDetail();

    const returnButtons = await screen.findAllByRole("button", {
      name: "Back to Messages",
    });
    const mobileReturn = returnButtons.find((button) =>
      button.className.includes("md:hidden"),
    );
    const desktopReturn = returnButtons.find((button) =>
      button.className.includes("md:inline-flex"),
    );
    const leaf = await screen.findByText("TES-1 Implement authentication");

    expect(mobileReturn).toHaveClass("md:hidden", "shrink-0", "min-h-11");
    expect(desktopReturn).toHaveClass("hidden", "md:inline-flex");
    expect(mobileReturn?.compareDocumentPosition(leaf) ?? 0).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  it("keeps an honest Issues-list return in the same mobile navigation slot without a Messages origin", async () => {
    mockViewport.isMobile = true;

    renderIssueDetail();

    const returnButtons = await screen.findAllByRole("button", {
      name: "Back to Issues",
    });
    const mobileReturn = returnButtons.find((button) => button.className.includes("md:hidden"));
    const desktopReturn = returnButtons.find((button) => button.className.includes("md:inline-flex"));

    expect(mobileReturn).toHaveClass("min-h-11", "shrink-0", "md:hidden");
    expect(desktopReturn).toHaveClass("hidden", "md:inline-flex");

    fireEvent.click(mobileReturn!);
    expect(mockNavigation.push).toHaveBeenCalledWith("/test/issues");
  });

  it("omits the project breadcrumb segment when the issue has no project_id", async () => {
    // Default fixture has project_id: null.
    renderIssueDetail();

    // Leaf renders once loaded; a bare issue has no ancestor crumbs at all.
    await screen.findByText("TES-1 Implement authentication");

    // Project is never fetched and no project crumb appears.
    expect(mockApiObj.getProject).not.toHaveBeenCalled();
    expect(screen.queryByText("Marketing site refresh")).not.toBeInTheDocument();
  });

  it("renders the project breadcrumb segment when the issue belongs to a project", async () => {
    mockApiObj.getIssue.mockResolvedValue({ ...mockIssue, project_id: "p-1" });
    mockApiObj.getProject.mockResolvedValue({
      id: "p-1",
      workspace_id: "ws-1",
      title: "Marketing site refresh",
      description: null,
      icon: "🚀",
      status: "in_progress",
      priority: "none",
      lead_type: null,
      lead_id: null,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      issue_count: 0,
      done_count: 0,
      resource_count: 0,
    });

    renderIssueDetail();

    const projectLink = await screen.findByText("Marketing site refresh");
    // The whole project segment is a single AppLink pointing at the project
    // detail route under the active workspace slug.
    expect(projectLink.closest("a")).toHaveAttribute("href", "/test/projects/p-1");
  });












  // #243 / E1 — execution-result isolation: a real comment renders as a
  // reactable Message bubble; a status_change / progress_update / system row
  // renders as an Activity entry (never a Message row, never an empty row,
  // never reactable). Message bodies flow through the mocked ReadonlyContent
  // (data-testid="readonly-content"); Activity rows render their label as
  // plain text, so the presence/absence of readonly-content discriminates the
  // two lanes cleanly.
  describe("execution-result isolation (#243 / E1)", () => {



    it("renders an explicit link to the Activity run when the entry carries a run pointer", async () => {
      mockApiObj.listTimeline.mockResolvedValue([
        {
          type: "comment",
          id: "exec-ptr",
          actor_type: "agent",
          actor_id: "agent-1",
          content: "Build complete",
          parent_id: null,
          created_at: "2026-01-16T00:00:00Z",
          updated_at: "2026-01-16T00:00:00Z",
          comment_type: "progress_update",
          details: { run_id: "run-9" },
        },
      ]);

      renderIssueDetail();

      const link = await screen.findByRole("link", { name: /activity/i });
      // Explicit pointer to the agent's Activity run surface (#236), carrying
      // the run id — never rendered as an empty message row.
      expect(link).toHaveAttribute("href", expect.stringContaining("/agents/agent-1"));
      expect(link.getAttribute("href")).toContain("run-9");
    });
  });







  // LRM-1123 — Properties "Associated group" reads top-level `issue.channel`
  // (BE LRM-1122 mirrors source_refs.channel). Never derive display from
  // source_refs alone (LRM-238).
  describe("associated group property (LRM-1123)", () => {
    it("shows the group name when top-level channel is present", async () => {
      mockApiObj.getIssue.mockResolvedValue({
        ...mockIssue,
        channel: {
          channel_id: "chan-9",
          channel_name: "调研模块开发",
          channel_kind: "group",
        },
        // Nested copy may also be present; UI must still prefer top-level.
        source_refs: {
          channel: {
            channel_id: "chan-9",
            channel_name: "调研模块开发",
            channel_kind: "group",
          },
        },
      });

      renderIssueDetail();

      expect(await screen.findByText("Associated group")).toBeInTheDocument();
      expect(screen.getByText("调研模块开发")).toBeInTheDocument();
      expect(screen.queryByText("No associated group")).not.toBeInTheDocument();
    });

    it("shows empty label when top-level channel is absent (does not fall back to source_refs.message)", async () => {
      mockApiObj.getIssue.mockResolvedValue({
        ...mockIssue,
        channel: null,
        source_refs: {
          message: {
            channel_id: "chan-9",
            channel_name: "product",
            channel_kind: "group",
            message_id: "msg-42",
            thread_root_message_id: "msg-42",
            excerpt: "we should file an issue",
          },
        },
      });

      renderIssueDetail();

      // Properties associated-group trigger stays empty; provenance row may still
      // show #product under "From discussion" — that is a different field.
      expect(await screen.findByText("No associated group")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "No associated group" })).toHaveAttribute(
        "title",
        "No associated group",
      );
    });
  });

  // #470 — "From discussion" back-jump: a chat-originated issue shows a
  // lightweight provenance row linking to the source message. The server sends
  // `source_refs.message` only when the viewer can see the channel, so a present
  // ref is always safe to render + link.
  describe("from-discussion source row (#470)", () => {
    const sourceRef = {
      channel_id: "chan-9",
      channel_name: "product",
      channel_kind: "group",
      message_id: "msg-42",
      thread_root_message_id: "msg-42",
      excerpt: "we should file an issue for the login bug",
    };

    it("links 'From discussion' to the source channel message with the excerpt + channel name", async () => {
      mockApiObj.getIssue.mockResolvedValue({
        ...mockIssue,
        source_refs: { message: sourceRef },
      });

      renderIssueDetail();

      const label = await screen.findByText("From discussion");
      expect(label.closest("a")).toHaveAttribute(
        "href",
        "/test/channels/chan-9?message=msg-42",
      );
      // Channel name (group channels only) + excerpt read as provenance context.
      expect(screen.getByText("#product")).toBeInTheDocument();
      expect(
        screen.getByText("we should file an issue for the login bug"),
      ).toBeInTheDocument();
    });

    it("omits the channel name for a source without one (dm) but still links + shows excerpt", async () => {
      mockApiObj.getIssue.mockResolvedValue({
        ...mockIssue,
        source_refs: {
          message: { ...sourceRef, channel_kind: "dm", channel_name: undefined },
        },
      });

      renderIssueDetail();

      const label = await screen.findByText("From discussion");
      expect(label.closest("a")).toHaveAttribute(
        "href",
        "/test/channels/chan-9?message=msg-42",
      );
      expect(screen.queryByText("#product")).not.toBeInTheDocument();
      expect(
        screen.getByText("we should file an issue for the login bug"),
      ).toBeInTheDocument();
    });

  });
});
