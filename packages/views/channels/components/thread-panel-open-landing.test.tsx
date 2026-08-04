import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ThreadPanel } from "./thread-panel";

// LRM-1156 — where a thread LANDS when it opens. Frank:「thread里读消息，能不能
// 优先跳到最后一行？别老是让我滑动，有点烦」. This goes through the REAL
// ChannelMessageList (not a props spy) so the assertion is the position the
// virtualized list actually mounts at, which is the thing that was wrong: the
// thread surface used to override the chat default with a top anchor.

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const apiMock = vi.hoisted(() => {
  const known: Record<string, unknown> = {};
  return {
    proxy: new Proxy(known, {
      get(target, prop) {
        if (typeof prop !== "string") return undefined;
        if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
        return target[prop];
      },
    }),
  };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

// Records the mount position the list asks Virtuoso for. Real Virtuoso resolves
// this against the LOCAL data array (see the #689/#1189 index contract).
vi.mock("react-virtuoso", async () => {
  const React = await import("react");
  const MockVirtuoso = React.forwardRef(
    (
      {
        components = {},
        data = [],
        itemContent,
        initialTopMostItemIndex,
        firstItemIndex = 0,
      }: {
        components?: {
          Footer?: React.ComponentType;
          Header?: React.ComponentType;
          List?: React.ComponentType<React.HTMLAttributes<HTMLDivElement>>;
        };
        data?: ChannelMessage[];
        initialTopMostItemIndex?: number | { index: number; align?: string };
        firstItemIndex?: number;
        itemContent: (index: number, item: ChannelMessage) => React.ReactNode;
      },
      ref: React.ForwardedRef<{ scrollToIndex: (...args: unknown[]) => void }>,
    ) => {
      React.useImperativeHandle(ref, () => ({ scrollToIndex: vi.fn() }));
      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      const initialIndex =
        typeof initialTopMostItemIndex === "object" && initialTopMostItemIndex !== null
          ? initialTopMostItemIndex.index
          : initialTopMostItemIndex;
      return (
        <div
          data-testid="virtuoso-scroller"
          data-initial-index={initialIndex ?? "unset"}
          data-first-item-index={firstItemIndex}
        >
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

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "Alice" } }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: () => null,
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
  }),
}));
vi.mock("@multica/core/agents", () => ({ useAgentPresenceDetail: () => "loading" }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));
vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));
// Reaction attribution + identity resolution are not what this suite is about;
// stub them so the bubble rows stay free of workspace/query wiring (same
// rationale as channel-message-list.test.tsx).
vi.mock("../../common/use-reaction-actor-name", () => ({
  useReactionActorName: () => (type: string, id: string) => id || type,
}));
vi.mock("../../common/use-resolved-actor-identity", () => ({
  useResolvedActorIdentity: () => ({ displayName: "Test Actor", avatarUrl: null }),
  mentionTypeFromActorType: () => null,
  resolvedActorLabel: (identity: { displayName: string | null }, actorId?: string) =>
    identity.displayName ?? actorId ?? "",
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));
vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

function message(id: string, content: string, seq: number): ChannelMessage {
  return {
    id,
    channel_id: "chan-1",
    workspace_id: "ws-1",
    seq,
    type: "user",
    author_id: "peer-1",
    author_name: "Bob",
    content,
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-21T01:00:00Z",
  };
}

const ROOT = message("root", "Root of the thread", 1);
const REPLIES = [
  message("r1", "Oldest reply", 2),
  message("r2", "Middle reply", 3),
  message("r3", "Newest reply", 4),
];

function renderThread(extra: Partial<React.ComponentProps<typeof ThreadPanel>> = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ThreadPanel
          root={ROOT}
          replies={REPLIES}
          currentUserId="user-1"
          isMobile={false}
          onBack={() => {}}
          followed={false}
          onFollowChange={() => {}}
          editor={<div data-testid="thread-editor" />}
          onSend={() => {}}
          sendDisabled={false}
          {...extra}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ThreadPanel open landing (LRM-1156)", () => {
  it("opens on the newest reply instead of the top of the reply list", () => {
    renderThread();

    // 3 replies → the last LOCAL index is 2. A top anchor would be 0, which is
    // what forced Frank to scroll down on every thread open.
    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute("data-initial-index", "2");
    // The pinned root stays in the scroll window's header (reachable by
    // scrolling up); landing at the bottom does not remove it.
    expect(screen.getByText("Root of the thread")).toBeInTheDocument();
  });

  it("still honours a deep-linked reply anchor over the newest reply", () => {
    renderThread({ highlightMessageId: "r1" });

    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute("data-initial-index", "0");
  });
});
