import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiError } from "@multica/core/api";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

/**
 * #832 — the page decides which role failures may be retried.
 *
 * The gap this closes: `owner_changed` and `gone` were covered at BOTH ends and
 * nowhere in the middle.
 *   - core `role-change-failure.test.ts` proves error → kind;
 *   - `channel-members-list-role-pending.test.tsx` proves that a descriptor
 *     WITHOUT `onRetry` renders no Retry button — but the test hands it that
 *     descriptor directly.
 * Nothing drove the page from a real failure to the descriptor, and the page is
 * where the decision lives (`channels-page.tsx`, `retryable = kind ===
 * "transient"`). Relaxing that line to `!== "forbidden"`, or to `true`, left
 * the entire core + views suite green.
 *
 * Same shape as the transfer seam in #1367: two self-consistent halves, and the
 * joint between them untested.
 *
 * These render the real page, so the real `classifyRoleChangeFailure` →
 * `roleFailures` → `roleFailureFor` chain runs; only the surrounding chrome is
 * stubbed. Copy comes from the real `en/channels.json`, not a mock dictionary —
 * a hand-written dictionary drifts and renders "" instead of failing.
 *
 * HOW TO FLIP-VERIFY: widen `retryable` in channels-page.tsx (e.g. `kind !==
 * "forbidden"`) → the owner_changed and gone cases go red while the transient
 * case stays green. That asymmetry is the point: a guard that reddens for every
 * mutation is not discriminating between these kinds.
 */

const apiMock = vi.hoisted(() => {
  const updateChannelMemberRole = vi.fn();
  const known: Record<string, unknown> = { updateChannelMemberRole };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, updateChannelMemberRole };
});
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: apiMock.proxy,
}));

vi.mock("sonner", () => ({
  toast: Object.assign(vi.fn(), {
    info: vi.fn(),
    error: vi.fn(),
    success: vi.fn(),
    warning: vi.fn(),
  }),
}));

const CHANNELS = [
  {
    id: "chan-1",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  },
];

// The viewer is the group owner, so the management menu is offered; `bob` is an
// ordinary member, so `promote` is available on his row.
const MEMBERS = [
  {
    channel_id: "chan-1",
    member_id: "user-1",
    member_type: "user" as const,
    display_name: "Alice",
    role: "owner" as const,
    joined_at: "2026-06-17T09:00:00Z",
  },
  {
    channel_id: "chan-1",
    member_id: "user-2",
    member_type: "user" as const,
    display_name: "Bob",
    role: "member" as const,
    joined_at: "2026-06-17T09:00:00Z",
  },
];

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], CHANNELS),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], MEMBERS),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ messages: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"], queryFn: async () => [] }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "Alice" } }),
}));

vi.mock("@multica/core/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/hooks")>()),
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    channels: () => "/w/test/channels",
    channelDetail: (id: string) => `/w/test/channels/${id}`,
  }),
}));

vi.mock("@multica/core/realtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

vi.mock("@multica/core/dm", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/dm")>()),
  dmListOptions: () => ({ queryKey: ["dm-list"], queryFn: async () => [] }),
}));

vi.mock("@multica/core/workspace/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workspace/queries")>()),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  useContainerNarrowerThan: () => [false, vi.fn()] as const,
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));

// Chrome only. The roster reaches the details panel as `membersBody`, which is
// built by the page — rendering it directly keeps the real ChannelMembersList
// and the real role-failure wiring under test.
vi.mock("./channel-details-panel", () => ({
  ChannelDetailsPanel: (props: { membersBody?: React.ReactNode }) => (
    <div data-testid="details-panel">{props.membersBody}</div>
  ),
}));

vi.mock("./channel-agents-live-cue", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./channel-agents-live-cue")>()),
  ChannelPresenceCluster: (props: { onOpenMembers?: () => void }) => (
    <button type="button" onClick={props.onOpenMembers}>
      open-members
    </button>
  ),
}));

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({ ChannelMessageList: () => <div /> }));

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId="chan-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

/** Open the roster and fire "promote" on Bob's row. */
async function promoteBob() {
  renderPage();
  fireEvent.click(await screen.findByText("open-members"));
  // Exactly one row offers a menu: the viewer's own row never does, so this is
  // Bob's. Asserted rather than indexed blindly — if that ever changes, this
  // fails here instead of silently driving the wrong row.
  const triggers = await screen.findAllByLabelText("Member actions");
  expect(triggers).toHaveLength(1);
  fireEvent.click(triggers[0]!);
  fireEvent.click(await screen.findByTestId("group-member-menu-promote"));
}

describe("ChannelsPage — which role failures offer Retry (#832)", () => {
  beforeEach(() => {
    apiMock.updateChannelMemberRole.mockReset();
  });

  it("owner_changed: shows its own message and NO retry — the roster moved, repeating cannot help", async () => {
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("someone else took ownership", 403, "Forbidden", { code: "owner_changed" }),
    );

    await promoteBob();

    expect(
      await screen.findByText("Ownership has changed; the member list has been refreshed."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("channel-members-row-role-retry")).toBeNull();
  });

  it("gone: shows its own message and NO retry — the target is no longer there", async () => {
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("not found", 404, "Not Found"),
    );

    await promoteBob();

    expect(
      await screen.findByText("The member or channel state has changed. Refresh and try again."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("channel-members-row-role-retry")).toBeNull();
  });

  it("transient: DOES offer retry — without this the other two prove nothing", async () => {
    // The positive control. If the page stopped offering Retry entirely, the two
    // assertions above would still pass while the feature was silently dead.
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("upstream boom", 503, "Service Unavailable"),
    );

    await promoteBob();

    expect(
      await screen.findByText("Couldn't update the member's role. Please try again."),
    ).toBeInTheDocument();
    expect(screen.getByTestId("channel-members-row-role-retry")).toBeInTheDocument();
  });

  it("retry re-issues the SAME action, not a default one", async () => {
    apiMock.updateChannelMemberRole.mockRejectedValue(
      new ApiError("upstream boom", 503, "Service Unavailable"),
    );

    await promoteBob();
    await screen.findByTestId("channel-members-row-role-retry");
    apiMock.updateChannelMemberRole.mockClear();
    fireEvent.click(screen.getByTestId("channel-members-row-role-retry"));

    await waitFor(() => expect(apiMock.updateChannelMemberRole).toHaveBeenCalled());
    // promote → "manager"; a retry that sent "member" would be a demotion the
    // user never asked for.
    expect(apiMock.updateChannelMemberRole).toHaveBeenCalledWith(
      "chan-1",
      "user",
      "user-2",
      "manager",
    );
  });
});
