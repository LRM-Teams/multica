import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #309 — path-style channel routing. The selection resolver must reconcile the
// `/channels/[id]` route param into the active conversation WITHOUT fighting an
// in-page click. On web `replace()` commits the route asynchronously, so there
// is a window where the route id still points at the OLD conversation while the
// user has already clicked a new one. A resolver that keys off the (stale) route
// during that window reverts the click — a visible flicker. These tests pin the
// two invariants: cold-load deep-link resolves the named channel, and an
// optimistic click is never reverted by a lagging route.

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
  {
    id: "chan-2",
    workspace_id: "ws-1",
    name: "random",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  },
];

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], CHANNELS),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], []),
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
// #568 — channels-page.tsx also uses `useIsNarrowerThan` for the header
// actions row's own (wider) compact breakpoint; keep it false here so the
// full desktop icon row renders as before (routing tests don't exercise it).
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileViewport.value,
  useIsNarrowerThan: () => false,
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

// Surface the active channel's title unambiguously (the sidebar rows also print
// each channel name, so we read the resolved selection from the header stub).
vi.mock("./conversation-surface", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./conversation-surface")>()),
  ConversationHeader: ({ title, leading }: { title?: React.ReactNode; leading?: React.ReactNode }) => (
    <div data-testid="active-title">
      {leading}
      {title}
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

describe("ChannelsPage path-style selection (#309)", () => {
  beforeEach(() => {
    replaceSpy.mockReset();
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
  });

  it("cold-load deep-link opens the channel named in the route, not the first", async () => {
    renderPage("chan-2");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    await waitFor(() => {
      expect(useLastSelectedChannelStore.getState().lastSelectedChannelId).toBe("chan-2");
    });
  });

  it("does not revert an in-page click while the route param still lags behind", async () => {
    // Land on chan-2 via the route (as a shared link / notification would).
    renderPage("chan-2");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    // Click "general" (chan-1) in the sidebar. On web the route commit is async,
    // so `channelId` stays "chan-2" for now — the resolver must NOT drag the
    // selection back to chan-2.
    const generalRow = screen
      .getAllByRole("button")
      .find((el) => el.textContent?.includes("general"));
    expect(generalRow).toBeTruthy();
    fireEvent.click(generalRow!);

    expect(replaceSpy).toHaveBeenCalledWith("/w/test/channels/chan-1");
    expect(useLastSelectedChannelStore.getState().lastSelectedChannelId).toBe("chan-1");
    // Give any (buggy) revert effect a chance to fire, then assert it stuck.
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    // And it must stay on general across subsequent renders (no flicker back).
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.getByTestId("active-title")).toHaveTextContent("general");
  });

  it("restores the last selected group from the base channels route without flashing the default", async () => {
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: "chan-2" });

    let flashedDefault = false;
    const observer = new MutationObserver(() => {
      flashedDefault ||= screen.queryByTestId("active-title")?.textContent === "general";
    });
    observer.observe(document.body, { childList: true, subtree: true, characterData: true });
    renderPage();

    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    observer.disconnect();
    expect(flashedDefault).toBe(false);
    expect(replaceSpy).toHaveBeenCalledWith("/w/test/channels/chan-2");
  });

  it("drops a stale saved group and keeps the existing default fallback", async () => {
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: "removed-channel" });

    renderPage();

    await waitFor(() => {
      expect(useLastSelectedChannelStore.getState().lastSelectedChannelId).toBeNull();
    });
    expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it("keeps the mobile list open when Back remounts the base route", async () => {
    mobileViewport.value = true;
    const firstRoute = renderPage("chan-2");

    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(replaceSpy).toHaveBeenCalledWith("/w/test/channels");

    // App Router may remount the optional catch-all page when the route loses
    // its `[id]` segment. The base route must honor this explicit Back intent
    // instead of immediately restoring chan-2 from persisted state.
    firstRoute.unmount();
    renderPage();

    await waitFor(() => {
      expect(screen.queryByTestId("active-title")).not.toBeInTheDocument();
    });
    expect(replaceSpy).toHaveBeenCalledTimes(1);
  });
});
