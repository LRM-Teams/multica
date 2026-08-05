// @vitest-environment jsdom

import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// LRM-459 — CHANNELS sidebar list paints row skeletons while pending, then
// real rows. No empty-state flash on cold load.

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

const channelsFixture = vi.hoisted(() => ({
  resolver: null as null | (() => Promise<unknown[]>),
}));

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => ({
      queryKey: ["channels", "lrm-459"],
      queryFn: () =>
        channelsFixture.resolver?.() ??
        Promise.resolve([
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
        ]),
    }),
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

vi.mock("@multica/core/dm", async (importOriginal) => {
  const { dmMock } = await import("./__fixtures/channels-page-mocks");
  return dmMock(importOriginal);
});

// LRM-1399 — custom conversations mock that mirrors this file's resolver
// mechanism from `channelsOptions` so the pending → loaded skeleton behavior
// is preserved now that the page reads the unified conversations query.
vi.mock("@multica/core/conversations", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@multica/core/conversations")>();
  return {
    ...actual,
    conversationsOptions: () => ({
      queryKey: ["conversations", "ws-1", "list"],
      queryFn: async () => {
        const channels =
          channelsFixture.resolver?.() ??
          Promise.resolve([
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
          ]);
        const rows = await channels;
        return {
          items: rows.map((channel) => ({ kind: "channel" as const, channel })),
          next_cursor: undefined,
        };
      },
      initialPageParam: null as string | null,
      getNextPageParam: () => undefined,
    }),
  };
});

vi.mock("@multica/core/workspace/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workspace/queries")>()),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

vi.mock("@multica/core/auth", async () => {
  const { authMock } = await import("./__fixtures/channels-page-mocks");
  return authMock();
});

vi.mock("@multica/core/hooks", async (importOriginal) => {
  const { hooksMock } = await import("./__fixtures/channels-page-mocks");
  return hooksMock(importOriginal);
});

vi.mock("@multica/core/paths", async (importOriginal) => {
  const { pathsMock } = await import("./__fixtures/channels-page-mocks");
  return pathsMock(importOriginal);
});

vi.mock("@multica/core/realtime", async (importOriginal) => {
  const { realtimeMock } = await import("./__fixtures/channels-page-mocks");
  return realtimeMock(importOriginal);
});

vi.mock("@multica/core/hooks/use-file-upload", async () => {
  const { fileUploadMock } = await import("./__fixtures/channels-page-mocks");
  return fileUploadMock();
});

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  useContainerNarrowerThan: () => [false, () => {}] as const,
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/lazy-content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({ ChannelMessageList: () => <div data-testid="message-list" /> }));

vi.mock("./conversation-surface", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./conversation-surface")>()),
  ConversationHeader: ({ title }: { title?: string }) => (
    <div data-testid="conversation-header">{title}</div>
  ),
  Composer: () => <div data-testid="composer" />,
}));

const TEST_RESOURCES = { en: { common: enCommon, channels: enChannels } };

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ChannelsPage />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ChannelsPage sidebar list skeleton (LRM-459)", () => {
  beforeEach(() => {
    useLastSelectedChannelStore.getState().clearLastSelectedChannelId();
    channelsFixture.resolver = null;
  });

  it("shows CHANNELS row skeleton while list is pending, then real rows", async () => {
    let resolveChannels!: (value: unknown[]) => void;
    channelsFixture.resolver = () =>
      new Promise((resolve) => {
        resolveChannels = resolve;
      });

    renderPage();

    // Wait past InitialChannelsShellSkeleton (same testids) into the live list.
    expect(await screen.findByRole("heading", { name: "Messages" })).toBeInTheDocument();
    expect(await screen.findByTestId("channel-list-skeleton")).toBeInTheDocument();
    expect(screen.queryByText("general")).not.toBeInTheDocument();

    resolveChannels([
      {
        id: "chan-1",
        workspace_id: "ws-1",
        name: "general",
        kind: "group",
        description: null,
        lark_chat_id: null,
        created_by: "user-1",
        created_at: "2026-06-17T09:00:00Z",
        updated_at: "2026-06-17T09:00:00Z",
      },
    ]);

    await waitFor(() => {
      expect(screen.queryByTestId("channel-list-skeleton")).not.toBeInTheDocument();
    });
    // Sidebar row + conversation header both show the name.
    expect((await screen.findAllByText("general")).length).toBeGreaterThanOrEqual(1);
  });
});
