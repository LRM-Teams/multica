import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { UserActivityListResponse, UserActivityTab } from "@multica/core/types";
import { userActivityKeys } from "@multica/core/user-activity/queries";
import { InboxPage } from "./inbox-page";

const nav = vi.hoisted(() => ({
  searchParams: new URLSearchParams(),
  replace: vi.fn(),
  push: vi.fn(),
}));

const listUserActivity = vi.hoisted(() =>
  vi.fn(async (): Promise<UserActivityListResponse> => ({ items: [] })),
);

vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    searchParams: nav.searchParams,
    replace: nav.replace,
    push: nav.push,
    pathname: "/ws/inbox",
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    inbox: () => "/ws/inbox",
    issueDetail: (id: string) => `/ws/issues/${id}`,
    channelDetail: (id: string) => `/ws/channels/${id}`,
  }),
}));

vi.mock("@multica/core/user-activity/queries", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@multica/core/user-activity/queries")>();
  return {
    ...mod,
    userActivityListOptions: (wsId: string, tab: UserActivityTab) => ({
      ...mod.userActivityListOptions(wsId, tab),
      queryFn: () => listUserActivity(),
    }),
    useUserActivityUnreadCount: () => 0,
  };
});

vi.mock("@multica/core/user-activity/mutations", () => ({
  useMarkAllUserActivityRead: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@multica/core/inbox/mutations", () => ({
  useMarkInboxRead: () => ({ mutate: vi.fn() }),
  useArchiveInbox: () => ({ mutate: vi.fn() }),
}));

vi.mock("@multica/core/channels/mutations", () => ({
  useMarkChannelThreadRead: () => ({ mutate: vi.fn() }),
}));

vi.mock("../../channels/components/channels-page", () => ({
  ChannelsPage: ({ channelId }: { channelId: string }) => (
    <div data-testid="mock-channels-page">channel:{channelId}</div>
  ),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("react-resizable-panels", () => ({
  useDefaultLayout: () => ({ defaultLayout: undefined, onLayoutChanged: vi.fn() }),
}));

vi.mock("@multica/ui/components/ui/resizable", () => ({
  ResizablePanelGroup: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="panel-group">{children}</div>
  ),
  ResizablePanel: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  ResizableHandle: () => null,
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (bundle: Record<string, unknown>) => unknown,
      vars?: { count?: number },
    ) => {
      const types = new Proxy(
        {},
        { get: (_t, prop) => String(prop) },
      ) as Record<string, string>;
      const bundle = {
        page: { title: "Activity", back: "Back" },
        menu: { mark_all_read: "Mark all as read" },
        list: {
          time: {
            just_now: "just now",
            minutes: `${vars?.count ?? 0}m`,
            hours: `${vars?.count ?? 0}h`,
            days: `${vars?.count ?? 0}d`,
          },
        },
        detail: { select_prompt: "Select a notification", archive: "Archive" },
        types,
        errors: {
          mark_read_failed: "mark read failed",
          archive_failed: "archive failed",
          mark_all_read_failed: "mark all failed",
        },
        activity: {
          tabs_label: "tabs",
          tabs: { all: "All", unread: "Unread", mentions: "Mentions" },
          new_count: `${vars?.count ?? 0} new`,
          replies: `${vars?.count ?? 0} replies`,
          access_denied: "No access",
          load_failed: "load failed",
          retry: "Retry",
          open_thread_failed: "open thread failed",
          open_item_failed: "open item failed",
          open_in_channels: "Open in channel",
          empty: {
            all: { title: "Empty all", description: "desc" },
            unread: { title: "Empty unread", description: "desc" },
            mentions: { title: "Empty mentions", description: "desc" },
          },
        },
      };
      return selector(bundle);
    },
  }),
  Time: ({ value }: { kind: string; value: string }) => <span>{value}</span>,
}));

function renderInbox(qc?: QueryClient) {
  const client =
    qc ??
    new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Infinity },
      },
    });
  return {
    qc: client,
    ...render(
      <QueryClientProvider client={client}>
        <InboxPage />
      </QueryClientProvider>,
    ),
  };
}

describe("InboxPage entry paint (LRM-424)", () => {
  beforeEach(() => {
    nav.searchParams = new URLSearchParams();
    nav.replace.mockReset();
    nav.push.mockReset();
    listUserActivity.mockReset();
    listUserActivity.mockImplementation(
      () =>
        new Promise(() => {
          /* hang — cold network */
        }),
    );
  });

  it("paints real Activity shell (header + tabs) with list skeletons while cold", async () => {
    renderInbox();

    expect(await screen.findByRole("heading", { name: "Activity" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "All" })).toBeInTheDocument();
    expect(screen.getByTestId("activity-list-skeleton")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mark all as read" })).toBeInTheDocument();
  });

  it("re-enter with cache shows rows immediately (no list skeleton)", async () => {
    const cached: UserActivityListResponse = {
      items: [
        {
          kind: "thread",
          id: "root-1",
          workspace_id: "ws-1",
          channel_id: "ch-1",
          channel_name: "general",
          channel_kind: "channel",
          updated_at: new Date().toISOString(),
          unread_count: 1,
          preview_text: "hi",
          title: "Cached thread",
          access_denied: false,
          thread_root_message_id: "root-1",
          reply_count: 1,
        },
      ],
    };
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    qc.setQueryData(userActivityKeys.list("ws-1", "all"), cached);
    listUserActivity.mockImplementation(
      () =>
        new Promise(() => {
          /* hang refresh */
        }),
    );

    renderInbox(qc);

    expect(await screen.findByText("Cached thread")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-list-skeleton")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Activity" })).toBeInTheDocument();
  });

  it("shows explicit error state on cold failure (no silent empty)", async () => {
    listUserActivity.mockRejectedValueOnce(new Error("boom"));
    renderInbox();

    await waitFor(() => {
      expect(screen.getByText("boom")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Activity" })).toBeInTheDocument();
  });
});

describe("InboxPage unread session deep-link (LRM-388)", () => {
  beforeEach(() => {
    nav.searchParams = new URLSearchParams();
    nav.replace.mockReset();
    nav.push.mockReset();
    listUserActivity.mockReset();
  });

  it("keeps thread session pane when Unread feed no longer lists the row", async () => {
    // Simulate post-click Unread: URL already deep-linked, feed empty after mark-read.
    nav.searchParams = new URLSearchParams(
      "tab=unread&channel=ch-1&thread=root-1",
    );
    listUserActivity.mockResolvedValue({ items: [] });

    renderInbox();

    expect(await screen.findByTestId("activity-session-pane")).toBeInTheDocument();
    // Suspense may still be resolving ChannelsPage; the pane itself proves we
    // did not clear the deep-link back to the empty select prompt.
    expect(screen.queryByText("Select a notification")).not.toBeInTheDocument();
    expect(nav.replace).not.toHaveBeenCalled();
  });
});
