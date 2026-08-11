import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import type { DMItem } from "@multica/core/dm";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DmConversation } from "./dm-conversation";

// LRM-682 — the DM main area gains a 聊天 | 文件 tab bar (no Issues — a 1:1 has
// no issue context). 文件 is the 2nd tab and renders the shared
// ChannelFilesPanel wide; it is the single Files entry (the header files icon
// stays removed, LRM-675). Chat remains the default view. LRM-698: no count
// badge on the tab (Frank: the number is noise).

const apiMock = vi.hoisted(() => {
  const proxy = new Proxy({} as Record<string, unknown>, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

const ATTACHMENTS = [
  {
    id: "att-1",
    file_name: "notes.md",
    url: "https://files.example/notes.md",
    size: 12,
    created_at: "2026-07-28T09:00:00Z",
  },
  {
    id: "att-2",
    file_name: "shot.png",
    url: "https://files.example/shot.png",
    size: 34,
    created_at: "2026-07-28T09:01:00Z",
  },
];

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  return {
    ...actual,
    channelAttachmentsOptions: () => ({
      queryKey: ["dm-attachments"],
      queryFn: async () => ATTACHMENTS,
    }),
    channelMessageThreadOptions: () => ({
      queryKey: ["channel-thread"],
      queryFn: async () => ({ messages: [] }),
    }),
    channelMessagesPageOptions: () => ({
      queryKey: ["dm-messages"],
      queryFn: async () => ({
        messages: [
          {
            id: "m-1",
            channel_id: "dm-chan-1",
            workspace_id: "ws-1",
            seq: 1,
            type: "user",
            author_id: "peer-1",
            author_name: "Bob",
            content: "Hi",
            source: "multica",
            external_message_id: null,
            client_message_id: null,
            created_at: "2026-07-28T09:05:00Z",
          },
        ],
        next_cursor: null,
      }),
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

vi.mock("@multica/core/realtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: () => null,
    getMemberRole: () => null,
    getMemberHonor: () => ({ level: 42, name_style: "default" }),
    getAgentFleetRank: () => undefined,
    getAgentHonorLevel: () => undefined,
  }),
}));
vi.mock("@multica/core/agents", () => ({ useAgentPresence: () => "loading" }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

vi.mock("../../chat/components/dm-agent-bubble", () => ({
  DmAgentBubble: () => null,
}));
vi.mock("./dm-agent-working-cue", () => ({ DmAgentWorkingCue: () => null }));
vi.mock("./composer-agent-activity-strip", () => ({
  ComposerAgentActivityStrip: () => null,
}));
vi.mock("../../editor/lazy-content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));
vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

// Visible stand-in for the shared panel: the test asserts it is wired with the
// DM channelId + wide layout and only mounts under the 文件 tab — the panel's
// own thumbnails/lightbox/md-txt behavior is covered by
// channel-files-panel.test.tsx.
vi.mock("./channel-files-panel", () => ({
  ChannelFilesPanel: ({ channelId, wide }: { channelId: string; wide?: boolean }) => (
    <div data-testid="channel-files-panel" data-channel-id={channelId} data-wide={String(!!wide)} />
  ),
}));

vi.mock("react-virtuoso", async () => {
  const React = await import("react");
  const MockVirtuoso = React.forwardRef(
    (
      {
        components = {},
        data = [],
        itemContent,
      }: {
        components?: {
          Footer?: React.ComponentType;
          Header?: React.ComponentType;
          List?: React.ComponentType<React.HTMLAttributes<HTMLDivElement>>;
        };
        data?: ChannelMessage[];
        itemContent: (index: number, item: ChannelMessage) => React.ReactNode;
      },
      ref: React.ForwardedRef<{ scrollToIndex: (...args: unknown[]) => void }>,
    ) => {
      React.useImperativeHandle(ref, () => ({ scrollToIndex: vi.fn() }));
      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      return (
        <div data-testid="virtuoso-scroller">
          {Header ? <Header /> : null}
          <List>{data.map((item, index) => itemContent(index, item))}</List>
          {Footer ? <Footer /> : null}
        </div>
      );
    },
  );
  MockVirtuoso.displayName = "MockVirtuoso";
  return { Virtuoso: MockVirtuoso };
});

const dm: DMItem = {
  id: "dm-chan-1",
  source: "dm_channel",
  peer: { type: "user", id: "peer-1", name: "Bob" },
  unread: 0,
  updated_at: "2026-07-28T09:00:00Z",
};

function renderDm() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <DmConversation dm={dm} onBack={() => {}} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("LRM-682 DM 聊天|文件 tab bar", () => {
  it("shows the user's armor level crest in the DM header and message identity", async () => {
    const { container } = renderDm();

    await screen.findByTestId("virtuoso-scroller");
    expect(
      container.querySelectorAll('[data-user-honor-level="42"]').length,
    ).toBeGreaterThanOrEqual(2);
  });

  it("renders exactly 聊天 | 文件 (Files 2nd, no count badge per LRM-698) — no Issues tab", async () => {
    renderDm();
    const tablist = await screen.findByRole("tablist");
    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(2);
    expect(tabs[0]).toHaveTextContent(enChannels.view_tabs.dm_chat);
    expect(tabs[1]).toHaveTextContent(enChannels.view_tabs.files);
    expect(screen.queryByRole("tab", { name: /issues/i })).not.toBeInTheDocument();
    // LRM-698: the tab is the label only — no attachment count badge, even
    // after the async surfaces settle.
    expect(await screen.findByTestId("virtuoso-scroller")).toBeInTheDocument();
    expect(tabs[1]).not.toHaveTextContent(/\d/);
    expect(tablist).toBeInTheDocument();
  });

  it("defaults to chat and mounts ChannelFilesPanel (wide, DM channelId) only under the 文件 tab", async () => {
    const user = userEvent.setup();
    renderDm();
    await screen.findByRole("tablist");
    // Default: chat content mounted, files panel absent.
    expect(await screen.findByTestId("virtuoso-scroller")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-files-panel")).not.toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: new RegExp(enChannels.view_tabs.files) }));
    const panel = await screen.findByTestId("channel-files-panel");
    expect(panel).toHaveAttribute("data-channel-id", "dm-chan-1");
    expect(panel).toHaveAttribute("data-wide", "true");
    // Chat surface swaps out — one main view at a time.
    expect(screen.queryByTestId("virtuoso-scroller")).not.toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: enChannels.view_tabs.dm_chat }));
    expect(await screen.findByTestId("virtuoso-scroller")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-files-panel")).not.toBeInTheDocument();
  });
});
