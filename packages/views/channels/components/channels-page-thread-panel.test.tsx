import { act, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// B2 (#240 / #198) — the [[integration-wiring-blindspot]] discipline: B3 shipped
// broken because a component tested green in isolation while the PARENT never
// wired it. <ThreadPanel> unit-tests green on its own, so this asserts the real
// ChannelsPage actually MOUNTS it and feeds it the #251 read-model off the
// thread root — the participant chips + wake strip must render from the root
// message's thread_participants / thread_wake_annotations, NOT by handing props
// to ThreadPanel directly. Removing the mount wiring makes this fail.

const apiMock = vi.hoisted(() => {
  const known: Record<string, unknown> = {};
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

// Keep the real channel hooks (so follow / thread-read really route through the
// api), but stub the query options to fixtures so the page resolves one active
// channel with no network.
vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const channel = {
    id: "chan-1",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  };
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], [channel]),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    // The thread reply query resolves to no replies — the participant chips and
    // wake strip come from the root message, not the replies.
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ messages: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

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

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

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

vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));

// Mock the message list so we can capture the main pane's onOpenThread (the
// entry point that opens the thread) — the thread panel's own list never gets
// one (flat, no nesting), so we keep the first non-null we see. The mock renders
// nothing structural, so the participant chips / wake strip we assert on can
// only come from ThreadPanel's own DOM, proving it is what mounted.
const listCapture = vi.hoisted(() => ({
  onOpenThread: null as ((m: ChannelMessage) => void) | null,
}));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: (props: { onOpenThread?: (m: ChannelMessage) => void }) => {
    if (props.onOpenThread) listCapture.onOpenThread = props.onOpenThread;
    return <div data-testid="message-list" />;
  },
}));

function threadRootMessage(): ChannelMessage {
  return {
    id: "root-1",
    channel_id: "chan-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "Root question",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
    thread_followed: false,
    thread_participants: [
      { key: "user:user-1", member_type: "user", member_id: "user-1", name: "Alice", display_name: "Alice", followed: false },
      { key: "agent:agent-9", member_type: "agent", member_id: "agent-9", name: "Iris", display_name: "Iris", followed: true },
    ],
    thread_wake_annotations: [
      // Agent, no_reply → neutral "why no reply" annotation with a reason.
      { key: "agent:agent-9", member_type: "agent", member_id: "agent-9", display_name: "Iris", state: "no_reply", reason: "received, nothing to add" },
      // Human record → must NOT surface as woken.
      { key: "user:user-1", member_type: "user", member_id: "user-1", display_name: "Alice", state: "pending" },
    ],
  };
}

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
        <ChannelsPage />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

async function openThread() {
  renderPage();
  await screen.findByTestId("message-list");
  await waitFor(() => expect(listCapture.onOpenThread).toBeTypeOf("function"));
  await act(async () => {
    listCapture.onOpenThread?.(threadRootMessage());
  });
  return screen.findByTestId("thread-participants");
}

describe("ChannelsPage → ThreadPanel mount wiring (#240 B2)", () => {
  beforeEach(() => {
    listCapture.onOpenThread = null;
  });

  it("mounts ThreadPanel and renders participant chips from the root's thread_participants (#251)", async () => {
    const chips = await openThread();
    // ThreadPanel's own strip — the old inline thread render had no chips, so
    // this rendering proves the mount is live.
    expect(within(chips).getByText("Alice")).toBeInTheDocument();
    expect(within(chips).getByText("Iris")).toBeInTheDocument();
  });

  it("renders a neutral 'why no reply' wake annotation for the agent, and none for the human (#196)", async () => {
    await openThread();
    const strip = await screen.findByTestId("thread-wake-strip");
    // Agent no_reply → neutral, with the reason summary.
    expect(within(strip).getByText("No reply")).toBeInTheDocument();
    expect(within(strip).getByText(/received, nothing to add/)).toBeInTheDocument();
    const row = strip.querySelector('[data-wake-state="no_reply"]');
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText("No reply").className).toContain("bg-muted");
    // The human participant is never surfaced as woken.
    expect(within(strip).queryByText("Alice")).not.toBeInTheDocument();
  });

  it("does not render the also-send checkbox (#256 cut this round)", async () => {
    await openThread();
    expect(
      screen.queryByRole("checkbox", { name: "Also send to channel" }),
    ).not.toBeInTheDocument();
  });
});
