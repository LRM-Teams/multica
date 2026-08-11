import { render, screen } from "@testing-library/react";
import { cloneElement, isValidElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enLayout from "../locales/en/layout.json";
import { AppSidebar } from "./app-sidebar";

const TEST_RESOURCES = { en: { layout: enLayout } };

function renderSidebar() {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AppSidebar />
    </I18nProvider>,
  );
}

const { detail, deletePin, pins, unreadActivity } = vi.hoisted(() => ({
  detail: { current: { isPending: false, isError: false, data: null as unknown, error: null as unknown } },
  deletePin: vi.fn(),
  pins: {
    current: [
      {
        id: "pin-1",
        workspace_id: "ws-1",
        user_id: "user-1",
        item_type: "issue" as const,
        item_id: "issue-1",
        position: 0,
        created_at: "2026-05-06T00:00:00Z",
      },
    ],
  },
  unreadActivity: { current: 0 },
}));

vi.mock("@dnd-kit/core", () => ({
  DndContext: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  PointerSensor: vi.fn(),
  closestCenter: vi.fn(),
  useSensor: vi.fn(),
  useSensors: vi.fn(),
}));
vi.mock("@dnd-kit/sortable", () => ({
  SortableContext: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn() }),
  verticalListSortingStrategy: vi.fn(),
}));
vi.mock("@dnd-kit/utilities", () => ({ CSS: { Transform: { toString: () => undefined } } }));
vi.mock("@multica/ui/components/ui/sidebar", () => ({
  Sidebar: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarFooter: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroupContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroupLabel: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarHeader: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarMenuAction: () => <button type="button" />,
  SidebarMenuButton: ({ children, render }: { children: React.ReactNode; render?: React.ReactElement }) =>
    isValidElement(render) ? cloneElement(render, undefined, children) : <button type="button">{children}</button>,
  SidebarMenuItem: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarRail: () => null,
}));
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: () => null,
  DropdownMenuGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuSeparator: () => null,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
}));
vi.mock("@multica/ui/components/ui/collapsible", () => ({
  Collapsible: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleTrigger: () => <button type="button" />,
}));
vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <button type="button">{children}</button>,
}));
vi.mock("./help-launcher", () => ({ HelpLauncher: () => null }));
vi.mock("../auth/use-logout", () => ({ useLogout: () => vi.fn() }));
vi.mock("../issues/components/status-icon", () => ({ StatusIcon: () => <span /> }));
vi.mock("../navigation/app-link", () => ({
  AppLink: ({ children, href }: { children: React.ReactNode; href: string }) => <a href={href}>{children}</a>,
}));
vi.mock("../navigation/context", () => ({
  useNavigation: () => ({ pathname: "/acme/issues", push: vi.fn() }),
}));
vi.mock("../projects/components/project-icon", () => ({ ProjectIcon: () => <span /> }));
vi.mock("../workspace/workspace-avatar", () => ({ WorkspaceAvatar: () => <span /> }));
vi.mock("@multica/ui/components/common/actor-avatar", () => ({ ActorAvatar: () => <span /> }));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/paths", () => ({
  paths: { workspace: (slug: string) => ({ issues: () => `/${slug}/issues` }) },
  useCurrentWorkspace: () => ({ id: "ws-1", name: "Acme", slug: "acme" }),
  useWorkspacePaths: () => ({
    overview: () => "/acme/overview",
    inbox: () => "/acme/inbox",
    myIssues: () => "/acme/my-issues",
    issues: () => "/acme/issues",
    projects: () => "/acme/projects",
    research: () => "/acme/research",
    channels: () => "/acme/channels",
    members: () => "/acme/members",
    usage: () => "/acme/usage",
    evolution: () => "/acme/evolution",
    notes: () => "/acme/notes",
    wiki: () => "/acme/wiki",
    planBilling: () => "/acme/plan-billing",
    computers: () => "/acme/computers",
    sandboxes: () => "/acme/sandboxes",
    skills: () => "/acme/skills",
    settings: () => "/acme/settings",
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
    researchDetail: (id: string) => `/acme/research/${id}`,
  }),
}));
vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getBaseUrl: () => "http://127.0.0.1:8080",
    },
  };
});
vi.mock("@multica/core/user-activity/queries", () => ({
  useUserActivityUnreadCount: () => unreadActivity.current,
  userActivityListOptions: (wsId: string, tab: string) => ({
    queryKey: ["user-activity", wsId, "list", tab],
    queryFn: async () => ({ items: [] }),
  }),
}));
vi.mock("@multica/core/issues/queries", () => ({ issueDetailOptions: () => ({ queryKey: ["issue"] }) }));
vi.mock("@multica/core/issues/stores/create-mode-store", () => ({
  useCreateModeStore: { getState: () => ({ lastMode: "agent" }) },
  openCreateIssueWithPreference: vi.fn(),
}));
vi.mock("@multica/core/issues/stores/draft-store", () => ({ useIssueDraftStore: () => false }));
vi.mock("@multica/core/modals", () => ({ useModalStore: { getState: () => ({ modal: null, open: vi.fn() }) } }));
vi.mock("@multica/core/pins/mutations", () => ({ useDeletePin: () => ({ mutate: deletePin }), useReorderPins: () => ({ mutate: vi.fn() }) }));
vi.mock("@multica/core/pins/queries", () => ({ pinListOptions: () => ({ queryKey: ["pins"] }) }));
vi.mock("@multica/core/projects/queries", () => ({ projectDetailOptions: () => ({ queryKey: ["project"] }) }));
vi.mock("@multica/core/workspace/queries", () => ({
  myInvitationListOptions: () => ({ queryKey: ["invitations"] }),
  workspaceKeys: { myInvitations: () => ["invitations"] },
  workspaceListOptions: () => ({ queryKey: ["workspaces"] }),
}));
vi.mock("@tanstack/react-query", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-query")>()),
  useMutation: () => ({ isPending: false, mutate: vi.fn() }),
  useQuery: ({ queryKey }: { queryKey: readonly unknown[] }) => {
    if (queryKey[0] === "pins") return { data: pins.current };
    if (queryKey[0] === "issue") return detail.current;
    return { data: [] };
  },
  useQueryClient: () => ({ fetchQuery: vi.fn(), invalidateQueries: vi.fn() }),
}));

