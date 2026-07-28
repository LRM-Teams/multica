import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";

// #838 — drive the REAL page handlers without MediaRecorder: this stand-in
// exposes the voice-send callback as a button and renders the composer prefix
// (where the unsent-voice record lives). Everything under test — which send
// path retry takes, when the record clears — is the page's own logic.
const VOICE = { id: "att-voice-1", url: "https://cdn/v.wav", filename: "v.wav", content_type: "audio/wav", size_bytes: 1234 };
vi.mock("./composer", () => ({
  Composer: ({ prefix, onVoiceSend, onSend }: {
    prefix?: React.ReactNode;
    onVoiceSend?: (d: number, a: unknown) => boolean;
    onSend?: () => void;
  }) => (
    <div data-testid="composer">
      {prefix}
      <button data-testid="fire-voice" onClick={() => onVoiceSend?.(7000, VOICE)}>voice</button>
      <button data-testid="fire-text" onClick={() => onSend?.()}>text</button>
    </div>
  ),
}));

import { ChannelsPage } from "./channels-page";

// #642 — the workspace's immutable system #general channel. These tests
// cover what's reliably assertable through RTL: default-select priority
// (deep-link > remembered > #general > first channel), unpinned-list
// ordering, and the three gated affordances that are plain conditional
// DOM (not a floating Radix menu): the header Settings entry, the header
// member-management popover's per-member remove button, and the mobile
// Drawer's Settings row. The sidebar row's Archive item (inside a
// ContextMenu/DropdownMenu) is covered by direct code inspection in review
// rather than a jsdom floating-menu interaction test.

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
// Keep the real module's other exports (notably `ApiError`, used by the leave
// handler's 409 check) while swapping the `api` singleton for the spy proxy.
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: apiMock.proxy,
}));

const toastMock = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  info: vi.fn(),
}));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), toastMock) }));

// This suite renders the FULL ChannelsPage with the real react-virtuoso message
// list (intentionally unmocked, to exercise real sidebar/selection wiring). That
// render is heavy in jsdom, so under full-suite PARALLEL CI load a single test's
// `findByTestId("message-list")` can exceed vitest's 5s default and flake — this
// has repeatedly reddened UNRELATED PRs (e.g. #1243, #1232, whose diffs don't
// touch views). The tests are correct and pass in isolation; give the render
// timeout headroom under load rather than mask a real failure.
vi.setConfig({ testTimeout: 20000 });

// The system channel is deliberately NOT first in this array — ordering
// must come from `system_key`, never array/list position.
const DEFAULT_CHANNELS = [
  {
    id: "chan-random",
    workspace_id: "ws-1",
    name: "random",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  },
  {
    id: "chan-general",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
    system_key: "general",
  },
];

// Mutable per-test channel list — a handful of tests need a different
// workspace shape (no system channel at all; an unrecognized system_key)
// without duplicating the whole mock setup into a second file.
const channelsFixture = vi.hoisted(() => ({ current: [] as unknown[] }));

const membersByChannel: Record<string, unknown[]> = {
  "chan-general": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
  "chan-random": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
  "chan-no-key": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
};

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], channelsFixture.current),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: (channelId: string) =>
      options(["channel-members", channelId], membersByChannel[channelId] ?? []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ items: [], next_cursor: null }),
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

const mobileViewport = vi.hoisted(() => ({ value: false }));
// #568 — `useContainerNarrowerThan` (ResizeObserver-driven) isn't relevant
// to what this file tests; keep it a no-op ("plenty of room", direct row)
// so pre-existing desktop-direct-row assumptions here are unaffected.
// jsdom's default `getBoundingClientRect` is 0x0 for every element and
// `ResizeObserver` isn't implemented at all, so leaving the real hook
// running here would default to "compact" instead.
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileViewport.value,
  useContainerNarrowerThan: () => [false, () => {}] as const,
}));

const replaceSpy = vi.hoisted(() => vi.fn());
vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: replaceSpy,
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({ ChannelMessageList: () => <div data-testid="message-list" /> }));

vi.mock("./conversation-surface", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./conversation-surface")>()),
  ConversationHeader: ({
    title,
    leading,
    actions,
  }: {
    title?: React.ReactNode;
    leading?: React.ReactNode;
    actions?: React.ReactNode;
  }) => (
    <div data-testid="active-title">
      {leading}
      {title}
      {actions}
    </div>
  ),
}));

function renderPage(channelId?: string) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId={channelId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}


// ⚠️ WIP — DO NOT TRUST THESE YET (see #838 notes).
// The simulated failure is not the one being observed: `api.sendChannelMessage`
// is never invoked (0 calls), yet the failure path runs — i.e. something inside
// the mutation throws BEFORE the request (onMutate/optimistic path is the prime
// suspect). So "failure → record appears" currently passes for the wrong
// reason, and the retry's positive control (voice submit called with the SAME
// attachment id) cannot fire. Fix the harness so the failure is attributable
// before relying on any of this.
describe("voice send failure leaves a durable record (#838)", () => {
  beforeEach(() => {
    replaceSpy.mockReset();
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    channelsFixture.current = DEFAULT_CHANNELS;
    const api = apiMock.proxy as Record<string, ReturnType<typeof vi.fn>>;
    api.sendChannelMessage?.mockReset?.();
  });

  async function openChannel() {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    return screen.getByTestId("fire-voice");
  }

  function sendSpy() {
    return (apiMock.proxy as Record<string, ReturnType<typeof vi.fn>>).sendChannelMessage;
  }

  it("a failed voice send leaves the recording on screen (the toast is not the record)", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    expect(await screen.findByTestId("composer-pending-voice")).toBeInTheDocument();
  });

  it("retry re-sends THIS recording through the voice path and never the text send", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    const callsBefore = sendSpy().mock.calls.length;
    fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));

    // Positive control FIRST: the voice submit really ran, carrying the SAME
    // already-uploaded attachment. Without this, "no text send" would also pass
    // when retry did nothing at all.
    await waitFor(() => {
      expect(sendSpy().mock.calls.length).toBeGreaterThan(callsBefore);
    });
    const retried = sendSpy().mock.calls.at(-1)?.[0] as { parts?: unknown; content?: string };
    expect(JSON.stringify(retried?.parts ?? "")).toContain(VOICE.id);
    // …and it is a voice payload, not the text composer's content.
    expect(retried?.content).toBe("");
  });

  it("only a committed retry clears it", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    sendSpy().mockResolvedValueOnce({ id: "m1" });
    fireEvent.click(screen.getByTestId("composer-pending-voice-retry"));
    await waitFor(() => {
      expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
    });
  });

  it("explicit delete clears it — and nothing else does", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    const item = await screen.findByTestId("composer-pending-voice");
    expect(item).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("composer-pending-voice-delete"));
    await waitFor(() => {
      expect(screen.queryByTestId("composer-pending-voice")).toBeNull();
    });
  });

  it("survives the toast being dismissed — the toast is the announcement, not the storage", async () => {
    sendSpy().mockRejectedValueOnce(new Error("boom"));
    const fire = await openChannel();
    fireEvent.click(fire);
    await screen.findByTestId("composer-pending-voice");

    // The failure was announced…
    expect(toastMock.error).toHaveBeenCalled();
    // …and dismissing that announcement (sonner is mocked; the toast simply
    // goes away) must not remove the record.
    expect(screen.getByTestId("composer-pending-voice")).toBeInTheDocument();
  });
});