describe("PinRow", () => {
  beforeEach(() => {
    deletePin.mockReset();
    detail.current = { isPending: false, isError: false, data: null, error: null };
  });



  it("renders loaded details", async () => {
    detail.current = { isPending: false, isError: false, data: { identifier: "MUL-123", title: "Keep this pin", status: "todo" }, error: null };
    renderSidebar();
    expect(await screen.findByText("MUL-123 Keep this pin")).toBeInTheDocument();
  });
});

describe("AppSidebar navigation", () => {
  beforeEach(() => {
    detail.current = { isPending: false, isError: false, data: null, error: null };
    unreadActivity.current = 0;
  });

  it("renders one personal Messages entry directly below Activity", () => {
    renderSidebar();
    const activity = screen.getByText("Activity");
    const messages = screen.getByText("Messages");
    const overview = screen.getByText("Overview");

    expect(screen.getAllByText("Messages")).toHaveLength(1);
    expect(messages.closest("a")).toHaveAttribute("href", "/acme/channels");
    // Personal entry order stays Activity → Messages → Workspace / Overview.
    expect(
      activity.compareDocumentPosition(messages) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      messages.compareDocumentPosition(overview) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("does not render the New Issue row (removed from the sidebar)", () => {
    renderSidebar();
    expect(screen.queryByText("New Issue")).toBeNull();
  });

  it("does not render Autopilot nav (LRM-1050 hard-cut)", () => {
    renderSidebar();
    expect(screen.queryByText("Autopilot")).toBeNull();
    expect(screen.queryByText("Autopilots")).toBeNull();
  });

  it("does not render the My Issues shortcut row (removed; app leads with Messages)", () => {
    renderSidebar();
    expect(screen.queryByText("My Issues")).toBeNull();
    // Activity stays in the top personal section.
    expect(screen.getByText("Activity")).toBeInTheDocument();
  });

  it("renders Activity unread as a brand pill (LRM-354 contrast)", () => {
    unreadActivity.current = 3;
    const { container } = renderSidebar();
    const badge = container.querySelector("span.bg-brand-solid.text-brand-solid-foreground");
    expect(badge).not.toBeNull();
    expect(badge).toHaveTextContent("3");
  });
});
